from __future__ import annotations

import pondsocket_common


def test_package_imports() -> None:
    assert pondsocket_common.__version__ == "0.0.2"
