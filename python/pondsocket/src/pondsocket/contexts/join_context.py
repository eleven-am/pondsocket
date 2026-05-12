from __future__ import annotations

import asyncio
from typing import TYPE_CHECKING, Any

from pondsocket_common import (
    JoinParams,
    PondAssigns,
    PondMessage,
    PondPresence,
    ServerActions,
)

from ..errors import bad_request
from ..types import Event, Route, SystemEvents

if TYPE_CHECKING:
    from ..channel import Channel
    from ..transport import Transport


class JoinContext:
    __slots__ = (
        "_accepted",
        "_lock",
        "_responded",
        "channel",
        "event",
        "route",
        "transport",
    )

    def __init__(
        self,
        *,
        channel: Channel,
        event: Event,
        transport: Transport,
        route: Route | None = None,
    ) -> None:
        self.channel = channel
        self.event = event
        self.transport = transport
        self.route = route or Route()
        self._responded: bool = False
        self._accepted: bool = False
        self._lock = asyncio.Lock()

    @property
    def has_responded(self) -> bool:
        return self._responded

    @property
    def is_accepted(self) -> bool:
        return self._accepted

    @property
    def join_params(self) -> JoinParams:
        payload = self.event.payload
        if isinstance(payload, dict):
            return payload
        return {}

    def get_join_params(self) -> JoinParams:
        return self.join_params

    async def accept(self, assigns: PondAssigns | None = None) -> JoinContext:
        async with self._lock:
            if self._responded:
                raise bad_request(self.channel.name, "Already responded to the join request")
            self._responded = True
            self._accepted = True
        if assigns:
            for k, v in assigns.items():
                self.transport.set_assign(k, v)
        await self.channel.add_user(self.transport)
        await self.channel.send_acknowledge(
            self.event.request_id,
            self.transport.get_id(),
        )
        return self

    async def decline(self, code: int, message: str) -> None:
        async with self._lock:
            if self._responded:
                raise bad_request(self.channel.name, "Already responded to the join request")
            self._responded = True
            self._accepted = False
        ev = Event(
            action=ServerActions.SYSTEM.value,
            channel_name=self.channel.name,
            request_id=self.event.request_id,
            event=SystemEvents.UNAUTHORIZED.value,
            payload={"code": code, "message": message},
        )
        try:
            await self.transport.send_event(ev)
        except Exception:
            return

    async def reply(self, event_name: str, payload: PondMessage) -> JoinContext:
        if not self._accepted:
            raise bad_request(self.channel.name, "Cannot reply before accepting join request")
        await self.channel.send_system(
            event_name,
            payload,
            request_id=self.event.request_id,
            user_ids=[self.transport.get_id()],
        )
        return self

    async def track(self, presence: PondPresence) -> JoinContext:
        if not self._accepted:
            raise bad_request(self.channel.name, "Cannot track presence before accepting join request")
        await self.channel.track_presence(self.transport.get_id(), presence)
        return self

    async def broadcast(self, event_name: str, payload: PondMessage) -> JoinContext:
        if not self._accepted:
            raise bad_request(self.channel.name, "Cannot broadcast before accepting join request")
        await self.channel.broadcast(event_name, payload)
        return self

    async def broadcast_to(
        self,
        event_name: str,
        payload: PondMessage,
        *user_ids: str,
    ) -> JoinContext:
        if not self._accepted:
            raise bad_request(self.channel.name, "Cannot broadcast before accepting join request")
        await self.channel.broadcast_to(event_name, payload, *user_ids)
        return self

    async def broadcast_from(self, event_name: str, payload: PondMessage) -> JoinContext:
        if not self._accepted:
            raise bad_request(self.channel.name, "Cannot broadcast before accepting join request")
        await self.channel.broadcast_from(event_name, payload, self.transport.get_id())
        return self

    async def set_assign(self, key: str, value: Any) -> JoinContext:
        if not self._accepted:
            self.transport.set_assign(key, value)
        else:
            await self.channel.update_assign(self.transport.get_id(), key, value)
        return self

    async def assign(self, assigns: PondAssigns) -> JoinContext:
        for k, v in assigns.items():
            await self.set_assign(k, v)
        return self

    async def get_assign(self, key: str) -> Any:
        if not self._accepted:
            return self.transport.get_assign(key)
        return await self.channel.get_user_assign(self.transport.get_id(), key)

    async def get_all_presence(self) -> dict[str, PondPresence]:
        return await self.channel.get_presence()

    async def get_all_assigns(self) -> dict[str, PondAssigns]:
        return await self.channel.get_assigns()
