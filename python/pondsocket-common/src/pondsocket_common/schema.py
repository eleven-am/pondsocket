from __future__ import annotations

from collections.abc import Mapping
from dataclasses import asdict, dataclass, is_dataclass
from typing import Any, Generic, TypeVar, cast

from .types import PondObject

PayloadT = TypeVar("PayloadT")
ResponseT = TypeVar("ResponseT")
PresenceT = TypeVar("PresenceT")
AssignsT = TypeVar("AssignsT")
JoinParamsT = TypeVar("JoinParamsT")


@dataclass(frozen=True, slots=True)
class PondEvent(Generic[PayloadT, ResponseT]):
    name: str
    payload_type: type[PayloadT] | None = None
    response_type: type[ResponseT] | None = None


@dataclass(frozen=True, slots=True)
class PondSchema(Generic[PresenceT, AssignsT, JoinParamsT]):
    name: str = ""
    presence_type: type[PresenceT] | None = None
    assigns_type: type[AssignsT] | None = None
    join_params_type: type[JoinParamsT] | None = None


@dataclass(frozen=True, slots=True)
class TypedPresencePayload(Generic[PresenceT]):
    changed: PresenceT
    presence: list[PresenceT]


def pond_event(
    name: str,
    payload_type: type[PayloadT] | None = None,
    response_type: type[ResponseT] | None = None,
) -> PondEvent[PayloadT, ResponseT]:
    return PondEvent(name, payload_type, response_type)


def pond_schema(
    name: str = "",
    presence_type: type[PresenceT] | None = None,
    assigns_type: type[AssignsT] | None = None,
    join_params_type: type[JoinParamsT] | None = None,
) -> PondSchema[PresenceT, AssignsT, JoinParamsT]:
    return PondSchema(name, presence_type, assigns_type, join_params_type)


def to_pond_object(value: object | None) -> PondObject:
    if value is None:
        return {}
    if isinstance(value, dict):
        return cast(PondObject, dict(value))
    if isinstance(value, Mapping):
        return cast(PondObject, dict(value))
    if is_dataclass(value) and not isinstance(value, type):
        return asdict(value)
    model_dump = getattr(value, "model_dump", None)
    if callable(model_dump):
        dumped = model_dump()
        if isinstance(dumped, Mapping):
            return cast(PondObject, dict(dumped))
    raise TypeError("PondSocket typed payloads must be mappings, dataclasses, or Pydantic models")


def from_pond_object(value: Any, target_type: type[Any] | None = None) -> Any:
    if target_type is None or value is None:
        return value
    model_validate = getattr(target_type, "model_validate", None)
    if callable(model_validate):
        return model_validate(value)
    if is_dataclass(target_type) and isinstance(value, Mapping):
        return target_type(**value)
    return value
