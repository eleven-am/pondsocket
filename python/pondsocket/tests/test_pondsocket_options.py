from __future__ import annotations

import pytest

from pondsocket import Options


@pytest.mark.parametrize(
    ("field", "value"),
    [
        ("max_message_size", 0),
        ("send_timeout", 0.0),
        ("internal_queue_timeout", 0.0),
        ("internal_queue_buffer", 0),
        ("dispatch_concurrency", 0),
        ("send_channel_buffer", 0),
        ("receive_channel_buffer", 0),
        ("max_concurrent_handlers", 0),
        ("max_connections", -1),
        ("heartbeat_interval", 0.0),
        ("heartbeat_timeout", 0.0),
        ("user_get_timeout", 0.0),
    ],
)
def test_invalid_options_fail_immediately(field: str, value: object) -> None:
    with pytest.raises(ValueError, match=field):
        Options(**{field: value})  # type: ignore[arg-type]


def test_heartbeat_timeout_must_exceed_interval() -> None:
    with pytest.raises(ValueError, match="heartbeat_timeout"):
        Options(heartbeat_interval=10.0, heartbeat_timeout=10.0)
