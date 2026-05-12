from __future__ import annotations

import json
from typing import Any

from pondsocket_common import ValidationError, parse_client_message

from .types import Event


def event_to_wire(event: Event) -> dict[str, Any]:
    return {
        "action": event.action,
        "event": event.event,
        "channelName": event.channel_name,
        "requestId": event.request_id,
        "payload": event.payload if event.payload is not None else {},
    }


def event_to_json(event: Event) -> str:
    return json.dumps(event_to_wire(event), ensure_ascii=False, separators=(",", ":"))


def wire_to_event(data: dict[str, Any]) -> Event:
    msg = parse_client_message(data)
    return Event(
        action=msg.action.value,
        channel_name=msg.channel_name,
        request_id=msg.request_id,
        event=msg.event,
        payload=msg.payload,
    )


def parse_inbound_text(text: str) -> Event:
    try:
        data = json.loads(text)
    except json.JSONDecodeError as e:
        raise ValidationError(f"Invalid JSON: {e.msg}", "wireMessage") from e
    if not isinstance(data, dict):
        raise ValidationError("Expected object", "wireMessage")
    return wire_to_event(data)


def event_to_pubsub_bytes(event: Event) -> bytes:
    data: dict[str, Any] = {
        "action": event.action,
        "event": event.event,
        "channelName": event.channel_name,
        "requestId": event.request_id,
        "payload": event.payload if event.payload is not None else {},
        "nodeId": event.node_id,
    }
    return json.dumps(data, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def pubsub_bytes_to_event(data: bytes) -> Event | None:
    try:
        text = data.decode("utf-8")
        wire = json.loads(text)
    except (UnicodeDecodeError, json.JSONDecodeError):
        return None
    if not isinstance(wire, dict):
        return None
    action = wire.get("action")
    channel_name = wire.get("channelName")
    request_id = wire.get("requestId")
    event_name = wire.get("event")
    if not isinstance(action, str) or not action:
        return None
    if not isinstance(channel_name, str) or not channel_name:
        return None
    if not isinstance(request_id, str) or not request_id:
        return None
    if not isinstance(event_name, str) or not event_name:
        return None
    payload_raw = wire.get("payload")
    payload: Any = payload_raw if payload_raw is not None else {}
    return Event(
        action=action,
        channel_name=channel_name,
        request_id=request_id,
        event=event_name,
        payload=payload,
        node_id=str(wire.get("nodeId", "")),
    )
