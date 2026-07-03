from __future__ import annotations

import pondsocket_client


def test_package_imports() -> None:
    assert pondsocket_client.__version__ == "0.0.4"
