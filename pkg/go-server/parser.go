package main

import (
	"fmt"
	"net/url"
	"strings"
)

func parse(route, currentPath string) (*Route, error) {
	query := make(map[string][]string)

	params := make(map[string]string)

	var wildcard *string
	matched := false
	if currentPath == "" {
		currentPath = "/"
	}
	pathAndQuery := strings.SplitN(currentPath, "?", 2)

	path := pathAndQuery[0]
	if len(pathAndQuery) > 1 {
		queryString := pathAndQuery[1]
		queryValues, err := url.ParseQuery(queryString)

		if err != nil {
			return nil, fmt.Errorf("invalid query string: %w", err)
		}
		query = queryValues
	}
	routeSegments := splitPath(route)

	pathSegments := splitPath(path)

	wildcardIndex := -1
	for i, routeSeg := range routeSegments {
		if routeSeg == "*" {
			wildcardIndex = i
			break
		}
	}
	if wildcardIndex >= 0 {
		if wildcardIndex < len(pathSegments) {
			wildcardParts := pathSegments[wildcardIndex:]
			remainingPath := strings.Join(wildcardParts, "/")

			decodedPath, err := url.QueryUnescape(remainingPath)

			if err == nil {
				remainingPath = decodedPath
			}
			wildcard = &remainingPath
		} else {
			emptyStr := ""
			wildcard = &emptyStr
		}
		routeSegmentsToMatch := routeSegments[:wildcardIndex]
		pathSegmentsToMatch := pathSegments[:minInt(wildcardIndex, len(pathSegments))]
		if matchSegments(routeSegmentsToMatch, pathSegmentsToMatch, params) {
			matched = true
		}
	} else {
		if len(routeSegments) == len(pathSegments) {
			if matchSegments(routeSegments, pathSegments, params) {
				matched = true
			}
		}
	}
	if !matched {
		return nil, fmt.Errorf("route %s does not match path %s", route, currentPath)
	}
	return &Route{
		Query:    query,
		Params:   params,
		Wildcard: wildcard,
	}, nil
}

func matchSegments(routeSegments, pathSegments []string, params map[string]string) bool {
	if len(routeSegments) > len(pathSegments) {
		return false
	}
	for i, routeSeg := range routeSegments {
		if i >= len(pathSegments) {
			return false
		}
		pathSeg := pathSegments[i]
		if strings.HasPrefix(routeSeg, ":") {
			paramName := strings.TrimPrefix(routeSeg, ":")

			decodedValue, err := url.QueryUnescape(pathSeg)

			if err == nil {
				pathSeg = decodedValue
			}
			params[paramName] = pathSeg
			continue
		}
		if strings.Contains(routeSeg, ":") {
			parts := strings.SplitN(routeSeg, ":", 2)

			prefix := parts[0]
			paramName := parts[1]
			if !strings.HasPrefix(pathSeg, prefix) {
				return false
			}
			paramValue := strings.TrimPrefix(pathSeg, prefix)

			decodedValue, err := url.QueryUnescape(paramValue)

			if err == nil {
				paramValue = decodedValue
			}
			params[paramName] = paramValue
			continue
		}
		if routeSeg != pathSeg {
			return false
		}
	}
	return true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")

	for strings.Contains(path, "//") {
		path = strings.ReplaceAll(path, "//", "/")
	}
	if path == "" {
		return []string{}
	}
	return strings.Split(path, "/")
}

func (r *Route) ParseQuery(v interface{}) error {
	return parsePayload(v, r.Query)
}

func (r *Route) ParseParams(v interface{}) error {
	return parsePayload(v, r.Params)
}
