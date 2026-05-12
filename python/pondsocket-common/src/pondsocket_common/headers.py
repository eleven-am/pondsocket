from __future__ import annotations

from collections.abc import Iterable, Iterator
from typing import overload

RawHeader = tuple[bytes, bytes]
RawHeaders = list[RawHeader]


class Headers:
    __slots__ = ("_raw",)

    def __init__(self, raw: Iterable[tuple[bytes | str, bytes | str]] = ()) -> None:
        normalized: RawHeaders = []
        for name, value in raw:
            name_b = name.encode("latin-1") if isinstance(name, str) else name
            value_b = value.encode("latin-1") if isinstance(value, str) else value
            normalized.append((name_b.lower(), value_b))
        self._raw: RawHeaders = normalized

    @property
    def raw(self) -> RawHeaders:
        return list(self._raw)

    def __len__(self) -> int:
        return len(self._raw)

    def __iter__(self) -> Iterator[str]:
        seen: set[str] = set()
        for name, _ in self._raw:
            key = name.decode("latin-1")
            if key not in seen:
                seen.add(key)
                yield key

    def __contains__(self, name: object) -> bool:
        if not isinstance(name, str):
            return False
        target = name.lower().encode("latin-1")
        return any(n == target for n, _ in self._raw)

    def __getitem__(self, name: str) -> str:
        target = name.lower().encode("latin-1")
        for n, v in self._raw:
            if n == target:
                return v.decode("latin-1")
        raise KeyError(name)

    @overload
    def get(self, name: str) -> str | None: ...
    @overload
    def get(self, name: str, default: str) -> str: ...
    def get(self, name: str, default: str | None = None) -> str | None:
        target = name.lower().encode("latin-1")
        for n, v in self._raw:
            if n == target:
                return v.decode("latin-1")
        return default

    def get_all(self, name: str) -> list[str]:
        target = name.lower().encode("latin-1")
        return [v.decode("latin-1") for n, v in self._raw if n == target]

    def items(self) -> list[tuple[str, str]]:
        return [(n.decode("latin-1"), v.decode("latin-1")) for n, v in self._raw]

    def __repr__(self) -> str:
        return f"Headers({self.items()!r})"
