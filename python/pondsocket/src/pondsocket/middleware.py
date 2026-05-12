from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from typing import Generic, TypeAlias, TypeVar

from .errors import PondError, wrap

Request = TypeVar("Request")
Response = TypeVar("Response")

NextFn: TypeAlias = Callable[[], Awaitable[None]]
HandlerFunc: TypeAlias = Callable[[Request, Response, NextFn], Awaitable[None]]
FinalHandlerFunc: TypeAlias = Callable[[Request, Response], Awaitable[None]]


class Middleware(Generic[Request, Response]):
    __slots__ = ("_handlers", "_lock")

    def __init__(self) -> None:
        self._handlers: list[HandlerFunc[Request, Response]] = []
        self._lock = asyncio.Lock()

    async def use(self, *handlers: HandlerFunc[Request, Response]) -> None:
        async with self._lock:
            self._handlers.extend(handlers)

    def use_sync(self, *handlers: HandlerFunc[Request, Response]) -> None:
        self._handlers.extend(handlers)

    async def compose(
        self, *others: Middleware[Request, Response]
    ) -> Middleware[Request, Response]:
        result: Middleware[Request, Response] = Middleware()
        async with self._lock:
            result._handlers.extend(self._handlers)
        for other in others:
            async with other._lock:
                result._handlers.extend(other._handlers)
        return result

    async def handle(
        self,
        request: Request,
        response: Response,
        final: FinalHandlerFunc[Request, Response],
    ) -> None:
        async with self._lock:
            handlers = list(self._handlers)

        if not handlers:
            await final(request, response)
            return

        async def run(index: int) -> None:
            if index >= len(handlers):
                await final(request, response)
                return

            async def next_fn() -> None:
                await run(index + 1)

            await handlers[index](request, response, next_fn)

        await run(0)

    def __len__(self) -> int:
        return len(self._handlers)


C = TypeVar("C")
PerHandlerMiddleware: TypeAlias = Callable[[C, NextFn], Awaitable[None]]


async def execute_with_middleware(
    ctx: C,
    handler: Callable[[C], Awaitable[None]],
    middlewares: list[PerHandlerMiddleware[C]],
) -> None:
    if not middlewares:
        await handler(ctx)
        return

    async def run(index: int) -> None:
        if index >= len(middlewares):
            await handler(ctx)
            return

        async def next_fn() -> None:
            await run(index + 1)

        await middlewares[index](ctx, next_fn)

    await run(0)


def wrap_pond_error(err: BaseException, message: str) -> PondError:
    if isinstance(err, PondError):
        return wrap(err, message)
    return wrap(err, message)
