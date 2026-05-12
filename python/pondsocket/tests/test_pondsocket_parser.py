from __future__ import annotations

import pytest

from pondsocket.errors import PondError
from pondsocket.parser import parse


def test_exact_path_match_no_params() -> None:
    r = parse("/users/profile", "/users/profile")
    assert r.params == {}
    assert r.query == {}
    assert r.wildcard is None


def test_named_param() -> None:
    r = parse("/chat/:room_id", "/chat/42")
    assert r.params == {"room_id": "42"}
    assert r.param("room_id") == "42"
    assert r.param("missing") == ""


def test_multiple_named_params() -> None:
    r = parse("/users/:user_id/posts/:post_id", "/users/u1/posts/p2")
    assert r.params == {"user_id": "u1", "post_id": "p2"}


def test_prefix_param() -> None:
    r = parse("/file-:name", "/file-readme.md")
    assert r.params == {"name": "readme.md"}


def test_wildcard_captures_rest() -> None:
    r = parse("/assets/*", "/assets/images/logo.png")
    assert r.wildcard == "images/logo.png"


def test_wildcard_only_route_captures_everything() -> None:
    r = parse("*", "/some/nested/path")
    assert r.wildcard == "some/nested/path"


def test_wildcard_with_empty_tail() -> None:
    r = parse("/assets/*", "/assets")
    assert r.wildcard == ""


def test_query_parsed_into_lists() -> None:
    r = parse("/api/x", "/api/x?token=abc&token=xyz&user=daisy")
    assert r.query_param("token") == ["abc", "xyz"]
    assert r.query_param("user") == ["daisy"]
    assert r.query_param("missing") == []


def test_url_encoded_param_is_decoded() -> None:
    r = parse("/users/:name", "/users/hello%20world")
    assert r.params == {"name": "hello world"}


def test_mismatched_length_raises_not_found() -> None:
    with pytest.raises(PondError) as exc:
        parse("/a/b", "/a/b/c")
    assert exc.value.code == 404


def test_literal_mismatch_raises() -> None:
    with pytest.raises(PondError):
        parse("/foo", "/bar")


def test_empty_path_defaults_to_root() -> None:
    r = parse("/", "")
    assert r.wildcard is None
    assert r.params == {}


def test_repeated_slashes_collapse() -> None:
    r = parse("/a/b", "//a//b//")
    assert r.params == {}
