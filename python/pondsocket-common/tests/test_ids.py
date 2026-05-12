from __future__ import annotations

import re

from pondsocket_common import uuid

_UUID_RE = re.compile(
    r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"
)


def test_uuid_returns_dashed_lowercase_hex_string() -> None:
    value = uuid()
    assert isinstance(value, str)
    assert _UUID_RE.match(value), value


def test_uuid_is_unique() -> None:
    seen = {uuid() for _ in range(1000)}
    assert len(seen) == 1000
