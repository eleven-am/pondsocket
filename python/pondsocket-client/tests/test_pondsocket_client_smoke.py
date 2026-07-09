from __future__ import annotations

from importlib.metadata import version

import pondsocket_client


def test_package_imports() -> None:
    assert pondsocket_client.__version__ == version("pondsocket-client")
