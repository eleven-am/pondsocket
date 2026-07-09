from __future__ import annotations

from importlib.metadata import version

import pondsocket_asgi


def test_package_imports() -> None:
    assert pondsocket_asgi.__version__ == version("pondsocket-asgi")
