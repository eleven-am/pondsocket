from __future__ import annotations

from typing import Any, Literal, TypeAlias

from pydantic import BaseModel, ConfigDict, Field
from pydantic import ValidationError as PydanticValidationError

from .enums import ClientActions, PresenceEventTypes, ServerActions


class ValidationError(Exception):
    __slots__ = ("path",)

    def __init__(self, message: str, path: str | None = None) -> None:
        self.path: str | None = path
        full = f"{path}: {message}" if path else message
        super().__init__(full)


_WIRE_MODEL_CONFIG = ConfigDict(
    populate_by_name=True,
    extra="ignore",
    use_enum_values=False,
)


class ClientMessage(BaseModel):
    model_config = _WIRE_MODEL_CONFIG

    event: str
    request_id: str = Field(alias="requestId")
    channel_name: str = Field(alias="channelName")
    payload: dict[str, Any]
    action: ClientActions


_SERVER_BROADCAST_ACTIONS = (
    ServerActions.BROADCAST,
    ServerActions.CONNECT,
    ServerActions.ERROR,
    ServerActions.SYSTEM,
)

ServerMessageAction: TypeAlias = Literal[
    ServerActions.BROADCAST,
    ServerActions.CONNECT,
    ServerActions.ERROR,
    ServerActions.SYSTEM,
]


class ServerMessage(BaseModel):
    model_config = _WIRE_MODEL_CONFIG

    event: str
    request_id: str = Field(alias="requestId")
    channel_name: str = Field(alias="channelName")
    payload: dict[str, Any]
    action: ServerMessageAction


class PresencePayloadModel(BaseModel):
    model_config = ConfigDict(extra="ignore")

    presence: list[dict[str, Any]]
    changed: dict[str, Any]


class PresenceMessage(BaseModel):
    model_config = _WIRE_MODEL_CONFIG

    request_id: str = Field(alias="requestId")
    channel_name: str = Field(alias="channelName")
    event: PresenceEventTypes
    action: Literal[ServerActions.PRESENCE]
    payload: PresencePayloadModel


ChannelEvent: TypeAlias = ServerMessage | PresenceMessage


def _path_from_loc(loc: tuple[int | str, ...], root: str) -> str:
    if not loc:
        return root
    return ".".join(str(p) for p in loc)


def _message_from_pydantic(err_type: str, msg: str) -> str:
    if err_type == "missing":
        return "Missing required field"
    if err_type.startswith("string"):
        return "Expected string"
    if err_type in {"dict_type", "model_type", "model_attributes_type", "is_instance_of"}:
        return "Expected object"
    if err_type == "list_type":
        return "Expected array"
    if err_type in {"literal_error", "enum"}:
        return msg
    return msg


def _translate(err: PydanticValidationError, root: str) -> ValidationError:
    errs = err.errors()
    if not errs:
        return ValidationError("validation failed", root)
    first = errs[0]
    loc = first.get("loc", ())
    err_type = str(first.get("type", ""))
    msg = str(first.get("msg", "invalid"))
    return ValidationError(_message_from_pydantic(err_type, msg), _path_from_loc(loc, root))


def _ensure_object(data: Any, root: str) -> dict[str, Any]:
    if not isinstance(data, dict):
        raise ValidationError("Expected object", root)
    return data


def parse_client_message(data: Any) -> ClientMessage:
    _ensure_object(data, "clientMessage")
    try:
        return ClientMessage.model_validate(data)
    except PydanticValidationError as e:
        raise _translate(e, "clientMessage") from None


def parse_server_message(data: Any) -> ServerMessage:
    _ensure_object(data, "serverMessage")
    try:
        return ServerMessage.model_validate(data)
    except PydanticValidationError as e:
        raise _translate(e, "serverMessage") from None


def parse_presence_message(data: Any) -> PresenceMessage:
    _ensure_object(data, "presenceMessage")
    try:
        return PresenceMessage.model_validate(data)
    except PydanticValidationError as e:
        raise _translate(e, "presenceMessage") from None


def parse_channel_event(data: Any) -> ChannelEvent:
    obj = _ensure_object(data, "channelEvent")
    if "action" not in obj:
        raise ValidationError("Missing required field", "action")
    if obj["action"] == ServerActions.PRESENCE:
        return parse_presence_message(data)
    return parse_server_message(data)


__all__ = [
    "ChannelEvent",
    "ClientMessage",
    "PresenceMessage",
    "PresencePayloadModel",
    "ServerMessage",
    "ServerMessageAction",
    "ValidationError",
    "parse_channel_event",
    "parse_client_message",
    "parse_presence_message",
    "parse_server_message",
]
