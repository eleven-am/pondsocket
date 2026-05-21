from __future__ import annotations

from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from enum import StrEnum
from typing import TypeAlias

from pondsocket_common import ClientMessage

DEFAULT_CONNECTION_TIMEOUT: float = 10.0
DEFAULT_MAX_RECONNECT_DELAY: float = 30.0
DEFAULT_BASE_RECONNECT_DELAY: float = 1.0
DEFAULT_PING_INTERVAL: float | None = None
DEFAULT_MAX_QUEUE_SIZE: int = 100
DEFAULT_RESPONSE_TIMEOUT: float = 5.0


class ConnectionState(StrEnum):
    DISCONNECTED = "disconnected"
    CONNECTING = "connecting"
    CONNECTED = "connected"


@dataclass(slots=True)
class ClientOptions:
    connection_timeout: float = DEFAULT_CONNECTION_TIMEOUT
    base_reconnect_delay: float = DEFAULT_BASE_RECONNECT_DELAY
    max_reconnect_delay: float = DEFAULT_MAX_RECONNECT_DELAY
    max_reconnect_attempts: int = -1
    ping_interval: float | None = DEFAULT_PING_INTERVAL
    max_queue_size: int = DEFAULT_MAX_QUEUE_SIZE
    response_timeout: float = DEFAULT_RESPONSE_TIMEOUT


Publisher: TypeAlias = Callable[[ClientMessage], Awaitable[None]]
