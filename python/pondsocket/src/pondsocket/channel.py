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
from .errors import internal, not_found
from .middleware import Middleware, execute_with_middleware
from .presence import PresenceClient
from .pubsub import PubSub, format_topic
from .store import Store
from .transport import Transport
from .types import (
    AllExceptSender,
    AllUsers,
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


class Channel:
    __slots__ = (
        "_active",
        "_assigns",
        "_connections",
        "_dispatch_queue",
        "_dispatch_semaphore",
        "_dispatch_task",
        "_endpoint_path",
        "_leave_handler",
        "_leave_middlewares",
        "_lifecycle_lock",
        "_message_middleware",
        "_on_destroy",
        "_options",
        "_outgoing_middleware",
        "_presence",
        "_pubsub",
        "name",
        "node_id",
    )

    def __init__(self, opts: ChannelOptions) -> None:
        self.name = opts.name
        self.node_id = opts.options.node_id or ""
        self._options = opts.options
        self._endpoint_path = opts.endpoint_path
        self._pubsub = opts.pubsub
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
        self._assigns: Store[PondAssigns] = Store()
        self._presence = PresenceClient(self)
        self._dispatch_queue: asyncio.Queue[_DispatchItem] = asyncio.Queue(
            self._options.internal_queue_buffer
        )
        self._dispatch_semaphore = asyncio.Semaphore(
            self._options.dispatch_concurrency
        )
        self._dispatch_task: asyncio.Task[None] | None = None
        self._lifecycle_lock = asyncio.Lock()

    async def start(self) -> None:
        async with self._lifecycle_lock:
            if self._active:
                return
            self._active = True
            self._dispatch_task = asyncio.create_task(self._dispatch_loop())
        await self._subscribe_to_pubsub()
        self._fire_metric_channel_created()

    async def close(self) -> None:
        async with self._lifecycle_lock:
            if not self._active:
                return
            self._active = False
            if self._dispatch_task is not None and not self._dispatch_task.done():
                self._dispatch_task.cancel()
                with contextlib.suppress(asyncio.CancelledError):
                    await self._dispatch_task
            self._dispatch_task = None
        await self._unsubscribe_from_pubsub()
        self._fire_metric_channel_destroyed()
        await self._fire_on_destroy()

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
        await self._connections.create(user_id, transport)
        await self._assigns.upsert(user_id, dict(transport.clone_assigns()))
        self._fire_metric_channel_joined(user_id)

    async def remove_user(
        self, user_id: str, reason: str = LeaveReason.EXPLICIT_LEAVE.value
    ) -> None:
        if not await self._connections.discard(user_id):
            await self._publish_user_command("user:remove", user_id, reason)
            return
        assigns = await self._assigns.get(user_id, {}) or {}
        presence: PondPresence | None = None
        if await self._presence.has(user_id):
            presence = await self._presence.get(user_id)
        user = User(id=user_id, assigns=dict(assigns), presence=presence)

        await self._assigns.discard(user_id)
        await self._presence.untrack(user_id)
        self._fire_metric_channel_left(user_id)

        await self._fire_leave_handler(user, reason)

        if await self._connections.len() == 0:
            await self._fire_on_destroy()

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
            payload={"userId": user_id, "key": key, "value": value, "assigns": dict(current)},
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
        await self.remove_user(user_id, reason=LeaveReason.EVICTED.value)

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

    def _pubsub_pattern(self) -> str:
        return format_topic(self._clean_endpoint(), self.name, ".*")

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
        topic = format_topic(self._clean_endpoint(), self.name, event_subtype)
        try:
            await self._pubsub.publish(
                topic,
                _event_to_distributed_bytes(event, event_subtype, self._clean_endpoint()),
            )
        except Exception:
            return

    async def _handle_pubsub_message(self, _topic: str, data: bytes) -> None:
        event = _distributed_bytes_to_event(data)
        if event is None:
            return
        if event.node_id and event.node_id == self.node_id:
            return
        if event.channel_name != self.name:
            return
        if not self._active:
            return
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
            return
        if isinstance(changed, dict):
            presence = {k: v for k, v in changed.items() if k not in {"userId", "id"}}
            if not presence:
                presence = dict(changed)
            await self._presence.set_remote(user_id, presence)

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

    async def _fire_after_message(
        self, event: Event, err: BaseException | None
    ) -> None:
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
        envelope["event"] = event.event
        changed = payload.get("changed", {})
        if event.user_id:
            envelope["userId"] = event.user_id
        elif isinstance(changed, dict):
            envelope["userId"] = str(changed.get("userId") or changed.get("id") or "")
        else:
            envelope["userId"] = ""
        envelope["presence"] = payload.get("changed", {})
        envelope["payload"] = payload
    elif message_type == "PRESENCE_REMOVED":
        payload = event.payload if isinstance(event.payload, dict) else {}
        changed = payload.get("changed", {})
        if event.user_id:
            envelope["userId"] = event.user_id
        elif isinstance(changed, dict):
            envelope["userId"] = str(changed.get("userId") or changed.get("id") or "")
        else:
            envelope["userId"] = ""
        envelope["payload"] = payload
    elif message_type == "ASSIGNS_UPDATE":
        payload = event.payload if isinstance(event.payload, dict) else {}
        envelope["userId"] = payload.get("userId", "")
        if "key" in payload:
            envelope["key"] = payload.get("key")
        if "value" in payload:
            envelope["value"] = payload.get("value")
        envelope["assigns"] = payload.get("assigns", {})
        envelope["payload"] = payload
    elif message_type in {"EVICT_USER", "USER_REMOVE"}:
        payload = event.payload if isinstance(event.payload, dict) else {}
        envelope["userId"] = payload.get("userId", "")
        envelope["reason"] = payload.get("reason", "")
    return json.dumps(envelope, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def _distributed_bytes_to_event(data: bytes) -> Event | None:
    import json

    try:
        raw = json.loads(data.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        return None
    if not isinstance(raw, dict):
        return None
    if raw.get("protocol") != "pondsocket.distributed":
        return None
    if raw.get("version") != 1:
        return None
    message_type = raw.get("type")
    channel_name = raw.get("channelName")
    node_id = raw.get("sourceNodeId")
    if not isinstance(message_type, str) or not isinstance(channel_name, str) or not isinstance(node_id, str):
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
