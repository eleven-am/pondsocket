use std::collections::HashMap;

use crate::errors::{Result, bad_request, not_found};
use crate::types::Route;

pub fn parse(route: &str, current_path: &str) -> Result<Route> {
    let mut params = HashMap::new();
    let mut query = HashMap::new();
    let mut wildcard = None;
    let current_path = if current_path.is_empty() {
        "/"
    } else {
        current_path
    };
    let (path, query_string) = current_path
        .split_once('?')
        .map_or((current_path, ""), |(p, q)| (p, q));

    if !query_string.is_empty() {
        for part in query_string.split('&') {
            let (key, value) = part.split_once('=').unwrap_or((part, ""));
            query
                .entry(form_percent_decode(key))
                .or_insert_with(Vec::new)
                .push(form_percent_decode(value));
        }
    }

    let route_segments = split_path(route);
    let path_segments = split_path(path);
    let wildcard_indexes = route_segments
        .iter()
        .enumerate()
        .filter_map(|(index, segment)| (segment == "*").then_some(index))
        .collect::<Vec<_>>();
    if wildcard_indexes.len() > 1
        || wildcard_indexes
            .first()
            .is_some_and(|index| *index + 1 != route_segments.len())
    {
        return Err(bad_request(
            route,
            "route wildcard must appear once and as the final segment",
        ));
    }
    if route_segments.iter().any(|segment| {
        segment.strip_prefix(':').is_some_and(str::is_empty)
            || segment
                .split_once(':')
                .is_some_and(|(_, parameter)| parameter.is_empty())
    }) {
        return Err(bad_request(route, "route parameter name must not be empty"));
    }
    let wildcard_index = wildcard_indexes.first().copied();

    let matched = if let Some(index) = wildcard_index {
        wildcard = Some(path_segments.get(index..).unwrap_or(&[]).join("/"));
        match_segments(
            &route_segments[..index],
            &path_segments[..index.min(path_segments.len())],
            &mut params,
        )
    } else {
        route_segments.len() == path_segments.len()
            && match_segments(&route_segments, &path_segments, &mut params)
    };

    if !matched {
        return Err(not_found(
            route,
            format!("route {route} does not match path {current_path}"),
        ));
    }

    Ok(Route {
        params,
        query,
        wildcard,
    })
}

fn match_segments(
    route_segments: &[String],
    path_segments: &[String],
    params: &mut HashMap<String, String>,
) -> bool {
    if route_segments.len() > path_segments.len() {
        return false;
    }
    for (route_seg, path_seg) in route_segments.iter().zip(path_segments.iter()) {
        if let Some(param_name) = route_seg.strip_prefix(':') {
            params.insert(param_name.to_owned(), percent_decode(path_seg));
            continue;
        }
        if let Some((prefix, param_name)) = route_seg.split_once(':') {
            if !path_seg.starts_with(prefix) {
                return false;
            }
            params.insert(
                param_name.to_owned(),
                percent_decode(&path_seg[prefix.len()..]),
            );
            continue;
        }
        if route_seg != path_seg {
            return false;
        }
    }
    true
}

fn split_path(path: &str) -> Vec<String> {
    let mut path = path.trim_matches('/').replace("//", "/");
    while path.contains("//") {
        path = path.replace("//", "/");
    }
    if path.is_empty() {
        Vec::new()
    } else {
        path.split('/').map(ToOwned::to_owned).collect()
    }
}

fn percent_decode(value: &str) -> String {
    let mut out = Vec::with_capacity(value.len());
    let bytes = value.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%'
            && i + 2 < bytes.len()
            && let (Some(high), Some(low)) = (hex_value(bytes[i + 1]), hex_value(bytes[i + 2]))
        {
            out.push((high << 4) | low);
            i += 3;
            continue;
        }
        out.push(bytes[i]);
        i += 1;
    }
    String::from_utf8_lossy(&out).into_owned()
}

fn hex_value(byte: u8) -> Option<u8> {
    match byte {
        b'0'..=b'9' => Some(byte - b'0'),
        b'a'..=b'f' => Some(byte - b'a' + 10),
        b'A'..=b'F' => Some(byte - b'A' + 10),
        _ => None,
    }
}

fn form_percent_decode(value: &str) -> String {
    percent_decode(&value.replace('+', " "))
}

#[cfg(test)]
mod tests {
    use super::parse;

    #[test]
    fn matches_params_prefix_and_query() {
        let route = parse("/chat/room-:id/*", "/chat/room-42/a/b?token=x&token=y").unwrap();
        assert_eq!(route.param("id"), Some("42"));
        assert_eq!(route.wildcard.as_deref(), Some("a/b"));
        assert_eq!(
            route.query_param("token"),
            &["x".to_string(), "y".to_string()]
        );
    }

    #[test]
    fn decodes_utf8_and_form_encoded_query_values() {
        let route = parse(
            "/caf%C3%A9/:name",
            "/caf%C3%A9/Jos%C3%A9?q=hello+world&emoji=%F0%9F%8C%8A",
        )
        .unwrap();
        assert_eq!(route.param("name"), Some("José"));
        assert_eq!(route.query_param("q"), &["hello world".to_owned()]);
        assert_eq!(route.query_param("emoji"), &["🌊".to_owned()]);
    }

    #[test]
    fn plus_remains_literal_in_path_parameters() {
        let route = parse("/users/:name", "/users/a+b").unwrap();
        assert_eq!(route.param("name"), Some("a+b"));
    }

    #[test]
    fn rejects_non_terminal_or_duplicate_wildcards() {
        assert!(parse("/room/*/events", "/room/a/events").is_err());
        assert!(parse("/room/*/*", "/room/a/b").is_err());
    }

    #[test]
    fn rejects_empty_parameter_names() {
        assert!(parse("/room/:", "/room/value").is_err());
        assert!(parse("/room/prefix:", "/room/prefix-value").is_err());
    }

    #[test]
    fn malformed_percent_encoding_never_panics() {
        let route = parse("/room/:value", "/room/%aé").unwrap();
        assert_eq!(route.param("value"), Some("%aé"));
    }
}
