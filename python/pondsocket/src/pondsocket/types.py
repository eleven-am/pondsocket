from __future__ import annotations

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
    hooks: Hooks | None = None


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
