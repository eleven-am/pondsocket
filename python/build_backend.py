from __future__ import annotations


def _raise() -> None:
    raise RuntimeError(
        "The workspace root is not publishable. Build from one of: "
        "pondsocket-common/, pondsocket/, or pondsocket-asgi/."
    )


def build_sdist(
    sdist_directory: str,
    config_settings: dict[str, object] | None = None,
) -> str:
    del sdist_directory, config_settings
    _raise()


def build_wheel(
    wheel_directory: str,
    config_settings: dict[str, object] | None = None,
    metadata_directory: str | None = None,
) -> str:
    del wheel_directory, config_settings, metadata_directory
    _raise()


def get_requires_for_build_sdist(
    config_settings: dict[str, object] | None = None,
) -> list[str]:
    del config_settings
    return []


def get_requires_for_build_wheel(
    config_settings: dict[str, object] | None = None,
) -> list[str]:
    del config_settings
    return []


def prepare_metadata_for_build_wheel(
    metadata_directory: str,
    config_settings: dict[str, object] | None = None,
) -> str:
    del metadata_directory, config_settings
    _raise()
