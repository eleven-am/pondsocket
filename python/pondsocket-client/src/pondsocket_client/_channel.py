from __future__ import annotations

import asyncio
import contextlib
from collections import deque
from collections.abc import Callable
from typing import TypeAlias

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


class ChannelQueueFullError(Exception):
    pass


class Channel:
    __slots__ = (
        "_closed",
        "_closing",
        "_connection_state",
        "_connection_unsub",
        "_drain_task",
        "_events",
        "_join_generation",
        "_join_request_id",
        "_max_queue_size",
        "_params",
        "_pending_join",
        "_pending_responses",
        "_presence",
        "_publisher",
        "_queue",
        "_response_timeout",
        "_state",
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
        self._join_generation = 0
        self._join_request_id: str | None = None
        if max_queue_size <= 0:
            raise ValueError("max_queue_size must be greater than zero")
        self._max_queue_size = max_queue_size
        self._queue: deque[ClientMessage] = deque()
        self._pending_join: ClientMessage | None = None
        self._pending_responses: dict[str, asyncio.Future[PondMessage]] = {}
        self._response_timeout = response_timeout
        self._drain_task: asyncio.Task[None] | None = None
        self._closed = False
        self._closing = False
        self._connection_unsub: Unsubscribe = self._connection_state.subscribe(
            self.handle_connection_state
        )

    @property
    def channel_state(self) -> ChannelState:
        value = self._state.value
        return value if value is not None else ChannelState.IDLE

    @property
    def presence(self) -> list[PondPresence]:
        return list(self._presence)

    def join(self) -> None:
        if self._closed or self._closing:
            return
        state = self.channel_state
        if state in (ChannelState.JOINING, ChannelState.JOINED, ChannelState.DECLINED):
            return
        self._state.publish(ChannelState.JOINING)
        self._prepare_join()
        self._ensure_drain()

    def leave(self) -> None:
        if self._closed or self._closing:
            return
        msg = ClientMessage(
            action=ClientActions.LEAVE_CHANNEL,
            channelName=self.name,
            event=ClientActions.LEAVE_CHANNEL.value,
            requestId=uuid(),
            payload={},
        )
        self._closing = True
        self._state.publish(ChannelState.CLOSED)
        self._pending_join = None
        if self._connection_state.value != ConnectionState.CONNECTED:
            self._queue.clear()
            self._finish_close()
            return
        self._queue.append(msg)
        self._ensure_drain()

    def send_message(self, event: str, payload: PondMessage | None = None) -> None:
        if self._closed or self._closing:
            return
        msg = ClientMessage(
            action=ClientActions.BROADCAST,
            channelName=self.name,
            event=event,
            requestId=uuid(),
            payload=dict(payload or {}),
        )
        self._enqueue(msg)

    async def send_for_response(
        self,
        event: str,
        payload: PondMessage | None = None,
        *,
        wait: float | None = None,
    ) -> PondMessage:
        if self._closed or self._closing:
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
        try:
            self._enqueue(msg)
        except Exception:
            self._pending_responses.pop(request_id, None)
            raise
        wait_seconds = wait if wait is not None else self._response_timeout
        try:
            return await asyncio.wait_for(fut, timeout=wait_seconds)
        except TimeoutError as e:
            raise ResponseTimeoutError(
                f"no response received for event {event!r} within {wait_seconds}s"
            ) from e
        finally:
            self._pending_responses.pop(request_id, None)
            self._remove_queued_request(request_id)

    def handle_event(self, event: ChannelEvent) -> None:
        if self._closed or self._closing:
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
                if not self._is_current_join_response(event.request_id):
                    return
                self._acknowledge()
                return
            if event.event in (Events.UNAUTHORIZED.value, Events.NOT_FOUND.value):
                if self.channel_state in (ChannelState.JOINING, ChannelState.STALLED):
                    if not self._is_current_join_response(event.request_id):
                        return
                    self._decline()
                return
        if self.channel_state == ChannelState.JOINED:
            self._events.publish(event)

    def handle_connection_state(self, state: ConnectionState) -> None:
        if self._closed:
            return
        if state == ConnectionState.CONNECTED:
            if (
                self.channel_state in (ChannelState.JOINING, ChannelState.STALLED)
                and self._pending_join is None
            ):
                self._prepare_join()
            if self._closing or self.channel_state in (
                ChannelState.JOINING,
                ChannelState.STALLED,
            ):
                self._ensure_drain()
        elif state == ConnectionState.DISCONNECTED:
            if self.channel_state in (ChannelState.JOINING, ChannelState.JOINED):
                self._state.publish(ChannelState.STALLED)

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

    def _prepare_join(self) -> None:
        msg = self._build_join_message()
        self._join_generation += 1
        self._join_request_id = msg.request_id
        self._pending_join = msg

    def _is_current_join_response(self, request_id: str) -> bool:
        if self._join_generation <= 1:
            return True
        return request_id == self._join_request_id

    def _enqueue(self, msg: ClientMessage) -> None:
        if len(self._queue) >= self._max_queue_size:
            raise ChannelQueueFullError(
                f"channel {self.name!r} outbound queue is full "
                f"(max {self._max_queue_size})"
            )
        self._queue.append(msg)
        self._ensure_drain()

    def _remove_queued_request(self, request_id: str) -> None:
        self._queue = deque(
            msg for msg in self._queue if msg.request_id != request_id
        )

    def _ensure_drain(self) -> None:
        if self._closed:
            return
        if self._drain_task is not None and not self._drain_task.done():
            return
        self._drain_task = asyncio.create_task(self._drain())
        self._drain_task.add_done_callback(self._on_drain_done)

    def _on_drain_done(self, task: asyncio.Task[None]) -> None:
        if self._drain_task is task:
            self._drain_task = None
        if not task.cancelled():
            task.exception()

    async def _drain(self) -> None:
        while not self._closed:
            if self._connection_state.value != ConnectionState.CONNECTED:
                return
            if self._pending_join is not None:
                msg = self._pending_join
                try:
                    await self._publisher(msg)
                except asyncio.CancelledError:
                    raise
                except Exception:
                    return
                if self._pending_join is msg:
                    self._pending_join = None
                continue
            if not self._queue:
                return
            if not self._closing and self.channel_state != ChannelState.JOINED:
                return
            msg = self._queue[0]
            try:
                await self._publisher(msg)
            except asyncio.CancelledError:
                raise
            except Exception:
                return
            if self._queue and self._queue[0] is msg:
                self._queue.popleft()
            if msg.action == ClientActions.LEAVE_CHANNEL:
                self._finish_close()
                return

    def _acknowledge(self) -> None:
        if self.channel_state not in (ChannelState.JOINING, ChannelState.STALLED):
            return
        self._join_request_id = None
        self._state.publish(ChannelState.JOINED)
        self._ensure_drain()

    def _decline(self) -> None:
        self._state.publish(ChannelState.DECLINED)
        self._join_request_id = None
        self._pending_join = None
        self._closed = True
        self._teardown()

    def _teardown(self) -> None:
        with contextlib.suppress(Exception):
            self._connection_unsub()
        for fut in self._pending_responses.values():
            if not fut.done():
                fut.cancel()
        self._pending_responses.clear()
        self._queue.clear()
        self._events.close()

    def _finish_close(self) -> None:
        self._closed = True
        self._closing = False
        self._teardown()

    def _force_close(self) -> asyncio.Task[None] | None:
        if self._closed:
            return self._drain_task
        self._state.publish(ChannelState.CLOSED)
        self._closed = True
        self._closing = False
        task = self._drain_task
        if task is not None and not task.done() and task is not asyncio.current_task():
            task.cancel()
        self._teardown()
        return task

    def _close_state_subject(self) -> None:
        self._state.close()

    def _is_replaceable(self) -> bool:
        return self._closed or self.channel_state == ChannelState.DECLINED

__all__ = [
    "Channel",
    "ChannelQueueFullError",
    "ChannelStateHandler",
    "MessageHandler",
    "PresenceHandler",
    "PresenceUpdateHandler",
    "ResponseTimeoutError",
    "UsersChangeHandler",
]
