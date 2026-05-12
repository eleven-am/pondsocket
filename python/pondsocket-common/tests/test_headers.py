from __future__ import annotations

import pytest

from pondsocket_common import Headers


def test_construct_from_asgi_raw() -> None:
    h = Headers([(b"content-type", b"application/json"), (b"x-foo", b"bar")])
    assert h["content-type"] == "application/json"
    assert h["x-foo"] == "bar"


def test_case_insensitive_lookup() -> None:
    h = Headers([(b"Content-Type", b"text/plain")])
    assert h["content-type"] == "text/plain"
    assert h["Content-Type"] == "text/plain"
    assert h["CONTENT-TYPE"] == "text/plain"


def test_get_returns_default_when_missing() -> None:
    h = Headers()
    assert h.get("x-missing") is None
    assert h.get("x-missing", "fallback") == "fallback"


def test_contains_is_case_insensitive() -> None:
    h = Headers([(b"X-Token", b"abc")])
    assert "x-token" in h
    assert "X-TOKEN" in h
    assert "x-other" not in h
    assert 42 not in h


def test_getitem_raises_keyerror_on_miss() -> None:
    h = Headers()
    with pytest.raises(KeyError):
        _ = h["x-missing"]


def test_get_all_returns_every_value_for_duplicate_names() -> None:
    h = Headers([(b"set-cookie", b"a=1"), (b"set-cookie", b"b=2")])
    assert h.get_all("set-cookie") == ["a=1", "b=2"]
    assert h.get_all("missing") == []


def test_iter_yields_unique_keys_only() -> None:
    h = Headers([(b"a", b"1"), (b"b", b"2"), (b"a", b"3")])
    assert list(h) == ["a", "b"]


def test_items_preserves_order_and_duplicates() -> None:
    h = Headers([(b"a", b"1"), (b"b", b"2"), (b"a", b"3")])
    assert h.items() == [("a", "1"), ("b", "2"), ("a", "3")]


def test_accepts_string_input_and_normalizes_to_bytes() -> None:
    h = Headers([("Content-Type", "application/json")])
    assert h["content-type"] == "application/json"
    assert h.raw == [(b"content-type", b"application/json")]


def test_raw_returns_a_copy() -> None:
    h = Headers([(b"a", b"1")])
    raw = h.raw
    raw.append((b"injected", b"x"))
    assert "injected" not in h


def test_len_counts_entries_including_duplicates() -> None:
    h = Headers([(b"a", b"1"), (b"a", b"2"), (b"b", b"3")])
    assert len(h) == 3


def test_empty_headers() -> None:
    h = Headers()
    assert len(h) == 0
    assert list(h) == []
    assert h.items() == []
    assert h.raw == []
