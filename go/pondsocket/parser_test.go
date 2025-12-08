package pondsocket

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
					query:  map[string][]string{},
					params: map[string]string{},
				},
			},
			{
				pattern: "/user/profile",
				path:    "/user/profile",
				want: &Route{
					query:  map[string][]string{},
					params: map[string]string{},
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
					query: map[string][]string{},
					params: map[string]string{
						"id": "123",
					},
				},
			},
			{
				pattern: "/user/:userId/profile",
				path:    "/user/john/profile",
				want: &Route{
					query: map[string][]string{},
					params: map[string]string{
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
					query: map[string][]string{},
					params: map[string]string{
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
					query:  map[string][]string{},
					params: map[string]string{},
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
					query: map[string][]string{
						"q":    {"test"},
						"sort": {"asc"},
					},
					params: map[string]string{},
				},
			},
			{
				pattern: "/search",
				path:    "/search?q=test&q=another",
				want: &Route{
					query: map[string][]string{
						"q": {"test", "another"},
					},
					params: map[string]string{},
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
				if !reflect.DeepEqual(route.query, tt.want.query) {
					t.Errorf("parse() query = %+v, want %+v", route.query, tt.want.query)
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
					query: map[string][]string{},
					params: map[string]string{
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

func TestRouteParam(t *testing.T) {
	route := &Route{
		params: map[string]string{
			"id":   "123",
			"name": "john",
		},
		query: map[string][]string{},
	}

	t.Run("returns param value when exists", func(t *testing.T) {
		val := route.Param("id")
		if val != "123" {
			t.Errorf("expected '123', got '%s'", val)
		}
	})

	t.Run("returns empty string when param doesn't exist", func(t *testing.T) {
		val := route.Param("nonexistent")
		if val != "" {
			t.Errorf("expected empty string, got '%s'", val)
		}
	})
}

func TestRouteQueryParam(t *testing.T) {
	route := &Route{
		params: map[string]string{},
		query: map[string][]string{
			"page":   {"1"},
			"filter": {"active", "pending"},
		},
	}

	t.Run("returns query values when exists", func(t *testing.T) {
		val := route.QueryParam("page")
		if len(val) != 1 || val[0] != "1" {
			t.Errorf("expected ['1'], got %v", val)
		}
	})

	t.Run("returns all values when multiple exist", func(t *testing.T) {
		val := route.QueryParam("filter")
		if len(val) != 2 || val[0] != "active" {
			t.Errorf("expected ['active', 'pending'], got %v", val)
		}
	})

	t.Run("returns empty slice when query doesn't exist", func(t *testing.T) {
		val := route.QueryParam("nonexistent")
		if len(val) != 0 {
			t.Errorf("expected empty slice, got %v", val)
		}
	})
}

func TestRouteParseQuery(t *testing.T) {
	route := &Route{
		params: map[string]string{},
		query: map[string][]string{
			"name": {"john"},
			"age":  {"25"},
		},
	}

	t.Run("parses query into struct", func(t *testing.T) {
		var result struct {
			Name []string `json:"name"`
			Age  []string `json:"age"`
		}

		err := route.ParseQuery(&result)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(result.Name) != 1 || result.Name[0] != "john" {
			t.Errorf("expected name ['john'], got %v", result.Name)
		}
		if len(result.Age) != 1 || result.Age[0] != "25" {
			t.Errorf("expected age ['25'], got %v", result.Age)
		}
	})
}

func TestRouteParseParams(t *testing.T) {
	route := &Route{
		params: map[string]string{
			"id":   "123",
			"type": "user",
		},
		query: map[string][]string{},
	}

	t.Run("parses params into struct", func(t *testing.T) {
		var result struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}

		err := route.ParseParams(&result)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result.ID != "123" {
			t.Errorf("expected ID '123', got '%s'", result.ID)
		}
		if result.Type != "user" {
			t.Errorf("expected Type 'user', got '%s'", result.Type)
		}
	})
}
