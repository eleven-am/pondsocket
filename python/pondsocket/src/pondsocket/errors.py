from __future__ import annotations

from typing import Any

from pondsocket_common import (
    STATUS_BAD_REQUEST,
    STATUS_CONFLICT,
    STATUS_FORBIDDEN,
    STATUS_GATEWAY_TIMEOUT,
    STATUS_INTERNAL_SERVER_ERROR,
    STATUS_NOT_FOUND,
    STATUS_SERVICE_UNAVAILABLE,
    STATUS_TOO_MANY_REQUESTS,
    STATUS_UNAUTHORIZED,
    MultiError,
    PondError,
    ServerActions,
    add_error,
    bad_request,
    combine,
    conflict,
    forbidden,
    internal,
    not_found,
    temporary_error,
    timeout,
    too_many_requests,
    unauthorized,
    unavailable,
    uuid,
    wrap,
)

from .types import Event, SystemEntity, SystemEvents


def error_event(err: BaseException | None) -> Event | None:
    if err is None:
        return None
    if isinstance(err, PondError):
        message = str(err.cause) if err.cause is not None else err.message
        payload: dict[str, Any] = {
            "code": err.code,
            "details": err.details,
            "temporary": err.temporary,
            "message": message,
        }
        return Event(
            action=ServerActions.SYSTEM.value,
            channel_name=err.channel_name or SystemEntity.GATEWAY.value,
            request_id=uuid(),
            event=SystemEvents.INTERNAL_ERROR.value,
            payload=payload,
        )
    return Event(
        action=ServerActions.SYSTEM.value,
        channel_name=SystemEntity.GATEWAY.value,
        request_id=uuid(),
        event=SystemEvents.INTERNAL_ERROR.value,
        payload={"message": str(err)},
    )


__all__ = [
    "STATUS_BAD_REQUEST",
    "STATUS_CONFLICT",
    "STATUS_FORBIDDEN",
    "STATUS_GATEWAY_TIMEOUT",
    "STATUS_INTERNAL_SERVER_ERROR",
    "STATUS_NOT_FOUND",
    "STATUS_SERVICE_UNAVAILABLE",
    "STATUS_TOO_MANY_REQUESTS",
    "STATUS_UNAUTHORIZED",
    "MultiError",
    "PondError",
    "add_error",
    "bad_request",
    "combine",
    "conflict",
    "error_event",
    "forbidden",
    "internal",
    "not_found",
    "temporary_error",
    "timeout",
    "too_many_requests",
    "unauthorized",
    "unavailable",
    "wrap",
]
