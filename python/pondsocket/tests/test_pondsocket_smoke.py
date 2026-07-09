from __future__ import annotations

from importlib.metadata import version

import pondsocket


def test_package_imports() -> None:
    assert pondsocket.__version__ == version("pondsocket")
