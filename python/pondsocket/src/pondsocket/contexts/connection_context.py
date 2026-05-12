from __future__ import annotations

from enum import StrEnum
from typing import Any

from pondsocket_common import Headers, IncomingConnection, PondAssigns, PondMessage

from ..errors import bad_request
from ..types import Route, SystemEntity


class ConnectionDecision(StrEnum):
    PENDING = "PENDING"
    ACCEPTED = "ACCEPTED"
    DECLINED = "DECLINED"


class ConnectionContext:
    __slots__ = (
        "_assigns",
        "_decision",
        "_decline_code",
        "_decline_message",
        "_pending_reply",
        "_user_id",
        "request",
        "route",
    )

    def __init__(
        self,
        *,
        user_id: str,
        request: IncomingConnection,
        route: Route | None = None,
    ) -> None:
        self._user_id = user_id
        self.request = request
        self.route = route or Route()
        self._assigns: PondAssigns = {}
        self._decision: ConnectionDecision = ConnectionDecision.PENDING
        self._decline_code: int = 0
        self._decline_message: str = ""
        self._pending_reply: tuple[str, PondMessage] | None = None

    @property
    def user_id(self) -> str:
        return self._user_id

    @property
    def headers(self) -> Headers:
        return self.request.headers

    @property
    def cookies(self) -> dict[str, str]:
        return self.request.cookies

    @property
    def query(self) -> dict[str, str]:
        return self.request.query

    @property
    def params(self) -> dict[str, str]:
        return self.request.params

    @property
    def address(self) -> str:
        return self.request.address

    @property
    def assigns(self) -> PondAssigns:
        return dict(self._assigns)

    @property
    def decision(self) -> ConnectionDecision:
        return self._decision

    @property
    def is_accepted(self) -> bool:
        return self._decision is ConnectionDecision.ACCEPTED

    @property
    def is_declined(self) -> bool:
        return self._decision is ConnectionDecision.DECLINED

    @property
    def decline_info(self) -> tuple[int, str]:
        return (self._decline_code, self._decline_message)

    @property
    def pending_reply(self) -> tuple[str, PondMessage] | None:
        return self._pending_reply

    def accept(self, **assigns: Any) -> ConnectionContext:
        if self._decision is not ConnectionDecision.PENDING:
            raise bad_request(
                SystemEntity.GATEWAY.value,
                "ConnectionContext: the response has already been sent",
            )
        self._decision = ConnectionDecision.ACCEPTED
        if assigns:
            self._assigns.update(assigns)
        return self

    def decline(self, code: int, message: str) -> None:
        if self._decision is not ConnectionDecision.PENDING:
            raise bad_request(
                SystemEntity.GATEWAY.value,
                "ConnectionContext: the response has already been sent",
            )
        self._decision = ConnectionDecision.DECLINED
        self._decline_code = code
        self._decline_message = message

    def reply(self, event_name: str, payload: PondMessage | None = None) -> ConnectionContext:
        if self._decision is ConnectionDecision.PENDING:
            self.accept()
        if self._decision is ConnectionDecision.DECLINED:
            raise bad_request(
                SystemEntity.GATEWAY.value,
                "ConnectionContext: cannot reply after declining",
            )
        self._pending_reply = (event_name, payload or {})
        return self

    def set_assign(self, key: str, value: Any) -> ConnectionContext:
        self._assigns[key] = value
        return self

    def set_assigns(self, **assigns: Any) -> ConnectionContext:
        self._assigns.update(assigns)
        return self

    def get_assign(self, key: str) -> Any:
        return self._assigns.get(key)
