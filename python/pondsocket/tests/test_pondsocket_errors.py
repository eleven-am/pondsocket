from __future__ import annotations

from pondsocket.errors import (
    STATUS_INTERNAL_SERVER_ERROR,
    PondError,
    error_event,
)
from pondsocket.types import SystemEntity, SystemEvents


def test_error_event_none_returns_none() -> None:
    assert error_event(None) is None


def test_error_event_from_pond_error_carries_fields() -> None:
    err = PondError(
        "bad token",
        401,
        channel_name="room/1",
        temporary=False,
        details={"hint": "refresh"},
    )
    ev = error_event(err)
    assert ev is not None
    assert ev.channel_name == "room/1"
    assert ev.event == SystemEvents.INTERNAL_ERROR.value
    assert ev.payload["code"] == 401
    assert ev.payload["details"] == {"hint": "refresh"}
    assert ev.payload["temporary"] is False
    assert ev.payload["message"] == "bad token"


def test_error_event_from_plain_exception_routes_to_gateway() -> None:
    ev = error_event(RuntimeError("kaboom"))
    assert ev is not None
    assert ev.channel_name == SystemEntity.GATEWAY.value
    assert ev.event == SystemEvents.INTERNAL_ERROR.value
    assert ev.payload == {"message": "kaboom"}


def test_error_event_from_wrapped_cause_uses_cause_message() -> None:
    cause = ValueError("real reason")
    err = PondError("wrapped", STATUS_INTERNAL_SERVER_ERROR, cause=cause)
    ev = error_event(err)
    assert ev is not None
    assert ev.payload["message"] == "real reason"
