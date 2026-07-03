from __future__ import annotations

import json
import time
from typing import Any

from pondsocket_common import uuid

PROTOCOL = "pondsocket.distributed"
VERSION = 1
HEARTBEAT_PART = "__heartbeat__"


def now_ms() -> int:
    return int(time.time() * 1000)


def distributed_bytes(
    msg_type: str,
    node_id: str,
    endpoint: str,
    channel: str,
    extra: dict[str, Any],
) -> bytes:
    envelope: dict[str, Any] = {
        "protocol": PROTOCOL,
        "version": VERSION,
        "type": msg_type,
        "messageId": uuid(),
        "sourceNodeId": node_id,
        "endpointName": endpoint,
        "channelName": channel,
        "timestamp": now_ms(),
    }
    envelope.update(extra)
    return json.dumps(envelope, ensure_ascii=False, separators=(",", ":")).encode("utf-8")


def heartbeat_bytes(node_id: str) -> bytes:
    return distributed_bytes(
        "NODE_HEARTBEAT",
        node_id,
        HEARTBEAT_PART,
        HEARTBEAT_PART,
        {"nodeId": node_id},
    )


def parse_envelope(data: bytes) -> dict[str, Any] | None:
    try:
        raw = json.loads(data.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        return None
    if not isinstance(raw, dict):
        return None
    if raw.get("protocol") != PROTOCOL or raw.get("version") != VERSION:
        return None
    if not isinstance(raw.get("type"), str):
        return None
    if not isinstance(raw.get("sourceNodeId"), str):
        return None
    return raw
