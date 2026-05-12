from __future__ import annotations

import pytest

from pondsocket_common import (
    STATUS_BAD_REQUEST,
    STATUS_GATEWAY_TIMEOUT,
    STATUS_INTERNAL_SERVER_ERROR,
    STATUS_NOT_FOUND,
    STATUS_SERVICE_UNAVAILABLE,
    STATUS_TOO_MANY_REQUESTS,
    MultiError,
    PondError,
    add_error,
    bad_request,
    combine,
    internal,
    not_found,
    temporary_error,
    timeout,
    too_many_requests,
    unavailable,
    wrap,
)


def test_pond_error_default_message_without_channel() -> None:
    err = PondError("nope", STATUS_INTERNAL_SERVER_ERROR)
    assert str(err) == "nope (code: 500)"
    assert err.channel_name == ""
    assert err.temporary is False
    assert err.details is None


def test_pond_error_message_includes_channel_when_set() -> None:
    err = bad_request("room/42", "missing token")
    assert str(err) == "Error in Channel room/42: missing token (code: 400)"
    assert err.code == STATUS_BAD_REQUEST


def test_constructors_set_temporary_flag() -> None:
    assert too_many_requests("", "slow down").temporary is True
    assert too_many_requests("", "slow down").code == STATUS_TOO_MANY_REQUESTS
    assert unavailable("", "wait").temporary is True
    assert unavailable("", "wait").code == STATUS_SERVICE_UNAVAILABLE
    assert timeout("", "late").temporary is True
    assert timeout("", "late").code == STATUS_GATEWAY_TIMEOUT
    assert temporary_error("", "retry", 418).temporary is True
    assert temporary_error("", "retry", 418).code == 418


def test_not_found_and_internal() -> None:
    nf = not_found("room/42", "no channel")
    assert nf.code == STATUS_NOT_FOUND
    assert nf.channel_name == "room/42"
    assert internal("", "boom").code == STATUS_INTERNAL_SERVER_ERROR


def test_wrap_preserves_pond_error_fields() -> None:
    base = bad_request("room", "bad")
    wrapped = wrap(base, "while joining")
    assert wrapped.code == STATUS_BAD_REQUEST
    assert wrapped.channel_name == "room"
    assert "while joining: bad" in wrapped.message


def test_wrap_with_plain_exception_becomes_internal() -> None:
    plain = RuntimeError("kaboom")
    wrapped = wrap(plain, "context")
    assert wrapped.code == STATUS_INTERNAL_SERVER_ERROR
    assert "context: kaboom" in wrapped.message
    assert wrapped.cause is plain


def test_multi_error_concatenates_messages() -> None:
    err = MultiError([RuntimeError("a"), RuntimeError("b")])
    assert "a; b" in str(err)


def test_multi_error_empty_string() -> None:
    assert str(MultiError([])) == "no errors"


def test_combine_returns_none_when_all_none() -> None:
    assert combine(None, None) is None


def test_combine_returns_single_error_unchanged() -> None:
    e = RuntimeError("only")
    assert combine(None, e, None) is e


def test_combine_returns_multi_error_for_multiple() -> None:
    a = RuntimeError("a")
    b = RuntimeError("b")
    combined = combine(a, b)
    assert isinstance(combined, MultiError)
    assert combined.errors == [a, b]


def test_add_error_handles_none_cases() -> None:
    e = RuntimeError("x")
    assert add_error(None, e) is e
    assert add_error(e, None) is e


def test_add_error_appends_to_multi_error() -> None:
    a = RuntimeError("a")
    b = RuntimeError("b")
    c = RuntimeError("c")
    me = MultiError([a, b])
    out = add_error(me, c)
    assert out is me
    assert me.errors == [a, b, c]


def test_add_error_creates_multi_error_from_two_singles() -> None:
    a = RuntimeError("a")
    b = RuntimeError("b")
    out = add_error(a, b)
    assert isinstance(out, MultiError)
    assert out.errors == [a, b]


def test_pond_error_raise_preserves_cause() -> None:
    cause = ValueError("root")
    err = PondError("wrapped", STATUS_INTERNAL_SERVER_ERROR, cause=cause)
    with pytest.raises(PondError) as exc:
        raise err
    assert exc.value.__cause__ is cause
