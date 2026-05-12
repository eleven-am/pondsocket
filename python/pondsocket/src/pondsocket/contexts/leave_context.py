from __future__ import annotations

from typing import TYPE_CHECKING

from pondsocket_common import PondAssigns, PondMessage, PondPresence

from ..errors import bad_request
from ..types import Route, User

if TYPE_CHECKING:
    from ..channel import Channel


class LeaveContext:
    __slots__ = ("_responded", "channel", "reason", "route", "user")

    def __init__(
        self,
        *,
        channel: Channel,
        user: User,
        reason: str,
        route: Route | None = None,
    ) -> None:
        self.channel = channel
        self.user = user
        self.reason = reason
        self.route = route or Route()
        self._responded: bool = False

    def get_reason(self) -> str:
        return self.reason

    def get_user(self) -> User:
        return self.user

    def get_assign(self, key: str) -> object:
        if not self.user.assigns:
            return None
        return self.user.assigns.get(key)

    async def remaining_user_count(self) -> int:
        return await self.channel.user_count()

    async def get_all_presence(self) -> dict[str, PondPresence]:
        return await self.channel.get_presence()

    async def get_all_assigns(self) -> dict[str, PondAssigns]:
        return await self.channel.get_assigns()

    def _claim_response(self) -> None:
        if self._responded:
            raise bad_request(self.channel.name, "Already broadcast leave notification")
        self._responded = True

    async def broadcast(self, event_name: str, payload: PondMessage) -> LeaveContext:
        self._claim_response()
        await self.channel.broadcast(event_name, payload)
        return self

    async def broadcast_to(
        self,
        event_name: str,
        payload: PondMessage,
        *user_ids: str,
    ) -> LeaveContext:
        self._claim_response()
        await self.channel.broadcast_to(event_name, payload, *user_ids)
        return self

    async def broadcast_from(
        self,
        event_name: str,
        payload: PondMessage,
    ) -> LeaveContext:
        self._claim_response()
        await self.channel.broadcast_from(event_name, payload, self.user.id)
        return self
