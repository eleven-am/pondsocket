package main

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	t.Run("parses simple routes", func(t *testing.T) {
		tests := []struct {
			pattern string
			path    string
			want    *Route
			wantErr bool
		}{
			{
				pattern: "/room/123",
				path:    "/room/123",
				want: &Route{
					Query:  map[string][]string{},
					Params: map[string]string{},
				},
			},
			{
				pattern: "/user/profile",
				path:    "/user/profile",
				want: &Route{
					Query:  map[string][]string{},
					Params: map[string]string{},
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.pattern, func(t *testing.T) {
				route, err := parse(tt.pattern, tt.path)

				if (err != nil) != tt.wantErr {
					t.Errorf("parse() error = %v, wantErr %v", err, tt.wantErr)

					return
				}
				if !reflect.DeepEqual(route, tt.want) {
					t.Errorf("parse() = %+v, want %+v", route, tt.want)
				}
			})
		}
	})

	t.Run("parses routes with parameters", func(t *testing.T) {
		tests := []struct {
			pattern string
			path    string
			want    *Route
			wantErr bool
		}{
			{
				pattern: "/room/:id",
				path:    "/room/123",
				want: &Route{
					Query: map[string][]string{},
					Params: map[string]string{
						"id": "123",
					},
				},
			},
			{
				pattern: "/user/:userId/profile",
				path:    "/user/john/profile",
				want: &Route{
					Query: map[string][]string{},
					Params: map[string]string{
						"userId": "john",
					},
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.pattern, func(t *testing.T) {
				route, err := parse(tt.pattern, tt.path)

				if (err != nil) != tt.wantErr {
					t.Errorf("parse() error = %v, wantErr %v", err, tt.wantErr)

					return
				}
				if !reflect.DeepEqual(route, tt.want) {
					t.Errorf("parse() = %+v, want %+v", route, tt.want)
				}
			})
		}
	})

	t.Run("parses routes with multiple parameters", func(t *testing.T) {
		tests := []struct {
			pattern string
			path    string
			want    *Route
			wantErr bool
		}{
			{
				pattern: "/api/:version/users/:id",
				path:    "/api/v1/users/123",
				want: &Route{
					Query: map[string][]string{},
					Params: map[string]string{
						"version": "v1",
						"id":      "123",
					},
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.pattern, func(t *testing.T) {
				route, err := parse(tt.pattern, tt.path)

				if (err != nil) != tt.wantErr {
					t.Errorf("parse() error = %v, wantErr %v", err, tt.wantErr)

					return
				}
				if !reflect.DeepEqual(route, tt.want) {
					t.Errorf("parse() = %+v, want %+v", route, tt.want)
				}
			})
		}
	})

	t.Run("parses routes with wildcards", func(t *testing.T) {
		tests := []struct {
			pattern string
			path    string
			want    *Route
			wantErr bool
		}{
			{
				pattern: "/files/*",
				path:    "/files/docs/readme.txt",
				want: &Route{
					Query:  map[string][]string{},
					Params: map[string]string{},
					Wildcard: func() *string {
						s := "docs/readme.txt"
						return &s
					}(),
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.pattern, func(t *testing.T) {
				route, err := parse(tt.pattern, tt.path)

				if (err != nil) != tt.wantErr {
					t.Errorf("parse() error = %v, wantErr %v", err, tt.wantErr)

					return
				}
				if route.Wildcard == nil && tt.want.Wildcard != nil {
					t.Errorf("expected wildcard %v, got nil", *tt.want.Wildcard)
				} else if route.Wildcard != nil && tt.want.Wildcard != nil && *route.Wildcard != *tt.want.Wildcard {
					t.Errorf("expected wildcard %v, got %v", *tt.want.Wildcard, *route.Wildcard)
				}
			})
		}
	})

	t.Run("parses query parameters", func(t *testing.T) {
		tests := []struct {
			pattern string
			path    string
			want    *Route
			wantErr bool
		}{
			{
				pattern: "/search",
				path:    "/search?q=test&sort=asc",
				want: &Route{
					Query: map[string][]string{
						"q":    {"test"},
						"sort": {"asc"},
					},
					Params: map[string]string{},
				},
			},
			{
				pattern: "/search",
				path:    "/search?q=test&q=another",
				want: &Route{
					Query: map[string][]string{
						"q": {"test", "another"},
					},
					Params: map[string]string{},
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.path, func(t *testing.T) {
				route, err := parse(tt.pattern, tt.path)

				if (err != nil) != tt.wantErr {
					t.Errorf("parse() error = %v, wantErr %v", err, tt.wantErr)

					return
				}
				if !reflect.DeepEqual(route.Query, tt.want.Query) {
					t.Errorf("parse() Query = %+v, want %+v", route.Query, tt.want.Query)
				}
			})
		}
	})

	t.Run("handles URL escaping", func(t *testing.T) {
		tests := []struct {
			pattern string
			path    string
			want    *Route
			wantErr bool
		}{
			{
				pattern: "/user/:name",
				path:    "/user/John%20Doe",
				want: &Route{
					Query: map[string][]string{},
					Params: map[string]string{
						"name": "John Doe",
					},
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.path, func(t *testing.T) {
				route, err := parse(tt.pattern, tt.path)

				if (err != nil) != tt.wantErr {
					t.Errorf("parse() error = %v, wantErr %v", err, tt.wantErr)

					return
				}
				if !reflect.DeepEqual(route, tt.want) {
					t.Errorf("parse() = %+v, want %+v", route, tt.want)
				}
			})
		}
	})

	t.Run("handles non-matching routes", func(t *testing.T) {
		route, err := parse("/room/:id", "/user/123")

		if err == nil {
			t.Error("expected error for non-matching path")
		}
		if route != nil {
			t.Error("expected nil route for non-matching path")
		}
		expectedErr := "route /room/:id does not match path /user/123"
		if err.Error() != expectedErr {
			t.Errorf("expected error message '%s', got '%s'", expectedErr, err.Error())
		}
	})
}

func TestSplitPath(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{"/", []string{}},
		{"/room", []string{"room"}},
		{"/room/123", []string{"room", "123"}},
		{"/room/123/", []string{"room", "123"}},
		{"room/123/", []string{"room", "123"}},
		{"", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := splitPath(tt.path)

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("splitPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}
