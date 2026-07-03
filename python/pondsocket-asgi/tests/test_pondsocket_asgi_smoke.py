from __future__ import annotations

import pondsocket_asgi


def test_package_imports() -> None:
    assert pondsocket_asgi.__version__ == "0.0.4"
