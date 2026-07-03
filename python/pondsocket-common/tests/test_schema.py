from __future__ import annotations

from dataclasses import dataclass

import pytest
from pydantic import BaseModel

from pondsocket_common import (
    PondEvent,
    PondSchema,
    from_pond_object,
    pond_event,
    pond_schema,
    to_pond_object,
)


@dataclass(frozen=True)
class ChatMessage:
    text: str
    author: str


class ChatModel(BaseModel):
    text: str
    author: str


def test_to_pond_object_from_dataclass() -> None:
    assert to_pond_object(ChatMessage(text="hi", author="alice")) == {
        "text": "hi",
        "author": "alice",
    }


def test_to_pond_object_from_mapping_copies() -> None:
    source = {"text": "hi"}
    result = to_pond_object(source)
    assert result == {"text": "hi"}
    assert result is not source


def test_to_pond_object_from_pydantic_model() -> None:
    assert to_pond_object(ChatModel(text="hi", author="alice")) == {
        "text": "hi",
        "author": "alice",
    }


def test_to_pond_object_from_none_is_empty() -> None:
    assert to_pond_object(None) == {}


def test_to_pond_object_rejects_unsupported() -> None:
    with pytest.raises(TypeError):
        to_pond_object(object())


def test_from_pond_object_without_type_is_identity() -> None:
    payload = {"text": "hi"}
    assert from_pond_object(payload) is payload


def test_from_pond_object_none_stays_none() -> None:
    assert from_pond_object(None, ChatMessage) is None


def test_dataclass_round_trip() -> None:
    original = ChatMessage(text="hello", author="bob")
    encoded = to_pond_object(original)
    decoded = from_pond_object(encoded, ChatMessage)
    assert decoded == original
    assert isinstance(decoded, ChatMessage)


def test_pydantic_round_trip() -> None:
    original = ChatModel(text="hello", author="bob")
    encoded = to_pond_object(original)
    decoded = from_pond_object(encoded, ChatModel)
    assert decoded == original
    assert isinstance(decoded, ChatModel)


def test_pond_event_carries_runtime_types() -> None:
    event: PondEvent[ChatMessage, ChatMessage] = pond_event(
        "chat", ChatMessage, ChatMessage
    )
    assert event.name == "chat"
    assert event.payload_type is ChatMessage
    assert event.response_type is ChatMessage


def test_pond_event_defaults_have_no_types() -> None:
    event: PondEvent[dict[str, str], dict[str, str]] = pond_event("chat")
    assert event.payload_type is None
    assert event.response_type is None


def test_pond_schema_carries_presence_type() -> None:
    schema: PondSchema[ChatMessage, dict[str, str], dict[str, str]] = pond_schema(
        "room", ChatMessage
    )
    assert schema.name == "room"
    assert schema.presence_type is ChatMessage
