from __future__ import annotations

from uuid import uuid4


def uuid() -> str:
    return str(uuid4())
