from __future__ import annotations

import asyncio
import contextlib
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any, TypeAlias

from pondsocket_common import (
    PondAssigns,
    PondMessage,
    PondPresence,
    PresenceEventTypes,
    ServerActions,
    uuid,
)

from .contexts.leave_context import LeaveContext
from .contexts.outgoing_context import OutgoingContext
from .distributed_wire import distributed_bytes, parse_envelope
from .errors import internal, not_found
from .heartbeat import HeartbeatCoordinator
from .middleware import Middleware, execute_with_middleware
from .presence import PresenceClient
from .pubsub import PubSub, format_topic
from .store import Store
from .transport import Transport
from .types import (
    AllExceptSender,
    AllUsers,
    DistributedMessageType,
    Event,
    InternalActions,
    LeaveReason,
    MessageEvent,
    Options,
    RecipientSpec,
    SystemEvents,
    ToUsers,
    User,
)

_LEGACY_DISTRIBUTED_TYPES = frozenset(
    {
        DistributedMessageType.USER_MESSAGE.value,
        DistributedMessageType.PRESENCE_UPDATE.value,
        DistributedMessageType.PRESENCE_REMOVED.value,
        DistributedMessageType.ASSIGNS_UPDATE.value,
        DistributedMessageType.EVICT_USER.value,
        DistributedMessageType.USER_REMOVE.value,
    }
)

LeaveHandler: TypeAlias = Callable[[LeaveContext], Awaitable[None]]
LeaveMiddlewareFn: TypeAlias = Callable[
    [LeaveContext, Callable[[], Awaitable[None]]], Awaitable[None]
]
OnDestroy: TypeAlias = Callable[[], Awaitable[None]]


@dataclass(slots=True)
class _DispatchItem:
    event: Event
    recipient_ids: list[str]


@dataclass(slots=True)
class ChannelOptions:
    name: str
    options: Options = field(default_factory=Options)
    message_middleware: Middleware[MessageEvent, Channel] | None = None
    outgoing_middleware: Middleware[OutgoingContext, None] | None = None
    leave_handler: LeaveHandler | None = None
    leave_middlewares: list[LeaveMiddlewareFn] = field(default_factory=list)
    on_destroy: OnDestroy | None = None
    endpoint_path: str = ""
    pubsub: PubSub | None = None
    heartbeat: HeartbeatCoordinator | None = None


