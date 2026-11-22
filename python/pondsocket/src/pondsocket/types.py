from __future__ import annotations

import math
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from enum import StrEnum
from typing import TYPE_CHECKING, Any, TypeAlias

from pondsocket_common import (
    ChannelReceivers,
    PondAssigns,
    PondPresence,
    UserData,
)

if TYPE_CHECKING:
    from .hooks import Hooks

DEFAULT_MAX_MESSAGE_SIZE: int = 1 * 1024 * 1024
DEFAULT_PING_INTERVAL: float = 30.0
DEFAULT_PONG_WAIT: float = 60.0
DEFAULT_WRITE_WAIT: float = 10.0
DEFAULT_SEND_TIMEOUT: float = 10.0
DEFAULT_INTERNAL_QUEUE_TIMEOUT: float = 30.0
DEFAULT_MAX_CONCURRENT_HANDLERS: int = 10
DEFAULT_SEND_CHANNEL_BUFFER: int = 256
DEFAULT_RECEIVE_CHANNEL_BUFFER: int = 256
DEFAULT_INTERNAL_QUEUE_BUFFER: int = 128
DEFAULT_DISPATCH_CONCURRENCY: int = 32


class TransportType(StrEnum):
    WEBSOCKET = "websocket"
    SSE = "sse"


class SystemEvents(StrEnum):
    ACKNOWLEDGE = "ACKNOWLEDGE"
    EXIT_ACKNOWLEDGE = "EXIT_ACKNOWLEDGE"
    CONNECTION = "CONNECTION"
    INTERNAL_ERROR = "INTERNAL_ERROR"
    NOT_FOUND = "NOT_FOUND"
    UNAUTHORIZED = "UNAUTHORIZED"


class SystemEntity(StrEnum):
    GATEWAY = "GATEWAY"
    CHANNEL = "CHANNEL"


class InternalActions(StrEnum):
    ASSIGNS = "ASSIGNS"
    USER_COMMAND = "USER_COMMAND"


class DistributedMessageType(StrEnum):
    STATE_REQUEST = "STATE_REQUEST"
    STATE_RESPONSE = "STATE_RESPONSE"
    USER_JOINED = "USER_JOINED"
    USER_LEFT = "USER_LEFT"
    USER_MESSAGE = "USER_MESSAGE"
    PRESENCE_UPDATE = "PRESENCE_UPDATE"
    PRESENCE_REMOVED = "PRESENCE_REMOVED"
    ASSIGNS_UPDATE = "ASSIGNS_UPDATE"
    EVICT_USER = "EVICT_USER"
    USER_REMOVE = "USER_REMOVE"
    USER_GET_REQUEST = "USER_GET_REQUEST"
    USER_GET_RESPONSE = "USER_GET_RESPONSE"
    NODE_HEARTBEAT = "NODE_HEARTBEAT"


class LeaveReason(StrEnum):
    EXPLICIT_LEAVE = "explicit_leave"
    CONNECTION_CLOSED = "connection_closed"
    EVICTED = "evicted"


@dataclass(slots=True)
class Route:
    params: dict[str, str] = field(default_factory=dict)
    query: dict[str, list[str]] = field(default_factory=dict)
    wildcard: str | None = None

    def param(self, key: str) -> str:
        return self.params.get(key, "")

    def query_param(self, key: str) -> list[str]:
        return self.query.get(key, [])


@dataclass(slots=True)
class Event:
    action: str
    channel_name: str
    request_id: str
    event: str
    payload: Any = None
    node_id: str = ""
    recipients: list[str] = field(default_factory=list)
    recipient_descriptor: object = "ALL_USERS"
    from_user_id: str = "CHANNEL"
    user_id: str = ""

    def __post_init__(self) -> None:
        if not self.action:
            raise ValueError("Event.action must not be empty")
        if not self.channel_name:
            raise ValueError("Event.channel_name must not be empty")
        if not self.request_id:
            raise ValueError("Event.request_id must not be empty")
        if not self.event:
            raise ValueError("Event.event must not be empty")


