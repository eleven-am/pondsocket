from __future__ import annotations

from urllib.parse import parse_qs, unquote

from .errors import not_found
from .types import Route


def parse(route: str, current_path: str) -> Route:
    query: dict[str, list[str]] = {}
    params: dict[str, str] = {}
    wildcard: str | None = None

    if current_path == "":
        current_path = "/"

    if "?" in current_path:
        path, _, query_string = current_path.partition("?")
        query = parse_qs(query_string, keep_blank_values=True)
    else:
        path = current_path

    route_segments = _split_path(route)
    path_segments = _split_path(path)

    wildcard_index = -1
    for i, seg in enumerate(route_segments):
        if seg == "*":
            wildcard_index = i
            break

    matched = False
    if wildcard_index >= 0:
        if wildcard_index < len(path_segments):
            remaining = "/".join(path_segments[wildcard_index:])
            wildcard = unquote(remaining)
        else:
            wildcard = ""
        route_match = route_segments[:wildcard_index]
        path_match = path_segments[: min(wildcard_index, len(path_segments))]
        matched = _match_segments(route_match, path_match, params)
    else:
        if len(route_segments) == len(path_segments):
            matched = _match_segments(route_segments, path_segments, params)

    if not matched:
        raise not_found(route, f"route {route} does not match path {current_path}")

    return Route(params=params, query=query, wildcard=wildcard)


def _match_segments(
    route_segments: list[str],
    path_segments: list[str],
    params: dict[str, str],
) -> bool:
    if len(route_segments) > len(path_segments):
        return False
    for i, route_seg in enumerate(route_segments):
        if i >= len(path_segments):
            return False
        path_seg = path_segments[i]
        if route_seg.startswith(":"):
            param_name = route_seg[1:]
            params[param_name] = unquote(path_seg)
            continue
        if ":" in route_seg:
            prefix, _, param_name = route_seg.partition(":")
            if not path_seg.startswith(prefix):
                return False
            value = path_seg[len(prefix) :]
            params[param_name] = unquote(value)
            continue
        if route_seg != path_seg:
            return False
    return True


def _split_path(path: str) -> list[str]:
    path = path.strip("/")
    while "//" in path:
        path = path.replace("//", "/")
    if path == "":
        return []
    return path.split("/")
