from __future__ import annotations

from typing import TYPE_CHECKING, Any

from pondsocket_common import PondMessage

from ..types import Event, Route, User

if TYPE_CHECKING:
    from ..channel import Channel
    from ..transport import Transport


class OutgoingContext:
    __slots__ = (
        "_blocked",
        "_transformed",
        "channel",
        "event",
        "route",
        "transport",
        "user",
    )

    def __init__(
        self,
        *,
        channel: Channel,
        event: Event,
        user: User,
        transport: Transport,
        route: Route | None = None,
    ) -> None:
        self.channel = channel
        self.event = event
        self.user = user
        self.transport = transport
        self.route = route or Route()
        self._blocked: bool = False
        self._transformed: bool = False

    def block(self) -> OutgoingContext:
        self._blocked = True
        return self

    def unblock(self) -> OutgoingContext:
        self._blocked = False
        return self

    def is_blocked(self) -> bool:
        return self._blocked

    def transform(self, payload: PondMessage) -> OutgoingContext:
        self.event.payload = payload
        self._transformed = True
        return self

    def has_transformed(self) -> bool:
        return self._transformed

    def get_payload(self) -> Any:
        return self.event.payload

    def get_event(self) -> str:
        return self.event.event

    def get_user(self) -> User:
        return self.user

    async def refresh_user(self) -> OutgoingContext:
        self.user = await self.channel.get_user(self.user.id)
        return self
