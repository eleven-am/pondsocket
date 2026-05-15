from __future__ import annotations

from collections.abc import Awaitable, Callable
from typing import Protocol, TypeAlias, runtime_checkable

PubSubHandler: TypeAlias = Callable[[str, bytes], Awaitable[None]]


@runtime_checkable
class PubSub(Protocol):
    async def subscribe(self, pattern: str, handler: PubSubHandler) -> None: ...
    async def unsubscribe(self, pattern: str) -> None: ...
    async def publish(self, topic: str, data: bytes) -> None: ...
    async def close(self) -> None: ...


class PubSubClosedError(Exception):
    def __init__(self) -> None:
        super().__init__("pubsub: closed")


def match_topic(pattern: str, topic: str) -> bool:
    if pattern == topic:
        return True
    if len(pattern) > 2 and pattern.endswith(".*"):
        prefix = pattern[:-2]
        return topic.startswith(prefix)
    return False


def format_topic(endpoint: str, channel: str, event: str) -> str:
    del event
    return f"pondsocket:v1:default:{endpoint}:{channel}"


def format_heartbeat_topic() -> str:
    return "pondsocket:v1:default:__heartbeat__"


def format_presence_topic(endpoint: str, channel: str) -> str:
    return format_topic(endpoint, channel, "presence")


def format_message_topic(endpoint: str, channel: str) -> str:
    return format_topic(endpoint, channel, "message")


def format_system_topic(event: str) -> str:
    return f"pondsocket:v1:default:system:{event}"
