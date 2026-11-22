from __future__ import annotations

import math
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

    def __post_init__(self) -> None:
        positive_floats = {
            "connection_timeout": self.connection_timeout,
            "base_reconnect_delay": self.base_reconnect_delay,
            "max_reconnect_delay": self.max_reconnect_delay,
            "response_timeout": self.response_timeout,
        }
        if self.ping_interval is not None:
            positive_floats["ping_interval"] = self.ping_interval
        for name, value in positive_floats.items():
            if not math.isfinite(value) or value <= 0:
                raise ValueError(f"{name} must be a finite value greater than zero")
        if self.max_reconnect_delay < self.base_reconnect_delay:
            raise ValueError(
                "max_reconnect_delay must be greater than or equal to "
                "base_reconnect_delay"
            )
        if self.max_reconnect_attempts < -1:
            raise ValueError("max_reconnect_attempts must be -1 or greater")
        if self.max_queue_size <= 0:
            raise ValueError("max_queue_size must be greater than zero")


Publisher: TypeAlias = Callable[[ClientMessage], Awaitable[None]]
