from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from typing import Any, TypeAlias

from pondsocket import (
    ConnectionContext,
    Endpoint,
    Event,
    PondSocket,
    SystemEntity,
)
from pondsocket.sse_transport import SSEServerTransport
from pondsocket.transport import PushableTransport, Transport
from pondsocket_common import Headers, ServerActions, uuid

from ._scope import build_incoming_connection
from ._sse import event_to_sse_frame
from .transport import ASGIWebSocketTransport

ASGISend: TypeAlias = Callable[[dict[str, Any]], Awaitable[None]]
ASGIReceive: TypeAlias = Callable[[], Awaitable[dict[str, Any]]]

_SSE_INIT_HEADERS: list[tuple[bytes, bytes]] = [
    (b"content-type", b"text/event-stream; charset=utf-8"),
    (b"cache-control", b"no-cache, no-transform"),
    (b"connection", b"keep-alive"),
    (b"x-accel-buffering", b"no"),
]


def _http_to_ws_close_code(http_code: int) -> int:
    if 1000 <= http_code <= 4999:
        return http_code
    if 100 <= http_code <= 999:
        return 4000 + http_code
    return 1008


class PondASGIApp:
    __slots__ = ("_pond",)

    def __init__(self, pond: PondSocket) -> None:
        self._pond = pond

    @property
    def pond(self) -> PondSocket:
        return self._pond

    async def __call__(
        self,
        scope: dict[str, Any],
        receive: ASGIReceive,
        send: ASGISend,
    ) -> None:
        scope_type = scope.get("type")
        if scope_type == "lifespan":
            await self._handle_lifespan(receive, send)
            return
        if scope_type == "websocket":
            await self._handle_websocket(scope, receive, send)
            return
        if scope_type == "http":
            await self._handle_http(scope, receive, send)
            return
        raise RuntimeError(f"PondASGIApp does not support scope type: {scope_type!r}")

    async def _handle_lifespan(self, receive: ASGIReceive, send: ASGISend) -> None:
        while True:
            event = await receive()
            event_type = event.get("type")
            if event_type == "lifespan.startup":
                await send({"type": "lifespan.startup.complete"})
            elif event_type == "lifespan.shutdown":
                try:
                    await self._pond.close()
                except Exception as e:
                    await send(
                        {
                            "type": "lifespan.shutdown.failed",
                            "message": str(e),
                        }
                    )
                    return
                await send({"type": "lifespan.shutdown.complete"})
                return
            else:
                return

    async def _handle_websocket(
        self,
        scope: dict[str, Any],
        receive: ASGIReceive,
        send: ASGISend,
    ) -> None:
        path = scope.get("path", "")
        match = self._pond.match_endpoint(path)
        if match is None:
            await self._reject_websocket_before_accept(
                send, code=4404, reason="endpoint not found"
            )
            return
        endpoint = match.endpoint
        route = match.route

        first = await receive()
        if first.get("type") != "websocket.connect":
            return

        user_id = uuid()
        incoming = build_incoming_connection(
            user_id=user_id, scope=scope, route=route
        )
        ctx = await endpoint.request_connection(incoming, user_id=user_id)

        if ctx.is_declined:
            code, message = ctx.decline_info
            await self._reject_websocket_before_accept(
                send, code=_http_to_ws_close_code(code), reason=message
            )
            return

        await send({"type": "websocket.accept"})
        transport = ASGIWebSocketTransport(
            id=ctx.user_id,
            send=send,
            receive=receive,
            assigns=ctx.assigns,
        )
        await endpoint.register_transport(transport)
        await self._send_pending_reply(ctx, transport, endpoint)
        await transport.wait_until_closed()

    async def _handle_http(
        self,
        scope: dict[str, Any],
        receive: ASGIReceive,
        send: ASGISend,
    ) -> None:
        method = scope.get("method", "")
        headers = Headers(scope.get("headers", []))

        if method == "GET" and _is_sse_request(headers):
            await self._handle_sse_init(scope, receive, send, headers)
            return
        connection_id = headers.get("x-connection-id")
        if method == "POST" and connection_id:
            await self._handle_sse_push(receive, send, connection_id)
            return
        if method == "DELETE" and connection_id:
            await self._handle_sse_disconnect(send, connection_id)
            return
        await self._send_simple_http(send, status=404, body=b"PondSocket: not found")

    async def _handle_sse_init(
        self,
        scope: dict[str, Any],
        receive: ASGIReceive,
        send: ASGISend,
        _headers: Headers,
    ) -> None:
        path = scope.get("path", "")
        match = self._pond.match_endpoint(path)
        if match is None:
            await self._send_simple_http(
                send, status=404, body=b"endpoint not found"
            )
            return
        endpoint = match.endpoint
        route = match.route

        user_id = uuid()
        incoming = build_incoming_connection(
            user_id=user_id, scope=scope, route=route
        )
        ctx = await endpoint.request_connection(incoming, user_id=user_id)

        if ctx.is_declined:
            code, message = ctx.decline_info
            await self._send_simple_http(
                send, status=code, body=message.encode("utf-8")
            )
            return

        sse_headers: list[tuple[bytes, bytes]] = [
            *_SSE_INIT_HEADERS,
            (b"x-connection-id", ctx.user_id.encode("utf-8")),
        ]
        await send(
            {
                "type": "http.response.start",
                "status": 200,
                "headers": sse_headers,
            }
        )

        transport = SSEServerTransport(id=ctx.user_id, assigns=ctx.assigns)
        await endpoint.register_transport(transport)
        await self._send_pending_reply_sse(ctx, transport)

        await self._stream_sse(transport, receive, send)

    async def _stream_sse(
        self,
        transport: SSEServerTransport,
        receive: ASGIReceive,
        send: ASGISend,
    ) -> None:
        disconnect_task = asyncio.create_task(_watch_disconnect(receive))
        try:
            while transport.is_active():
                next_task = asyncio.create_task(transport.next_event())
                done, _pending = await asyncio.wait(
                    {next_task, disconnect_task},
                    return_when=asyncio.FIRST_COMPLETED,
                )
                if disconnect_task in done:
                    next_task.cancel()
                    break
                event = next_task.result()
                if event is None:
                    break
                frame = event_to_sse_frame(event)
                try:
                    await send(
                        {
                            "type": "http.response.body",
                            "body": frame,
                            "more_body": True,
                        }
                    )
                except Exception:
                    break
        finally:
            disconnect_task.cancel()
            await transport.close()
            try:
                await send(
                    {"type": "http.response.body", "body": b"", "more_body": False}
                )
            except Exception:
                return

    async def _handle_sse_push(
        self,
        receive: ASGIReceive,
        send: ASGISend,
        connection_id: str,
    ) -> None:
        transport = await self._pond.find_transport(connection_id)
        if transport is None:
            await self._send_simple_http(
                send, status=404, body=b"connection not found"
            )
            return
        if not isinstance(transport, PushableTransport):
            await self._send_simple_http(
                send,
                status=400,
                body=b"connection does not accept inbound messages",
            )
            return
        body = await _read_body(receive)
        await transport.push_message(body)
        await self._send_simple_http(send, status=204, body=b"")

    async def _handle_sse_disconnect(
        self,
        send: ASGISend,
        connection_id: str,
    ) -> None:
        transport = await self._pond.find_transport(connection_id)
        if transport is None:
            await self._send_simple_http(send, status=404, body=b"")
            return
        await transport.close()
        await self._send_simple_http(send, status=204, body=b"")

    async def _send_pending_reply(
        self,
        ctx: ConnectionContext,
        transport: Transport,
        _endpoint: Endpoint,
    ) -> None:
        if ctx.pending_reply is None:
            return
        name, payload = ctx.pending_reply
        ev = Event(
            action=ServerActions.SYSTEM.value,
            channel_name=SystemEntity.GATEWAY.value,
            request_id=uuid(),
            event=name,
            payload=payload,
        )
        try:
            await transport.send_event(ev)
        except Exception:
            return

    async def _send_pending_reply_sse(
        self,
        ctx: ConnectionContext,
        transport: SSEServerTransport,
    ) -> None:
        if ctx.pending_reply is None:
            return
        name, payload = ctx.pending_reply
        ev = Event(
            action=ServerActions.SYSTEM.value,
            channel_name=SystemEntity.GATEWAY.value,
            request_id=uuid(),
            event=name,
            payload=payload,
        )
        try:
            await transport.send_event(ev)
        except Exception:
            return

    async def _reject_websocket_before_accept(
        self,
        send: ASGISend,
        *,
        code: int,
        reason: str,
    ) -> None:
        first = {"type": "websocket.close", "code": code, "reason": reason[:123]}
        try:
            await send(first)
        except Exception:
            return

    async def _send_simple_http(
        self,
        send: ASGISend,
        *,
        status: int,
        body: bytes,
    ) -> None:
        try:
            await send(
                {
                    "type": "http.response.start",
                    "status": status,
                    "headers": [(b"content-type", b"text/plain; charset=utf-8")],
                }
            )
            await send(
                {
                    "type": "http.response.body",
                    "body": body,
                    "more_body": False,
                }
            )
        except Exception:
            return


def _is_sse_request(headers: Headers) -> bool:
    accept = headers.get("accept", "")
    return "text/event-stream" in accept


async def _read_body(receive: ASGIReceive) -> bytes:
    chunks: list[bytes] = []
    while True:
        msg = await receive()
        msg_type = msg.get("type")
        if msg_type == "http.request":
            chunks.append(msg.get("body", b""))
            if not msg.get("more_body", False):
                break
        elif msg_type == "http.disconnect":
            break
    return b"".join(chunks)


async def _watch_disconnect(receive: ASGIReceive) -> None:
    while True:
        try:
            msg = await receive()
        except asyncio.CancelledError:
            return
        if msg.get("type") == "http.disconnect":
            return