class Channel:
    __slots__ = (
        "_active",
        "_assigns",
        "_close_task",
        "_connections",
        "_dispatch_queue",
        "_dispatch_semaphore",
        "_dispatch_task",
        "_endpoint_path",
        "_heartbeat",
        "_leave_handler",
        "_leave_middlewares",
        "_lifecycle_lock",
        "_message_middleware",
        "_node_users",
        "_on_destroy",
        "_options",
        "_outgoing_middleware",
        "_presence",
        "_pubsub",
        "_user_get_waiters",
        "name",
        "node_id",
    )

    def __init__(self, opts: ChannelOptions) -> None:
        self.name = opts.name
        self.node_id = opts.options.node_id or ""
        self._options = opts.options
        self._endpoint_path = opts.endpoint_path
        self._pubsub = opts.pubsub
        self._heartbeat = opts.heartbeat
        self._message_middleware: Middleware[MessageEvent, Channel] = (
            opts.message_middleware or Middleware()
        )
        self._outgoing_middleware: Middleware[OutgoingContext, None] = (
            opts.outgoing_middleware or Middleware()
        )
        self._leave_handler = opts.leave_handler
        self._leave_middlewares = list(opts.leave_middlewares)
        self._on_destroy = opts.on_destroy
        self._active: bool = False
        self._connections: Store[Transport] = Store()
        self._close_task: asyncio.Task[None] | None = None
        self._assigns: Store[PondAssigns] = Store()
        self._presence = PresenceClient(self)
        self._dispatch_queue: asyncio.Queue[_DispatchItem] = asyncio.Queue(
            self._options.internal_queue_buffer
        )
        self._dispatch_semaphore = asyncio.Semaphore(self._options.dispatch_concurrency)
        self._dispatch_task: asyncio.Task[None] | None = None
        self._lifecycle_lock = asyncio.Lock()
        self._node_users: dict[str, set[str]] = {}
        self._user_get_waiters: dict[str, asyncio.Future[User | None]] = {}

    async def start(self) -> None:
        close_task = self._close_task
        if close_task is not None:
            await asyncio.shield(close_task)
        async with self._lifecycle_lock:
            if self._active:
                return
            self._close_task = None
            self._active = True
            self._dispatch_task = asyncio.create_task(self._dispatch_loop())
        await self._subscribe_to_pubsub()
        await self._start_liveness()
        await self._request_channel_state()
        self._fire_metric_channel_created()

    async def close(self) -> None:
        async with self._lifecycle_lock:
            close_task = self._close_task
            if close_task is None:
                if not self._active:
                    return
                self._active = False
                close_task = asyncio.create_task(
                    self._finish_close(),
                    name=f"pondsocket-channel-close:{self.name}",
                )
                self._close_task = close_task

        if close_task is asyncio.current_task():
            return
        if self._dispatch_task is asyncio.current_task():
            return
        await asyncio.shield(close_task)

    async def _finish_close(self) -> None:
        dispatch_task = self._dispatch_task
        self._dispatch_task = None
        if dispatch_task is not None and not dispatch_task.done():
            dispatch_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await dispatch_task
        await self._unsubscribe_from_pubsub()
        self._stop_liveness()
        self._node_users.clear()
        self._resolve_pending_user_gets()
        self._fire_metric_channel_destroyed()
        await self._fire_on_destroy()

    async def _start_liveness(self) -> None:
        if self._heartbeat is None or self._pubsub is None or not self._endpoint_path:
            return
        self._heartbeat.register(self)
        await self._heartbeat.start()

    def _stop_liveness(self) -> None:
        if self._heartbeat is not None:
            self._heartbeat.unregister(self)

    def _resolve_pending_user_gets(self) -> None:
        waiters = list(self._user_get_waiters.values())
        self._user_get_waiters.clear()
        for fut in waiters:
            if not fut.done():
                fut.set_result(None)

    async def _fire_on_destroy(self) -> None:
        handler = self._on_destroy
        if handler is None:
            return
        self._on_destroy = None
        try:
            await handler()
        except Exception:
            return

    @property
    def is_active(self) -> bool:
        return self._active

    async def user_count(self) -> int:
        return await self._connections.len()

    async def has_user(self, user_id: str) -> bool:
        return await self._connections.has(user_id)

    async def add_user(self, transport: Transport) -> None:
        if not self._active:
            raise internal(self.name, "Channel is not active")
        user_id = transport.get_id()
        assigns = dict(transport.clone_assigns())
        await self._connections.create(user_id, transport)
        await self._assigns.upsert(user_id, assigns)
        self._fire_metric_channel_joined(user_id)
        await self._publish_distributed(
            DistributedMessageType.USER_JOINED.value,
            {"userId": user_id, "presence": {}, "assigns": assigns},
        )

    async def remove_user(
        self,
        user_id: str,
        reason: str = LeaveReason.EXPLICIT_LEAVE.value,
        *,
        skip_distributed: bool = False,
    ) -> bool:
        if not await self._connections.discard(user_id):
            await self._publish_user_command("user:remove", user_id, reason)
            return False
        assigns = await self._assigns.get(user_id, {}) or {}
        presence: PondPresence | None = None
        if await self._presence.has(user_id):
            presence = await self._presence.get(user_id)
        user = User(id=user_id, assigns=dict(assigns), presence=presence)

        await self._assigns.discard(user_id)
        await self._presence.untrack(user_id)
        self._fire_metric_channel_left(user_id)

        if not skip_distributed:
            await self._publish_distributed(
                DistributedMessageType.USER_LEFT.value,
                {"userId": user_id},
            )

        await self._fire_leave_handler(user, reason)

        if await self._connections.len() == 0:
            await self.close()
        return True

    async def _fire_leave_handler(self, user: User, reason: str) -> None:
        if self._leave_handler is None:
            return
        ctx = LeaveContext(channel=self, user=user, reason=reason)
        handler = self._leave_handler
        try:
            await execute_with_middleware(ctx, handler, self._leave_middlewares)
        except Exception:
            return

    async def get_user(self, user_id: str) -> User:
        if not await self._connections.has(user_id):
            remote = await self._request_remote_user(user_id)
            if remote is not None:
                return remote
            raise not_found(self.name, f"User {user_id} not in channel")
        assigns = await self._assigns.get(user_id, {}) or {}
        presence: PondPresence | None = None
        if await self._presence.has(user_id):
            presence = await self._presence.get(user_id)
        return User(id=user_id, assigns=dict(assigns), presence=presence)

    async def get_user_assign(self, user_id: str, key: str) -> Any:
        assigns = await self._assigns.get(user_id, {}) or {}
        return assigns.get(key)

    async def update_assign(self, user_id: str, key: str, value: Any) -> None:
        if not await self._assigns.has(user_id):
            raise not_found(self.name, f"User {user_id} not in channel")
        current = await self._assigns.get(user_id, {}) or {}
        current = dict(current)
        current[key] = value
        await self._assigns.update(user_id, current)
        ev = Event(
            action=InternalActions.ASSIGNS.value,
            channel_name=self.name,
            request_id=uuid(),
            event="assigns:update",
            payload={"userId": user_id, "assigns": dict(current)},
            node_id=self.node_id,
        )
        await self._publish_event_to_pubsub(ev, "assigns:update")

    async def get_assigns(self) -> dict[str, PondAssigns]:
        snap = await self._assigns.snapshot()
        return {uid: dict(a) for uid, a in snap.items()}

    async def get_presence(self) -> dict[str, PondPresence]:
        return await self._presence.get_all()

    async def track_presence(self, user_id: str, data: PondPresence) -> None:
        await self._presence.track(user_id, data)

    async def update_presence(self, user_id: str, data: PondPresence) -> None:
        await self._presence.update(user_id, data)

    async def untrack_presence(self, user_id: str) -> None:
        await self._presence.untrack(user_id)

    async def evict_user(self, user_id: str, reason: str) -> None:
        if not await self._connections.has(user_id):
            await self._publish_user_command("user:evict", user_id, reason)
            return
        ev = Event(
            action=ServerActions.SYSTEM.value,
            channel_name=self.name,
            request_id=uuid(),
            event="EVICTED",
            payload={"reason": reason},
            node_id=self.node_id,
        )
        await self._send_direct([user_id], ev)
        await self.remove_user(user_id, reason=LeaveReason.EVICTED.value, skip_distributed=True)

    async def broadcast(self, event_name: str, payload: PondMessage) -> None:
        await self._send_event(
            recipients=AllUsers(),
            action=ServerActions.BROADCAST.value,
            event_name=event_name,
            payload=payload,
        )

    async def broadcast_to(
        self,
        event_name: str,
        payload: PondMessage,
        *user_ids: str,
    ) -> None:
        await self._send_event(
            recipients=ToUsers(tuple(user_ids)),
            action=ServerActions.BROADCAST.value,
            event_name=event_name,
            payload=payload,
        )

    async def broadcast_from(
        self,
        event_name: str,
        payload: PondMessage,
        exclude_user_id: str,
    ) -> None:
        await self._send_event(
            recipients=AllExceptSender(exclude_user_id),
            action=ServerActions.BROADCAST.value,
            event_name=event_name,
            payload=payload,
        )

    async def send_system(
        self,
        event_name: str,
        payload: PondMessage,
        *,
        request_id: str,
        user_ids: list[str],
    ) -> None:
        ev = Event(
            action=ServerActions.SYSTEM.value,
            channel_name=self.name,
            request_id=request_id,
            event=event_name,
            payload=payload,
            node_id=self.node_id,
        )
        await self._send_direct(user_ids, ev)

    async def send_acknowledge(self, request_id: str, user_id: str) -> None:
        await self.send_system(
            SystemEvents.ACKNOWLEDGE.value,
            {},
            request_id=request_id,
            user_ids=[user_id],
        )

    async def send_unauthorized(
        self,
        request_id: str,
        user_id: str,
        *,
        code: int,
        message: str,
    ) -> None:
        ev = Event(
            action=ServerActions.SYSTEM.value,
            channel_name=self.name,
            request_id=request_id,
            event=SystemEvents.UNAUTHORIZED.value,
            payload={"code": code, "message": message},
            node_id=self.node_id,
        )
        await self._send_direct([user_id], ev)

    async def _send_direct(self, user_ids: list[str], event: Event) -> None:
        for user_id in user_ids:
            transport = await self._connections.get(user_id)
            if transport is None or not transport.is_active():
                continue
            try:
                await transport.send_event(event)
            except Exception:
                continue

    async def handle_incoming_event(self, event: Event, sender_id: str) -> None:
        if not await self._connections.has(sender_id):
            return
        user = await self.get_user(sender_id)
        msg = MessageEvent(event=event, user=user)

        async def final(_req: MessageEvent, _resp: Channel) -> None:
            return

        before_err = await self._fire_before_message(event)
        if before_err is not None:
            await self._fire_after_message(event, before_err)
            return

        caught: BaseException | None = None
        try:
            await self._message_middleware.handle(msg, self, final)
        except Exception as exc:
            caught = exc
        await self._fire_after_message(event, caught)

    async def publish_presence_change(
        self,
        *,
        event_type: PresenceEventTypes,
        changed_user_id: str,
        changed: PondPresence,
        presence_snapshot: list[PondPresence],
    ) -> None:
        ev = Event(
            action=ServerActions.PRESENCE.value,
            channel_name=self.name,
            request_id=uuid(),
            event=event_type.value,
            payload={"presence": presence_snapshot, "changed": changed},
            node_id=self.node_id,
            user_id=changed_user_id,
        )
        recipients = await self._presence.keys()
        if recipients:
            await self._enqueue_dispatch(ev, recipients)
        await self._publish_event_to_pubsub(ev, f"presence:{event_type.value.lower()}")

    async def _resolve_recipients(self, spec: RecipientSpec) -> list[str]:
        match spec:
            case AllUsers():
                return await self._connections.keys()
            case AllExceptSender(sender_id=sender):
                return [uid for uid in await self._connections.keys() if uid != sender]
            case ToUsers(user_ids=ids):
                return list(ids)

    async def _send_event(
        self,
        *,
        recipients: RecipientSpec,
        action: str,
        event_name: str,
        payload: PondMessage,
        request_id: str | None = None,
    ) -> None:
        ev = Event(
            action=action,
            channel_name=self.name,
            request_id=request_id or uuid(),
            event=event_name,
            payload=payload,
            node_id=self.node_id,
        )
        match recipients:
            case AllUsers():
                ev.recipient_descriptor = "ALL_USERS"
            case AllExceptSender(sender_id=sender):
                ev.recipient_descriptor = "ALL_EXCEPT_SENDER"
                ev.from_user_id = sender
            case ToUsers(user_ids=ids):
                ev.recipient_descriptor = list(ids)
                ev.recipients = list(ids)
        resolved = await self._resolve_recipients(recipients)
        if resolved:
            await self._enqueue_dispatch(ev, resolved)
        if action == ServerActions.BROADCAST.value:
            await self._publish_event_to_pubsub(ev, "message")

    async def _enqueue_dispatch(self, event: Event, recipient_ids: list[str]) -> None:
        if not self._active:
            return
        item = _DispatchItem(event=event, recipient_ids=recipient_ids)
        timeout = self._options.internal_queue_timeout
        try:
            await asyncio.wait_for(self._dispatch_queue.put(item), timeout=timeout)
        except TimeoutError:
            return

    async def _dispatch_loop(self) -> None:
        while True:
            try:
                item = await self._dispatch_queue.get()
            except asyncio.CancelledError:
                return
            if not self._active:
                return
            await self._dispatch_item(item)

    async def _dispatch_item(self, item: _DispatchItem) -> None:
        async def bounded_deliver(rid: str) -> None:
            async with self._dispatch_semaphore:
                try:
                    await self._deliver_one(item.event, rid)
                except Exception:
                    return

        await asyncio.gather(
            *(bounded_deliver(rid) for rid in item.recipient_ids),
            return_exceptions=False,
        )

    async def _deliver_one(self, event: Event, recipient_id: str) -> None:
        transport = await self._connections.get(recipient_id)
        if transport is None or not transport.is_active():
            return
        try:
            user = await self.get_user(recipient_id)
        except Exception:
            return
        delivery_event = Event(
            action=event.action,
            channel_name=event.channel_name,
            request_id=event.request_id,
            event=event.event,
            payload=event.payload,
            node_id=event.node_id,
        )
        ctx = OutgoingContext(
            channel=self,
            event=delivery_event,
            user=user,
            transport=transport,
        )

        async def noop_final(_req: OutgoingContext, _resp: None) -> None:
            return

        await self._outgoing_middleware.handle(ctx, None, noop_final)

        if ctx.is_blocked():
            return
        try:
            await transport.send_event(ctx.event)
        except Exception:
            return

    def _clean_endpoint(self) -> str:
        ep = self._endpoint_path
        return ep[1:] if ep.startswith("/") else ep

    def _channel_topic(self) -> str:
        return format_topic(
            self._clean_endpoint(),
            self.name,
            "",
            namespace=self._options.namespace,
            prefix=self._options.key_prefix,
        )

    def _pubsub_pattern(self) -> str:
        return self._channel_topic()

    async def _subscribe_to_pubsub(self) -> None:
        if self._pubsub is None or not self._endpoint_path:
            return
        try:
            await self._pubsub.subscribe(self._pubsub_pattern(), self._handle_pubsub_message)
        except Exception:
            return

    async def _unsubscribe_from_pubsub(self) -> None:
        if self._pubsub is None or not self._endpoint_path:
            return
        try:
            await self._pubsub.unsubscribe(self._pubsub_pattern())
        except Exception:
            return

    async def _publish_event_to_pubsub(self, event: Event, event_subtype: str) -> None:
        if self._pubsub is None or not self._endpoint_path:
            return
        topic = self._channel_topic()
        try:
            await self._pubsub.publish(
                topic,
                _event_to_distributed_bytes(event, event_subtype, self._clean_endpoint()),
            )
        except Exception:
            return

    async def _publish_distributed(self, msg_type: str, extra: dict[str, Any]) -> None:
        if self._pubsub is None or not self._endpoint_path:
            return
        topic = self._channel_topic()
        data = distributed_bytes(msg_type, self.node_id, self._clean_endpoint(), self.name, extra)
        try:
            await self._pubsub.publish(topic, data)
        except Exception:
            return

    async def _handle_pubsub_message(self, _topic: str, data: bytes) -> None:
        raw = parse_envelope(data)
        if raw is None:
            return
        node_id = raw.get("sourceNodeId")
        if not isinstance(node_id, str) or node_id == self.node_id:
            return
        if raw.get("channelName") != self.name:
            return
        if not self._active:
            return
        msg_type = raw.get("type")
        if msg_type in _LEGACY_DISTRIBUTED_TYPES:
            event = _event_from_envelope(raw)
            if event is not None:
                await self._dispatch_event(event)
            return
        if msg_type == DistributedMessageType.STATE_REQUEST.value:
            await self._handle_state_request(raw)
        elif msg_type == DistributedMessageType.STATE_RESPONSE.value:
            await self._handle_state_response(raw)
        elif msg_type == DistributedMessageType.USER_JOINED.value:
            await self._handle_remote_user_joined(raw)
        elif msg_type == DistributedMessageType.USER_LEFT.value:
            await self._handle_remote_user_left(raw)
        elif msg_type == DistributedMessageType.USER_GET_REQUEST.value:
            await self._handle_user_get_request(raw)
        elif msg_type == DistributedMessageType.USER_GET_RESPONSE.value:
            await self._handle_user_get_response(raw)

    async def _dispatch_event(self, event: Event) -> None:
        if event.action == ServerActions.BROADCAST.value:
            recipients = await self._resolve_distributed_recipients(event)
            if recipients:
                await self._enqueue_dispatch(event, recipients)
            return
        if event.action == ServerActions.PRESENCE.value:
            await self._merge_remote_presence(event)
            recipients = await self._presence.keys()
            if recipients:
                await self._enqueue_dispatch(event, recipients)
            return
        if event.action == InternalActions.ASSIGNS.value:
            await self._handle_remote_assigns(event)
            return
        if event.action == InternalActions.USER_COMMAND.value:
            await self._handle_remote_user_command(event)
            return

    async def _resolve_distributed_recipients(self, event: Event) -> list[str]:
        local = await self._connections.keys()
        descriptor = event.recipient_descriptor
        if isinstance(descriptor, list):
            wanted = {str(item) for item in descriptor}
            return [uid for uid in local if uid in wanted]
        if descriptor == "ALL_EXCEPT_SENDER":
            return [uid for uid in local if uid != event.from_user_id]
        return local

    async def _merge_remote_presence(self, event: Event) -> None:
        if not isinstance(event.payload, dict):
            return
        changed = event.payload.get("changed")
        user_id = ""
        if isinstance(changed, dict):
            raw_id = event.user_id or changed.get("userId") or changed.get("id")
            user_id = str(raw_id) if raw_id else ""
        if not user_id:
            return
        if event.event == PresenceEventTypes.LEAVE.value:
            await self._presence.discard(user_id)
            if isinstance(event.payload, dict):
                event.payload["presence"] = await self._presence.values()
            return
        if isinstance(changed, dict):
            presence = {k: v for k, v in changed.items() if k not in {"userId", "id"}}
            if not presence:
                presence = dict(changed)
            existed = await self._presence.has(user_id)
            await self._presence.set_remote(user_id, presence)
            event.event = (
                PresenceEventTypes.UPDATE.value if existed else PresenceEventTypes.JOIN.value
            )
            if isinstance(event.payload, dict):
                event.payload["presence"] = await self._presence.values()

    async def _handle_remote_assigns(self, event: Event) -> None:
        if not isinstance(event.payload, dict):
            return
        user_id = str(event.payload.get("userId") or "")
        key = event.payload.get("key")
        if not user_id or not isinstance(key, str):
            remote_assigns = event.payload.get("assigns")
            if user_id and isinstance(remote_assigns, dict) and await self._assigns.has(user_id):
                await self._assigns.update(user_id, dict(remote_assigns))
            return
        if not await self._assigns.has(user_id):
            return
        current = await self._assigns.get(user_id, {}) or {}
        updated = dict(current)
        updated[key] = event.payload.get("value")
        await self._assigns.update(user_id, updated)

    async def _handle_remote_user_command(self, event: Event) -> None:
        if not isinstance(event.payload, dict):
            return
        user_id = str(event.payload.get("userId") or "")
        reason = str(event.payload.get("reason") or "remote command")
        if not user_id or not await self._connections.has(user_id):
            return
        if event.event == "user:evict":
            ev = Event(
                action=ServerActions.SYSTEM.value,
                channel_name=self.name,
                request_id=uuid(),
                event="EVICTED",
                payload={"reason": reason},
                node_id=self.node_id,
            )
            await self._send_direct([user_id], ev)
        await self.remove_user(user_id, reason=reason)

    async def _publish_user_command(self, command: str, user_id: str, reason: str) -> None:
        ev = Event(
            action=InternalActions.USER_COMMAND.value,
            channel_name=self.name,
            request_id=uuid(),
            event=command,
            payload={"userId": user_id, "reason": reason},
            node_id=self.node_id,
        )
        await self._publish_event_to_pubsub(ev, command)

    async def _request_channel_state(self) -> None:
        if self._pubsub is None or not self._endpoint_path:
            return
        await self._publish_distributed(
            DistributedMessageType.STATE_REQUEST.value,
            {"fromNode": self.node_id},
        )

    async def _handle_state_request(self, _raw: dict[str, Any]) -> None:
        local_ids = await self._connections.keys()
        if not local_ids:
            return
        users: list[dict[str, Any]] = []
        for uid in local_ids:
            assigns = await self._assigns.get(uid, {}) or {}
            presence: PondPresence = {}
            if await self._presence.has(uid):
                presence = await self._presence.get(uid)
            users.append({"id": uid, "assigns": dict(assigns), "presence": presence})
        await self._publish_distributed(
            DistributedMessageType.STATE_RESPONSE.value,
            {"users": users},
        )

    async def _handle_state_response(self, raw: dict[str, Any]) -> None:
        users = raw.get("users")
        if not isinstance(users, list):
            return
        node_id = str(raw.get("sourceNodeId") or "")
        for user in users:
            if not isinstance(user, dict):
                continue
            user_id = str(user.get("id") or "")
            if not user_id or await self._connections.has(user_id):
                continue
            assigns = user.get("assigns")
            await self._assigns.upsert(user_id, dict(assigns) if isinstance(assigns, dict) else {})
            self._track_node_user(node_id, user_id)
            presence = user.get("presence")
            if isinstance(presence, dict) and presence:
                await self._presence.set_remote(user_id, dict(presence))

    async def _handle_remote_user_joined(self, raw: dict[str, Any]) -> None:
        user_id = str(raw.get("userId") or "")
        if not user_id or await self._connections.has(user_id):
            return
        node_id = str(raw.get("sourceNodeId") or "")
        assigns = raw.get("assigns")
        await self._assigns.upsert(user_id, dict(assigns) if isinstance(assigns, dict) else {})
        self._track_node_user(node_id, user_id)
        presence = raw.get("presence")
        if isinstance(presence, dict) and presence:
            await self._presence.set_remote(user_id, dict(presence))

    async def _handle_remote_user_left(self, raw: dict[str, Any]) -> None:
        user_id = str(raw.get("userId") or "")
        if not user_id or await self._connections.has(user_id):
            return
        node_id = str(raw.get("sourceNodeId") or "")
        await self._assigns.discard(user_id)
        await self._presence.discard(user_id)
        self._untrack_node_user(node_id, user_id)

    async def _handle_user_get_request(self, raw: dict[str, Any]) -> None:
        user_id = str(raw.get("userId") or "")
        request_id = str(raw.get("requestId") or "")
        if not user_id or not request_id:
            return
        if not await self._connections.has(user_id):
            return
        assigns = await self._assigns.get(user_id, {}) or {}
        presence: PondPresence = {}
        if await self._presence.has(user_id):
            presence = await self._presence.get(user_id)
        await self._publish_distributed(
            DistributedMessageType.USER_GET_RESPONSE.value,
            {
                "userId": user_id,
                "requestId": request_id,
                "assigns": dict(assigns),
                "presence": presence,
            },
        )

    async def _handle_user_get_response(self, raw: dict[str, Any]) -> None:
        request_id = str(raw.get("requestId") or "")
        fut = self._user_get_waiters.get(request_id)
        if fut is None or fut.done():
            return
        assigns = raw.get("assigns")
        presence = raw.get("presence")
        user = User(
            id=str(raw.get("userId") or ""),
            assigns=dict(assigns) if isinstance(assigns, dict) else {},
            presence=dict(presence) if isinstance(presence, dict) else None,
        )
        fut.set_result(user)

    async def _request_remote_user(self, user_id: str) -> User | None:
        if self._pubsub is None or not self._endpoint_path:
            return None
        request_id = uuid()
        loop = asyncio.get_event_loop()
        fut: asyncio.Future[User | None] = loop.create_future()
        self._user_get_waiters[request_id] = fut
        try:
            await self._publish_distributed(
                DistributedMessageType.USER_GET_REQUEST.value,
                {
                    "userId": user_id,
                    "requestId": request_id,
                    "fromNode": self.node_id,
                },
            )
            return await asyncio.wait_for(fut, timeout=self._options.user_get_timeout)
        except (TimeoutError, asyncio.CancelledError):
            return None
        finally:
            self._user_get_waiters.pop(request_id, None)

    def _track_node_user(self, node_id: str, user_id: str) -> None:
        if not node_id:
            return
        self._node_users.setdefault(node_id, set()).add(user_id)
        if self._heartbeat is not None:
            self._heartbeat.note_node(node_id)

    def _untrack_node_user(self, node_id: str, user_id: str) -> None:
        if not node_id:
            return
        users = self._node_users.get(node_id)
        if users is None:
            return
        users.discard(user_id)
        if not users:
            self._node_users.pop(node_id, None)

    async def evict_node_users(self, node_id: str) -> None:
        users = self._node_users.pop(node_id, None)
        if not users:
            return
        for user_id in users:
            if await self._connections.has(user_id):
                continue
            await self._assigns.discard(user_id)
            await self._presence.discard(user_id)

    def _fire_metric_channel_created(self) -> None:
        hooks = self._options.hooks
        if hooks is None or hooks.metrics is None:
            return
        try:
            hooks.metrics.channel_created(self.name)
        except Exception:
            return

    def _fire_metric_channel_destroyed(self) -> None:
        hooks = self._options.hooks
        if hooks is None or hooks.metrics is None:
            return
        try:
            hooks.metrics.channel_destroyed(self.name)
        except Exception:
            return

    def _fire_metric_channel_joined(self, user_id: str) -> None:
        hooks = self._options.hooks
        if hooks is None or hooks.metrics is None:
            return
        try:
            hooks.metrics.channel_joined(user_id, self.name)
        except Exception:
            return

    def _fire_metric_channel_left(self, user_id: str) -> None:
        hooks = self._options.hooks
        if hooks is None or hooks.metrics is None:
            return
        try:
            hooks.metrics.channel_left(user_id, self.name)
        except Exception:
            return

    async def _fire_before_message(self, event: Event) -> BaseException | None:
        hooks = self._options.hooks
        if hooks is None or hooks.before_message is None:
            return None
        try:
            await hooks.before_message(event)
        except Exception as exc:
            return exc
        return None

    async def _fire_after_message(self, event: Event, err: BaseException | None) -> None:
        hooks = self._options.hooks
        if hooks is None or hooks.after_message is None:
            return
        try:
            await hooks.after_message(event, err)
        except Exception:
            return


