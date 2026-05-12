from __future__ import annotations

from collections.abc import Mapping
from typing import Any

STATUS_BAD_REQUEST = 400
STATUS_UNAUTHORIZED = 401
STATUS_FORBIDDEN = 403
STATUS_NOT_FOUND = 404
STATUS_CONFLICT = 409
STATUS_TOO_MANY_REQUESTS = 429
STATUS_INTERNAL_SERVER_ERROR = 500
STATUS_SERVICE_UNAVAILABLE = 503
STATUS_GATEWAY_TIMEOUT = 504


class PondError(Exception):
    __slots__ = ("cause", "channel_name", "code", "details", "message", "temporary")

    def __init__(
        self,
        message: str,
        code: int,
        *,
        channel_name: str = "",
        temporary: bool = False,
        details: Mapping[str, Any] | None = None,
        cause: BaseException | None = None,
    ) -> None:
        if code < 100:
            raise ValueError(f"PondError code must be >= 100, got {code}")
        self.message = message
        self.code = code
        self.channel_name = channel_name
        self.temporary = temporary
        self.details: Mapping[str, Any] | None = details
        self.cause = cause
        super().__init__(str(self))
        if cause is not None:
            self.__cause__ = cause

    def __str__(self) -> str:
        if self.channel_name:
            return f"Error in Channel {self.channel_name}: {self.message} (code: {self.code})"
        return f"{self.message} (code: {self.code})"


def bad_request(channel_name: str, message: str) -> PondError:
    return PondError(message, STATUS_BAD_REQUEST, channel_name=channel_name)


def unauthorized(channel_name: str, message: str) -> PondError:
    return PondError(message, STATUS_UNAUTHORIZED, channel_name=channel_name)


def forbidden(channel_name: str, message: str) -> PondError:
    return PondError(message, STATUS_FORBIDDEN, channel_name=channel_name)


def not_found(channel_name: str, message: str) -> PondError:
    return PondError(message, STATUS_NOT_FOUND, channel_name=channel_name)


def conflict(channel_name: str, message: str) -> PondError:
    return PondError(message, STATUS_CONFLICT, channel_name=channel_name)


def too_many_requests(channel_name: str, message: str) -> PondError:
    return PondError(
        message, STATUS_TOO_MANY_REQUESTS, channel_name=channel_name, temporary=True
    )


def internal(channel_name: str, message: str) -> PondError:
    return PondError(message, STATUS_INTERNAL_SERVER_ERROR, channel_name=channel_name)


def unavailable(channel_name: str, message: str) -> PondError:
    return PondError(
        message, STATUS_SERVICE_UNAVAILABLE, channel_name=channel_name, temporary=True
    )


def timeout(channel_name: str, message: str) -> PondError:
    return PondError(
        message, STATUS_GATEWAY_TIMEOUT, channel_name=channel_name, temporary=True
    )


def temporary_error(channel_name: str, message: str, code: int) -> PondError:
    return PondError(message, code, channel_name=channel_name, temporary=True)


def wrap(err: BaseException, message: str) -> PondError:
    if isinstance(err, PondError):
        return PondError(
            f"{message}: {err.message}",
            err.code,
            channel_name=err.channel_name,
            temporary=err.temporary,
            details=err.details,
            cause=err.cause,
        )
    return PondError(
        f"{message}: {err}",
        STATUS_INTERNAL_SERVER_ERROR,
        cause=err,
    )


class MultiError(Exception):
    __slots__ = ("errors",)

    def __init__(self, errors: list[BaseException]) -> None:
        self.errors = list(errors)
        super().__init__(str(self))

    def __str__(self) -> str:
        if not self.errors:
            return "no errors"
        return "; ".join(str(e) for e in self.errors)


def combine(*errs: BaseException | None) -> BaseException | None:
    non_nil = [e for e in errs if e is not None]
    if not non_nil:
        return None
    if len(non_nil) == 1:
        return non_nil[0]
    return MultiError(non_nil)


def add_error(
    base: BaseException | None, new: BaseException | None
) -> BaseException | None:
    if base is None:
        return new
    if new is None:
        return base
    if isinstance(base, MultiError):
        base.errors.append(new)
        return base
    return MultiError([base, new])
