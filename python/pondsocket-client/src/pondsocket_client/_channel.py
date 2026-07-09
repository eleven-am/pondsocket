from __future__ import annotations

import asyncio
import contextlib
from collections import deque
from collections.abc import Callable
from typing import Any, TypeAlias

from pondsocket_common import (
    BehaviorSubject,
    ChannelEvent,
    ChannelState,
    ClientActions,
    ClientMessage,
    Events,
    JoinParams,
    PondMessage,
    PondPresence,
    PresenceEventTypes,
    PresenceMessage,
    PresencePayloadModel,
    ServerActions,
    ServerMessage,
    Subject,
    Unsubscribe,
    uuid,
)

from .types import ConnectionState, Publisher

ChannelStateHandler: TypeAlias = Callable[[ChannelState], None]
MessageHandler: TypeAlias = Callable[[ServerMessage], None]
PresenceHandler: TypeAlias = Callable[[PondPresence], None]
PresenceUpdateHandler: TypeAlias = Callable[[PresencePayloadModel], None]
UsersChangeHandler: TypeAlias = Callable[[list[PondPresence]], None]


class ResponseTimeoutError(Exception):
    pass


class Channel:
    __slots__ = (
        "_closed",
        "_connection_state",
        "_connection_unsub",
        "_events",
        "_params",
        "_pending_responses",
        "_presence",
        "_publisher",
        "_queue",
        "_response_timeout",
        "_state",
        "_tasks",
        "name",
    )

    def __init__(
        self,
        *,
        name: str,
        params: JoinParams | None,
        publisher: Publisher,
        connection_state: BehaviorSubject[ConnectionState],
        max_queue_size: int = 100,
        response_timeout: float = 5.0,
    ) -> None:
        self.name = name
        self._params: JoinParams = dict(params or {})
        self._publisher = publisher
        self._connection_state = connection_state
        self._state: BehaviorSubject[ChannelState] = BehaviorSubject(ChannelState.IDLE)
        self._events: Subject[ChannelEvent] = Subject()
        self._presence: list[PondPresence] = []
        self._queue: deque[ClientMessage] = deque(maxlen=max_queue_size)
        self._pending_responses: dict[str, asyncio.Future[PondMessage]] = {}
        self._response_timeout = response_timeout
        self._tasks: set[asyncio.Task[None]] = set()
        self._closed = False
        self._connection_unsub: Unsubscribe = self._connection_state.subscribe(
            self.handle_connection_state
        )

    def _spawn(self, coro: Any) -> None:
        task = asyncio.create_task(coro)
        self._tasks.add(task)
        task.add_done_callback(self._tasks.discard)

    @property
    def channel_state(self) -> ChannelState:
        value = self._state.value
        return value if value is not None else ChannelState.IDLE

    @property
    def presence(self) -> list[PondPresence]:
        return list(self._presence)

    def join(self) -> None:
        if self._closed:
            return
        state = self.channel_state
        if state in (ChannelState.JOINING, ChannelState.JOINED, ChannelState.DECLINED):
            return
        self._state.publish(ChannelState.JOINING)
        msg = self._build_join_message()
        self._enqueue_or_send(msg)

    def leave(self) -> None:
        if self._closed:
            return
        msg = ClientMessage(
            action=ClientActions.LEAVE_CHANNEL,
            channelName=self.name,
            event=ClientActions.LEAVE_CHANNEL.value,
            requestId=uuid(),
            payload={},
        )
        if self._connection_state.value == ConnectionState.CONNECTED:
            self._spawn(self._publish(msg))
        self._state.publish(ChannelState.CLOSED)
        self._closed = True
        self._teardown()

    def send_message(self, event: str, payload: PondMessage | None = None) -> None:
        if self._closed:
            return
        msg = ClientMessage(
            action=ClientActions.BROADCAST,
            channelName=self.name,
            event=event,
            requestId=uuid(),
            payload=dict(payload or {}),
        )
        self._enqueue_or_send(msg)

    async def send_for_response(
        self,
        event: str,
        payload: PondMessage | None = None,
        *,
        wait: float | None = None,
    ) -> PondMessage:
        if self._closed:
            raise ResponseTimeoutError("channel is closed")
        request_id = uuid()
        loop = asyncio.get_running_loop()
        fut: asyncio.Future[PondMessage] = loop.create_future()
        self._pending_responses[request_id] = fut
        msg = ClientMessage(
            action=ClientActions.BROADCAST,
            channelName=self.name,
            event=event,
            requestId=request_id,
            payload=dict(payload or {}),
        )
        self._enqueue_or_send(msg)
        wait_seconds = wait if wait is not None else self._response_timeout
        try:
            return await asyncio.wait_for(fut, timeout=wait_seconds)
        except TimeoutError as e:
            raise ResponseTimeoutError(
                f"no response received for event {event!r} within {wait_seconds}s"
            ) from e
        finally:
            self._pending_responses.pop(request_id, None)

    def handle_event(self, event: ChannelEvent) -> None:
        if self._closed:
            return
        if isinstance(event, PresenceMessage):
            self._presence = [dict(p) for p in event.payload.presence]
            if self.channel_state == ChannelState.JOINED:
                self._events.publish(event)
            return
        if event.request_id in self._pending_responses:
            fut = self._pending_responses.pop(event.request_id)
            if not fut.done():
                payload = event.payload if isinstance(event.payload, dict) else {}
                fut.set_result(payload)
            return
        if event.action == ServerActions.SYSTEM:
            if event.event == Events.ACKNOWLEDGE.value:
                self._acknowledge()
                return
            if event.event in (Events.UNAUTHORIZED.value, Events.NOT_FOUND.value):
                if self.channel_state in (ChannelState.JOINING, ChannelState.STALLED):
                    self._decline()
                return
        if self.channel_state == ChannelState.JOINED:
            self._events.publish(event)

    def handle_connection_state(self, state: ConnectionState) -> None:
        if self._closed:
            return
        if state == ConnectionState.CONNECTED:
            if self.channel_state == ChannelState.STALLED:
                msg = self._build_join_message()
                self._spawn(self._publish(msg))
                return
            if self.channel_state == ChannelState.JOINING:
                self._drain_queued_joins()
        elif state == ConnectionState.DISCONNECTED:
            if self.channel_state == ChannelState.JOINED:
                self._state.publish(ChannelState.STALLED)

    def _drain_queued_joins(self) -> None:
        remaining: deque[ClientMessage] = deque(maxlen=self._queue.maxlen)
        for msg in self._queue:
            if msg.action == ClientActions.JOIN_CHANNEL:
                self._spawn(self._publish(msg))
            else:
                remaining.append(msg)
        self._queue = remaining

    def on_channel_state_change(self, callback: ChannelStateHandler) -> Unsubscribe:
        return self._state.subscribe(callback)

    def on_message(self, callback: MessageHandler) -> Unsubscribe:
        def filtered(ev: ChannelEvent) -> None:
            if isinstance(ev, ServerMessage):
                callback(ev)

        return self._events.subscribe(filtered)

    def on_message_event(self, event_name: str, callback: MessageHandler) -> Unsubscribe:
        def filtered(ev: ChannelEvent) -> None:
            if isinstance(ev, ServerMessage) and ev.event == event_name:
                callback(ev)

        return self._events.subscribe(filtered)

    def on_join(self, callback: PresenceHandler) -> Unsubscribe:
        return self._on_presence_kind(PresenceEventTypes.JOIN, callback)

    def on_leave(self, callback: PresenceHandler) -> Unsubscribe:
        return self._on_presence_kind(PresenceEventTypes.LEAVE, callback)

    def on_presence_change(self, callback: PresenceUpdateHandler) -> Unsubscribe:
        def filtered(ev: ChannelEvent) -> None:
            if isinstance(ev, PresenceMessage) and ev.event == PresenceEventTypes.UPDATE:
                callback(ev.payload)

        return self._events.subscribe(filtered)

    def on_users_change(self, callback: UsersChangeHandler) -> Unsubscribe:
        def filtered(ev: ChannelEvent) -> None:
            if isinstance(ev, PresenceMessage):
                callback([dict(p) for p in ev.payload.presence])

        return self._events.subscribe(filtered)

    def _on_presence_kind(self, kind: PresenceEventTypes, callback: PresenceHandler) -> Unsubscribe:
        def filtered(ev: ChannelEvent) -> None:
            if isinstance(ev, PresenceMessage) and ev.event == kind:
                callback(dict(ev.payload.changed))

        return self._events.subscribe(filtered)

    def _build_join_message(self) -> ClientMessage:
        return ClientMessage(
            action=ClientActions.JOIN_CHANNEL,
            channelName=self.name,
            event=ClientActions.JOIN_CHANNEL.value,
            requestId=uuid(),
            payload=dict(self._params),
        )

    def _enqueue_or_send(self, msg: ClientMessage) -> None:
        if (
            self._connection_state.value == ConnectionState.CONNECTED
            and self.channel_state == ChannelState.JOINED
        ):
            self._spawn(self._publish(msg))
            return
        is_join = msg.action == ClientActions.JOIN_CHANNEL
        if self._connection_state.value == ConnectionState.CONNECTED and is_join:
            self._spawn(self._publish(msg))
            return
        self._queue.append(msg)

    def _flush_queue(self) -> None:
        if not self._queue:
            return
        pending = list(self._queue)
        self._queue.clear()
        for msg in pending:
            self._spawn(self._publish(msg))

    def _acknowledge(self) -> None:
        if self.channel_state not in (ChannelState.JOINING, ChannelState.STALLED):
            return
        self._state.publish(ChannelState.JOINED)
        self._flush_queue()

    def _decline(self) -> None:
        self._state.publish(ChannelState.DECLINED)
        self._queue.clear()
        for fut in self._pending_responses.values():
            if not fut.done():
                fut.cancel()
        self._pending_responses.clear()

    async def _publish(self, msg: ClientMessage) -> None:
        try:
            await self._publisher(msg)
        except Exception:
            return

    def _teardown(self) -> None:
        with contextlib.suppress(Exception):
            self._connection_unsub()
        for fut in self._pending_responses.values():
            if not fut.done():
                fut.cancel()
        self._pending_responses.clear()
        self._queue.clear()
        self._events.close()

    def _force_close(self) -> None:
        if self._closed:
            return
        self._state.publish(ChannelState.CLOSED)
        self._closed = True
        self._teardown()

    def _close_state_subject(self) -> None:
        self._state.close()


_publisher_alias: Any = Publisher

__all__ = [
    "Channel",
    "ChannelStateHandler",
    "MessageHandler",
    "PresenceHandler",
    "PresenceUpdateHandler",
    "ResponseTimeoutError",
    "UsersChangeHandler",
]