def _now_ms() -> int:
    import time

    return int(time.time() * 1000)


def _event_to_distributed_bytes(event: Event, subtype: str, endpoint: str) -> bytes:
    import json

    if event.action == ServerActions.BROADCAST.value:
        message_type = "USER_MESSAGE"
    elif event.action == ServerActions.PRESENCE.value and (
        subtype.endswith("remove") or event.event == "LEAVE"
    ):
        message_type = "PRESENCE_REMOVED"
    elif event.action == ServerActions.PRESENCE.value:
        message_type = "PRESENCE_UPDATE"
    elif event.action == InternalActions.ASSIGNS.value:
        message_type = "ASSIGNS_UPDATE"
    elif event.action == InternalActions.USER_COMMAND.value and event.event == "user:remove":
        message_type = "USER_REMOVE"
    elif event.action == InternalActions.USER_COMMAND.value:
        message_type = "EVICT_USER"
    else:
        message_type = "USER_MESSAGE"

    envelope: dict[str, object] = {
        "protocol": "pondsocket.distributed",
        "version": 1,
        "type": message_type,
        "messageId": uuid(),
        "sourceNodeId": event.node_id,
        "endpointName": endpoint,
        "channelName": event.channel_name,
        "timestamp": _now_ms(),
    }
    if message_type == "USER_MESSAGE":
        envelope.update(
            {
                "fromUserId": event.from_user_id,
                "event": event.event,
                "payload": event.payload if event.payload is not None else {},
                "requestId": event.request_id,
                "recipientDescriptor": event.recipient_descriptor,
            }
        )
    elif message_type == "PRESENCE_UPDATE":
        payload = event.payload if isinstance(event.payload, dict) else {}
        changed = payload.get("changed", {})
        if event.user_id:
            envelope["userId"] = event.user_id
        elif isinstance(changed, dict):
            envelope["userId"] = str(changed.get("userId") or changed.get("id") or "")
        else:
            envelope["userId"] = ""
        envelope["presence"] = changed if isinstance(changed, dict) else {}
    elif message_type == "PRESENCE_REMOVED":
        payload = event.payload if isinstance(event.payload, dict) else {}
        changed = payload.get("changed", {})
        if event.user_id:
            envelope["userId"] = event.user_id
        elif isinstance(changed, dict):
            envelope["userId"] = str(changed.get("userId") or changed.get("id") or "")
        else:
            envelope["userId"] = ""
    elif message_type == "ASSIGNS_UPDATE":
        payload = event.payload if isinstance(event.payload, dict) else {}
        envelope["userId"] = payload.get("userId", "")
        envelope["assigns"] = payload.get("assigns", {})
    elif message_type == "EVICT_USER":
        payload = event.payload if isinstance(event.payload, dict) else {}
        envelope["userId"] = payload.get("userId", "")
        envelope["reason"] = payload.get("reason", "")
    elif message_type == "USER_REMOVE":
        payload = event.payload if isinstance(event.payload, dict) else {}
        envelope["userId"] = payload.get("userId", "")
    return json.dumps(envelope, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def _distributed_bytes_to_event(data: bytes) -> Event | None:
    raw = parse_envelope(data)
    if raw is None:
        return None
    return _event_from_envelope(raw)


def _event_from_envelope(raw: dict[str, Any]) -> Event | None:
    message_type = raw.get("type")
    channel_name = raw.get("channelName")
    node_id = raw.get("sourceNodeId")
    if (
        not isinstance(message_type, str)
        or not isinstance(channel_name, str)
        or not isinstance(node_id, str)
    ):
        return None
    request_id = str(raw.get("requestId") or uuid())
    if message_type == "USER_MESSAGE":
        descriptor = raw.get("recipientDescriptor") or "ALL_USERS"
        if isinstance(descriptor, list):
            descriptor = [str(item) for item in descriptor]
        return Event(
            action=ServerActions.BROADCAST.value,
            channel_name=channel_name,
            request_id=request_id,
            event=str(raw.get("event") or ""),
            payload=raw.get("payload") if raw.get("payload") is not None else {},
            node_id=node_id,
            recipients=descriptor if isinstance(descriptor, list) else [],
            recipient_descriptor=descriptor,
            from_user_id=str(raw.get("fromUserId") or "CHANNEL"),
        )
    if message_type == "PRESENCE_UPDATE":
        payload = raw.get("payload") if isinstance(raw.get("payload"), dict) else None
        return Event(
            action=ServerActions.PRESENCE.value,
            channel_name=channel_name,
            request_id=request_id,
            event=str(raw.get("event") or PresenceEventTypes.UPDATE.value),
            payload=payload or {"presence": [], "changed": raw.get("presence") or {}},
            node_id=node_id,
            user_id=str(raw.get("userId") or ""),
        )
    if message_type == "PRESENCE_REMOVED":
        payload = raw.get("payload") if isinstance(raw.get("payload"), dict) else None
        return Event(
            action=ServerActions.PRESENCE.value,
            channel_name=channel_name,
            request_id=request_id,
            event=PresenceEventTypes.LEAVE.value,
            payload=payload or {"presence": [], "changed": {"userId": raw.get("userId") or ""}},
            node_id=node_id,
            user_id=str(raw.get("userId") or ""),
        )
    if message_type == "ASSIGNS_UPDATE":
        payload = raw.get("payload") if isinstance(raw.get("payload"), dict) else None
        assigns_payload = {
            "userId": raw.get("userId") or "",
            "assigns": raw.get("assigns") or {},
        }
        if "key" in raw:
            assigns_payload["key"] = raw.get("key")
        if "value" in raw:
            assigns_payload["value"] = raw.get("value")
        return Event(
            action=InternalActions.ASSIGNS.value,
            channel_name=channel_name,
            request_id=request_id,
            event="assigns:update",
            payload=payload or assigns_payload,
            node_id=node_id,
        )
    if message_type in {"EVICT_USER", "USER_REMOVE"}:
        return Event(
            action=InternalActions.USER_COMMAND.value,
            channel_name=channel_name,
            request_id=request_id,
            event="user:remove" if message_type == "USER_REMOVE" else "user:evict",
            payload={"userId": raw.get("userId") or "", "reason": raw.get("reason") or ""},
            node_id=node_id,
        )
    return None
