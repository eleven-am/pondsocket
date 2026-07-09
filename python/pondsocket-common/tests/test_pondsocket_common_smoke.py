from __future__ import annotations

from importlib.metadata import version

import pondsocket_common


def test_package_imports() -> None:
    assert pondsocket_common.__version__ == version("pondsocket-common")
