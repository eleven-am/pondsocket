from __future__ import annotations

from importlib.metadata import PackageNotFoundError, version

from .app import PondASGIApp
from .transport import ASGIWebSocketTransport

try:
    __version__ = version("pondsocket-asgi")
except PackageNotFoundError:
    __version__ = "0.0.1"

__all__ = [
    "ASGIWebSocketTransport",
    "PondASGIApp",
    "__version__",
]
