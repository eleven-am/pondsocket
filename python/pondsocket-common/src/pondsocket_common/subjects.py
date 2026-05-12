from __future__ import annotations

from collections.abc import Callable
from typing import Generic, TypeVar

T = TypeVar("T")

_MISSING: object = object()


class Subject(Generic[T]):
    __slots__ = ("_closed", "_observers")

    def __init__(self) -> None:
        self._closed: bool = False
        self._observers: set[Callable[[T], None]] = set()

    @property
    def size(self) -> int:
        return len(self._observers)

    def subscribe(self, observer: Callable[[T], None]) -> Callable[[], None]:
        if self._closed:
            raise RuntimeError("Cannot subscribe to a closed subject")
        self._observers.add(observer)

        def _unsubscribe() -> None:
            self._observers.discard(observer)

        return _unsubscribe

    def publish(self, message: T) -> None:
        for observer in list(self._observers):
            observer(message)

    def close(self) -> None:
        self._observers.clear()
        self._closed = True


class BehaviorSubject(Subject[T]):
    __slots__ = ("_last_message",)

    def __init__(self, initial_value: T | object = _MISSING) -> None:
        super().__init__()
        self._last_message: T | object = initial_value

    @property
    def value(self) -> T | None:
        if self._last_message is _MISSING:
            return None
        return self._last_message  # type: ignore[return-value]

    @property
    def has_value(self) -> bool:
        return self._last_message is not _MISSING

    def publish(self, message: T) -> None:
        self._last_message = message
        super().publish(message)

    def subscribe(self, observer: Callable[[T], None]) -> Callable[[], None]:
        if self._last_message is not _MISSING:
            observer(self._last_message)  # type: ignore[arg-type]
        return super().subscribe(observer)
