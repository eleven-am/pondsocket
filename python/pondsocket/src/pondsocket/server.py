from __future__ import annotations

from dataclasses import dataclass, replace

from pondsocket_common import uuid

from .endpoint import (
    ConnectionHandler,
    ConnectionMiddlewareFn,
    Endpoint,
)
from .errors import PondError
from .heartbeat import HeartbeatCoordinator
from .parser import parse
from .pubsub import PubSub
from .transport import Transport
from .types import Options, Route


@dataclass(slots=True, frozen=True)
class EndpointMatch:
    endpoint: Endpoint
    route: Route


class PondSocket:
    __slots__ = ("_distributed", "_endpoints", "_options", "_pubsub")

    def __init__(
        self,
        *,
        options: Options | None = None,
        pubsub: PubSub | None = None,
    ) -> None:
        base_options = options or Options()
        if pubsub is not None and not base_options.node_id:
            base_options = replace(base_options, node_id=uuid())
        self._options = base_options
        self._pubsub = pubsub
        self._distributed: HeartbeatCoordinator | None = None
        if pubsub is not None:
            self._distributed = HeartbeatCoordinator(
                pubsub,
                base_options.node_id,
                namespace=base_options.namespace,
                key_prefix=base_options.key_prefix,
                interval=base_options.heartbeat_interval,
                timeout=base_options.heartbeat_timeout,
            )
        self._endpoints: list[Endpoint] = []

    @property
    def options(self) -> Options:
        return self._options

    @property
    def pubsub(self) -> PubSub | None:
        return self._pubsub

    @property
    def endpoints(self) -> list[Endpoint]:
        return list(self._endpoints)

    def create_endpoint(
        self,
        path: str,
        handler: ConnectionHandler,
        *middlewares: ConnectionMiddlewareFn,
    ) -> Endpoint:
        endpoint = Endpoint(
            path=path,
            connection_handler=handler,
            options=self._options,
            pubsub=self._pubsub,
            heartbeat=self._distributed,
            connection_middlewares=list(middlewares),
        )
        self._endpoints.append(endpoint)
        return endpoint

    def match_endpoint(self, path: str) -> EndpointMatch | None:
        for endpoint in self._endpoints:
            try:
                route = parse(endpoint.path, path)
            except PondError:
                continue
            return EndpointMatch(endpoint=endpoint, route=route)
        return None

    async def close(self) -> None:
        for endpoint in self._endpoints:
            await endpoint.close()
        self._endpoints.clear()
        if self._distributed is not None:
            await self._distributed.close()

    async def find_transport(self, user_id: str) -> Transport | None:
        for endpoint in self._endpoints:
            transport = await endpoint.get_transport(user_id)
            if transport is not None:
                return transport
        return None
