use std::collections::HashMap;

use crate::errors::{Result, not_found};
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
                .entry(percent_decode(key))
                .or_insert_with(Vec::new)
                .push(percent_decode(value));
        }
    }

    let route_segments = split_path(route);
    let path_segments = split_path(path);
    let wildcard_index = route_segments.iter().position(|s| s == "*");

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
    let mut out = String::with_capacity(value.len());
    let bytes = value.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%'
            && i + 2 < bytes.len()
            && let Ok(hex) = u8::from_str_radix(&value[i + 1..i + 3], 16)
        {
            out.push(hex as char);
            i += 3;
            continue;
        }
        out.push(bytes[i] as char);
        i += 1;
    }
    out
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
}
