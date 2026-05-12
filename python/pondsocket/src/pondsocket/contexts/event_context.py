from __future__ import annotations

from typing import TYPE_CHECKING, Any

from pondsocket_common import PondAssigns, PondMessage, PondPresence

from ..types import Event, MessageEvent, Route, User

if TYPE_CHECKING:
    from ..channel import Channel


class EventContext:
    __slots__ = ("_replied", "channel", "event", "route", "user")

    def __init__(
        self,
        *,
        channel: Channel,
        message: MessageEvent,
        route: Route | None = None,
    ) -> None:
        self.channel = channel
        self.event: Event = message.event
        self.user: User = message.user
        self.route = route or Route()
        self._replied: bool = False

    @property
    def request_id(self) -> str:
        return self.event.request_id

    @property
    def event_name(self) -> str:
        return self.event.event

    def get_payload(self) -> Any:
        return self.event.payload

    def get_user(self) -> User:
        return self.user

    def has_replied(self) -> bool:
        return self._replied

    async def reply(self, event_name: str, payload: PondMessage) -> EventContext:
        if self._replied:
            return self
        self._replied = True
        await self.channel.send_system(
            event_name,
            payload,
            request_id=self.event.request_id,
            user_ids=[self.user.id],
        )
        return self

    async def broadcast(self, event_name: str, payload: PondMessage) -> EventContext:
        await self.channel.broadcast(event_name, payload)
        return self

    async def broadcast_to(
        self,
        event_name: str,
        payload: PondMessage,
        *user_ids: str,
    ) -> EventContext:
        await self.channel.broadcast_to(event_name, payload, *user_ids)
        return self

    async def broadcast_from(
        self,
        event_name: str,
        payload: PondMessage,
    ) -> EventContext:
        await self.channel.broadcast_from(event_name, payload, self.user.id)
        return self

    async def track(
        self,
        presence: PondPresence,
        *user_ids: str,
    ) -> EventContext:
        targets = list(user_ids) or [self.user.id]
        for uid in targets:
            await self.channel.track_presence(uid, presence)
        return self

    async def update_presence(
        self,
        presence: PondPresence,
        *user_ids: str,
    ) -> EventContext:
        targets = list(user_ids) or [self.user.id]
        for uid in targets:
            await self.channel.update_presence(uid, presence)
        return self

    async def untrack(self, *user_ids: str) -> EventContext:
        targets = list(user_ids) or [self.user.id]
        for uid in targets:
            await self.channel.untrack_presence(uid)
        return self

    async def evict(self, reason: str, *user_ids: str) -> EventContext:
        targets = list(user_ids) or [self.user.id]
        for uid in targets:
            await self.channel.evict_user(uid, reason)
        return self

    async def set_assign(self, key: str, value: Any) -> EventContext:
        await self.channel.update_assign(self.user.id, key, value)
        return self

    async def assign(self, assigns: PondAssigns) -> EventContext:
        for k, v in assigns.items():
            await self.channel.update_assign(self.user.id, k, v)
        return self

    async def get_assign(self, key: str) -> Any:
        return await self.channel.get_user_assign(self.user.id, key)

    async def get_all_presence(self) -> dict[str, PondPresence]:
        return await self.channel.get_presence()

    async def get_all_assigns(self) -> dict[str, PondAssigns]:
        return await self.channel.get_assigns()
