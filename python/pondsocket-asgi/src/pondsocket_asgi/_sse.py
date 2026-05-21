from __future__ import annotations

from pondsocket.types import Event
from pondsocket.wire import event_to_json


def event_to_sse_frame(event: Event) -> bytes:
    body = event_to_json(event)
    name = (event.event or "message").replace("\r", " ").replace("\n", " ")
    return f"event: {name}\ndata: {body}\n\n".encode()


def sse_comment(text: str) -> bytes:
    return f": {text}\n\n".encode()
