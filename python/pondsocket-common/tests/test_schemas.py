from __future__ import annotations

from typing import Any

import pytest

from pondsocket_common import (
    ClientActions,
    PresenceEventTypes,
    ServerActions,
    ValidationError,
    parse_channel_event,
    parse_client_message,
    parse_presence_message,
    parse_server_message,
)


def _client_message(**overrides: Any) -> dict[str, Any]:
    base: dict[str, Any] = {
        "event": "test",
        "requestId": "123",
        "channelName": "channel",
        "payload": {"data": "test"},
        "action": ClientActions.BROADCAST.value,
    }
    base.update(overrides)
    return base


def _server_message(**overrides: Any) -> dict[str, Any]:
    base: dict[str, Any] = {
        "event": "test",
        "requestId": "123",
        "channelName": "channel",
        "payload": {"data": "test"},
        "action": ServerActions.BROADCAST.value,
    }
    base.update(overrides)
    return base


def _presence_message(**overrides: Any) -> dict[str, Any]:
    base: dict[str, Any] = {
        "requestId": "123",
        "channelName": "channel",
        "event": PresenceEventTypes.JOIN.value,
        "action": ServerActions.PRESENCE.value,
        "payload": {
            "presence": [{"userId": "1"}, {"userId": "2"}],
            "changed": {"userId": "1"},
        },
    }
    base.update(overrides)
    return base


class TestClientMessage:
    def test_parses_valid_message(self) -> None:
        msg = parse_client_message(_client_message())
        assert msg.event == "test"
        assert msg.request_id == "123"
        assert msg.channel_name == "channel"
        assert msg.payload == {"data": "test"}
        assert msg.action == ClientActions.BROADCAST

    def test_roundtrips_to_wire_json(self) -> None:
        original = _client_message()
        msg = parse_client_message(original)
        dumped = msg.model_dump(by_alias=True, mode="json")
        assert dumped == original

    def test_missing_event(self) -> None:
        data = _client_message()
        data.pop("event")
        with pytest.raises(ValidationError) as exc:
            parse_client_message(data)
        assert exc.value.path == "event"
        assert "Missing required field" in str(exc.value)

    def test_missing_request_id(self) -> None:
        data = _client_message()
        data.pop("requestId")
        with pytest.raises(ValidationError) as exc:
            parse_client_message(data)
        assert exc.value.path == "requestId"
        assert "Missing required field" in str(exc.value)

    def test_missing_channel_name(self) -> None:
        data = _client_message()
        data.pop("channelName")
        with pytest.raises(ValidationError) as exc:
            parse_client_message(data)
        assert exc.value.path == "channelName"

    def test_missing_payload(self) -> None:
        data = _client_message()
        data.pop("payload")
        with pytest.raises(ValidationError) as exc:
            parse_client_message(data)
        assert exc.value.path == "payload"

    def test_missing_action(self) -> None:
        data = _client_message()
        data.pop("action")
        with pytest.raises(ValidationError) as exc:
            parse_client_message(data)
        assert exc.value.path == "action"

    def test_non_string_event(self) -> None:
        data = _client_message(event=123)
        with pytest.raises(ValidationError) as exc:
            parse_client_message(data)
        assert exc.value.path == "event"
        assert "Expected string" in str(exc.value)

    def test_non_object_payload(self) -> None:
        data = _client_message(payload="not an object")
        with pytest.raises(ValidationError) as exc:
            parse_client_message(data)
        assert exc.value.path == "payload"

    def test_invalid_action_enum(self) -> None:
        data = _client_message(action="INVALID_ACTION")
        with pytest.raises(ValidationError) as exc:
            parse_client_message(data)
        assert exc.value.path == "action"

    def test_non_object_input(self) -> None:
        for bad in ("not an object", None, [], 42):
            with pytest.raises(ValidationError) as exc:
                parse_client_message(bad)
            assert exc.value.path == "clientMessage"


class TestServerMessage:
    def test_parses_valid_message(self) -> None:
        msg = parse_server_message(_server_message())
        assert msg.action == ServerActions.BROADCAST

    def test_accepts_all_server_actions(self) -> None:
        for action in (
            ServerActions.BROADCAST,
            ServerActions.CONNECT,
            ServerActions.ERROR,
            ServerActions.SYSTEM,
        ):
            parse_server_message(_server_message(action=action.value))

    def test_rejects_presence_action(self) -> None:
        with pytest.raises(ValidationError) as exc:
            parse_server_message(_server_message(action=ServerActions.PRESENCE.value))
        assert exc.value.path == "action"

    def test_missing_action(self) -> None:
        data = _server_message()
        data.pop("action")
        with pytest.raises(ValidationError) as exc:
            parse_server_message(data)
        assert exc.value.path == "action"

    def test_missing_payload(self) -> None:
        data = _server_message()
        data.pop("payload")
        with pytest.raises(ValidationError) as exc:
            parse_server_message(data)
        assert exc.value.path == "payload"


class TestPresenceMessage:
    def test_parses_valid_message(self) -> None:
        msg = parse_presence_message(_presence_message())
        assert msg.action == ServerActions.PRESENCE
        assert msg.event == PresenceEventTypes.JOIN
        assert msg.payload.presence == [{"userId": "1"}, {"userId": "2"}]
        assert msg.payload.changed == {"userId": "1"}

    def test_roundtrip_serializes_to_wire_form(self) -> None:
        original = _presence_message()
        msg = parse_presence_message(original)
        dumped = msg.model_dump(by_alias=True, mode="json")
        assert dumped == original

    def test_rejects_non_presence_action(self) -> None:
        with pytest.raises(ValidationError) as exc:
            parse_presence_message(
                _presence_message(action=ServerActions.BROADCAST.value)
            )
        assert exc.value.path == "action"

    def test_missing_payload_presence(self) -> None:
        data = _presence_message()
        data["payload"] = {"changed": {}}
        with pytest.raises(ValidationError) as exc:
            parse_presence_message(data)
        assert exc.value.path == "payload.presence"

    def test_missing_payload_changed(self) -> None:
        data = _presence_message()
        data["payload"] = {"presence": []}
        with pytest.raises(ValidationError) as exc:
            parse_presence_message(data)
        assert exc.value.path == "payload.changed"

    def test_non_array_presence(self) -> None:
        data = _presence_message()
        data["payload"] = {"presence": "not an array", "changed": {}}
        with pytest.raises(ValidationError) as exc:
            parse_presence_message(data)
        assert exc.value.path == "payload.presence"
        assert "Expected array" in str(exc.value)

    def test_non_object_payload(self) -> None:
        data = _presence_message(payload="not an object")
        with pytest.raises(ValidationError) as exc:
            parse_presence_message(data)
        assert exc.value.path == "payload"


class TestChannelEventDispatch:
    def test_parses_server_message_when_action_not_presence(self) -> None:
        msg = parse_channel_event(_server_message())
        assert msg.action == ServerActions.BROADCAST

    def test_parses_presence_message_when_action_is_presence(self) -> None:
        msg = parse_channel_event(_presence_message())
        assert msg.action == ServerActions.PRESENCE

    def test_missing_action_at_top_level(self) -> None:
        data = _server_message()
        data.pop("action")
        with pytest.raises(ValidationError) as exc:
            parse_channel_event(data)
        assert exc.value.path == "action"


class TestValidationError:
    def test_includes_path_in_message(self) -> None:
        err = ValidationError("Test error", "field.path")
        assert str(err) == "field.path: Test error"
        assert err.path == "field.path"

    def test_works_without_path(self) -> None:
        err = ValidationError("Test error")
        assert str(err) == "Test error"
        assert err.path is None
