from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass, field
from typing import Any, TypeAlias

from .enums import ChannelReceiver, PubSubEvents
from .headers import Headers

Subscriber: TypeAlias = Callable[[Any], None]
Unsubscribe: TypeAlias = Callable[[], None]

PondObject: TypeAlias = dict[str, Any]
PondMessage: TypeAlias = PondObject
PondPresence: TypeAlias = PondObject
PondAssigns: TypeAlias = PondObject
JoinParams: TypeAlias = PondObject

ChannelReceivers: TypeAlias = ChannelReceiver | list[str]
UserAssigns: TypeAlias = dict[str, PondAssigns]
UserPresences: TypeAlias = dict[str, PondPresence]


@dataclass(slots=True)
class UserData:
    id: str
    assigns: PondAssigns = field(default_factory=dict)
    presence: PondPresence = field(default_factory=dict)


@dataclass(slots=True)
class IncomingConnection:
    id: str
    address: str
    headers: Headers = field(default_factory=Headers)
    cookies: dict[str, str] = field(default_factory=dict)
    query: dict[str, str] = field(default_factory=dict)
    params: dict[str, str] = field(default_factory=dict)


@dataclass(slots=True)
class PresencePayload:
    changed: PondPresence
    presence: list[PondPresence]


@dataclass(slots=True)
class PubSubGetPresenceCommand:
    endpoint: str
    channel: str
    pub_sub_id: str
    event: PubSubEvents = PubSubEvents.GET_PRESENCE


@dataclass(slots=True)
class PubSubPresenceEvent:
    endpoint: str
    channel: str
    pub_sub_id: str
    presence: UserPresences
    event: PubSubEvents = PubSubEvents.PRESENCE


@dataclass(slots=True)
class PubSubMessageEvent:
    endpoint: str
    channel: str
    pub_sub_id: str
    message: dict[str, Any]
    recipient: ChannelReceivers
    event: PubSubEvents = PubSubEvents.MESSAGE


PubSubEvent: TypeAlias = (
    PubSubGetPresenceCommand | PubSubPresenceEvent | PubSubMessageEvent
)
