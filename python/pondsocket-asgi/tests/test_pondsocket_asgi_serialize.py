from __future__ import annotations

import json

import pytest

from pondsocket.types import Event
from pondsocket_asgi._serialize import (
    event_to_json,
    event_to_wire,
    parse_inbound_text,
    wire_to_event,
)
from pondsocket_common import ClientActions, ValidationError, uuid


def test_event_to_wire_renames_snake_to_camel() -> None:
    ev = Event(
        action="SYSTEM",
        channel_name="room/1",
        request_id="r-1",
        event="hello",
        payload={"text": "hi"},
    )
    wire = event_to_wire(ev)
    assert wire == {
        "action": "SYSTEM",
        "event": "hello",
        "channelName": "room/1",
        "requestId": "r-1",
        "payload": {"text": "hi"},
    }


def test_event_to_wire_normalizes_none_payload_to_empty_dict() -> None:
    ev = Event(action="SYSTEM", channel_name="c", request_id="r", event="e")
    wire = event_to_wire(ev)
    assert wire["payload"] == {}


def test_event_to_json_produces_compact_form() -> None:
    ev = Event(action="BROADCAST", channel_name="c", request_id="r", event="e", payload={"a": 1})
    s = event_to_json(ev)
    parsed = json.loads(s)
    assert parsed == event_to_wire(ev)
    assert " " not in s


def test_wire_to_event_parses_valid_client_message() -> None:
    rid = uuid()
    wire = {
        "action": ClientActions.BROADCAST.value,
        "event": "message",
        "channelName": "room/1",
        "requestId": rid,
        "payload": {"text": "hi"},
    }
    ev = wire_to_event(wire)
    assert ev.action == ClientActions.BROADCAST.value
    assert ev.channel_name == "room/1"
    assert ev.request_id == rid
    assert ev.event == "message"
    assert ev.payload == {"text": "hi"}


def test_wire_to_event_rejects_invalid_action() -> None:
    wire = {
        "action": "NOT_A_REAL_ACTION",
        "event": "x",
        "channelName": "c",
        "requestId": "r",
        "payload": {},
    }
    with pytest.raises(ValidationError):
        wire_to_event(wire)


def test_parse_inbound_text_handles_valid_json() -> None:
    rid = uuid()
    text = json.dumps(
        {
            "action": ClientActions.JOIN_CHANNEL.value,
            "event": "join",
            "channelName": "/chat/1",
            "requestId": rid,
            "payload": {"username": "daisy"},
        }
    )
    ev = parse_inbound_text(text)
    assert ev.action == ClientActions.JOIN_CHANNEL.value
    assert ev.payload == {"username": "daisy"}


def test_parse_inbound_text_rejects_invalid_json() -> None:
    with pytest.raises(ValidationError, match="Invalid JSON"):
        parse_inbound_text("{not json}")


def test_parse_inbound_text_rejects_non_object_root() -> None:
    with pytest.raises(ValidationError, match="Expected object"):
        parse_inbound_text("[1, 2, 3]")
