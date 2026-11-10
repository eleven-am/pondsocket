from __future__ import annotations

import abc
import asyncio
import contextlib
import json
from typing import TYPE_CHECKING

from pondsocket_common import ClientMessage

from ._base import BaseClient

if TYPE_CHECKING:
    from websockets.asyncio.client import ClientConnection


class WebSocketClient(BaseClient, abc.ABC):
    __slots__ = ("_recv_task", "_socket")

    def __init__(self, *args: object, **kwargs: object) -> None:
        super().__init__(*args, **kwargs)  # type: ignore[arg-type]
        self._socket: ClientConnection | None = None
        self._recv_task: asyncio.Task[None] | None = None

    @abc.abstractmethod
    def _resolve_url(self) -> str: ...

    async def _open_transport(self) -> None:
        from websockets.asyncio.client import connect

        url = self._resolve_url()
        timeout = self._options.connection_timeout
        try:
            socket = await asyncio.wait_for(connect(url), timeout=timeout)
        except TimeoutError as e:
            raise ConnectionError(f"WebSocket connection to {url} timed out") from e
        self._socket = socket
        self._mark_connected()
        self._recv_task = asyncio.create_task(self._receive_loop())

    async def _close_transport(self) -> None:
        recv_task = self._recv_task
        self._recv_task = None
        if recv_task is not None and not recv_task.done():
            recv_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await recv_task
        socket = self._socket
        self._socket = None
        if socket is not None:
            try:
                await socket.close()
            except Exception as exc:
                self._errors.publish(exc)

    async def _send_outbound(self, message: ClientMessage) -> None:
        socket = self._socket
        if socket is None:
            raise ConnectionError("WebSocket is not connected")
        wire = message.model_dump(by_alias=True, mode="json")
        await socket.send(json.dumps(wire))

    async def _receive_loop(self) -> None:
        socket = self._socket
        if socket is None:
            return
        try:
            async for frame in socket:
                if isinstance(frame, bytes):
                    try:
                        text = frame.decode("utf-8")
                    except UnicodeDecodeError:
                        continue
                else:
                    text = frame
                self._handle_inbound_text(text)
        except asyncio.CancelledError:
            return
        except Exception as e:
            self._errors.publish(e)
        finally:
            if self._socket is socket:
                self._socket = None
            self._mark_disconnected()


__all__ = ["WebSocketClient"]