@dataclass(slots=True)
class Options:
    max_message_size: int = DEFAULT_MAX_MESSAGE_SIZE
    ping_interval: float = DEFAULT_PING_INTERVAL
    pong_wait: float = DEFAULT_PONG_WAIT
    write_wait: float = DEFAULT_WRITE_WAIT
    send_timeout: float = DEFAULT_SEND_TIMEOUT
    internal_queue_timeout: float = DEFAULT_INTERNAL_QUEUE_TIMEOUT
    internal_queue_buffer: int = DEFAULT_INTERNAL_QUEUE_BUFFER
    dispatch_concurrency: int = DEFAULT_DISPATCH_CONCURRENCY
    send_channel_buffer: int = DEFAULT_SEND_CHANNEL_BUFFER
    receive_channel_buffer: int = DEFAULT_RECEIVE_CHANNEL_BUFFER
    max_concurrent_handlers: int = DEFAULT_MAX_CONCURRENT_HANDLERS
    max_connections: int = 0
    node_id: str = ""
    namespace: str = "default"
    key_prefix: str = "pondsocket"
    heartbeat_interval: float = 30.0
    heartbeat_timeout: float = 90.0
    user_get_timeout: float = 0.5
    hooks: Hooks | None = None

    def __post_init__(self) -> None:
        positive_integers: dict[str, int] = {
            "max_message_size": self.max_message_size,
            "internal_queue_buffer": self.internal_queue_buffer,
            "dispatch_concurrency": self.dispatch_concurrency,
            "send_channel_buffer": self.send_channel_buffer,
            "receive_channel_buffer": self.receive_channel_buffer,
            "max_concurrent_handlers": self.max_concurrent_handlers,
        }
        for integer_name, integer_value in positive_integers.items():
            if integer_value <= 0:
                raise ValueError(f"{integer_name} must be greater than zero")
        if self.max_connections < 0:
            raise ValueError("max_connections must be zero or greater")

        positive_floats: dict[str, float] = {
            "ping_interval": self.ping_interval,
            "pong_wait": self.pong_wait,
            "write_wait": self.write_wait,
            "send_timeout": self.send_timeout,
            "internal_queue_timeout": self.internal_queue_timeout,
            "heartbeat_interval": self.heartbeat_interval,
            "heartbeat_timeout": self.heartbeat_timeout,
            "user_get_timeout": self.user_get_timeout,
        }
        for float_name, float_value in positive_floats.items():
            if not math.isfinite(float_value) or float_value <= 0:
                raise ValueError(f"{float_name} must be a finite value greater than zero")
        if self.heartbeat_timeout <= self.heartbeat_interval:
            raise ValueError("heartbeat_timeout must be greater than heartbeat_interval")


Recipients: TypeAlias = ChannelReceivers


@dataclass(frozen=True, slots=True)
class AllUsers:
    pass


@dataclass(frozen=True, slots=True)
class AllExceptSender:
    sender_id: str


@dataclass(frozen=True, slots=True)
class ToUsers:
    user_ids: tuple[str, ...]


RecipientSpec: TypeAlias = AllUsers | AllExceptSender | ToUsers


@dataclass(slots=True)
class User:
    id: str
    assigns: PondAssigns = field(default_factory=dict)
    presence: PondPresence | None = None

    def to_user_data(self) -> UserData:
        return UserData(
            id=self.id,
            assigns=dict(self.assigns),
            presence=dict(self.presence) if isinstance(self.presence, dict) else {},
        )


@dataclass(slots=True)
class MessageEvent:
    event: Event
    user: User


HandlerFn: TypeAlias = Callable[..., Awaitable[None]]
MiddlewareFn: TypeAlias = Callable[[Any, Callable[[], Awaitable[None]]], Awaitable[None]]
