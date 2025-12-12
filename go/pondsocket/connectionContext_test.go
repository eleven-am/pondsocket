package pondsocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

func TestIsWebSocketRequest(t *testing.T) {
	t.Run("returns true for WebSocket upgrade request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ws", nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")

		if !isWebSocketRequest(req) {
			t.Error("expected true for WebSocket request")
		}
	})

	t.Run("returns true for case-insensitive header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ws", nil)
		req.Header.Set("Upgrade", "WebSocket")

		if !isWebSocketRequest(req) {
			t.Error("expected true for case-insensitive WebSocket header")
		}
	})

	t.Run("returns false for SSE request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/sse", nil)
		req.Header.Set("Accept", "text/event-stream")

		if isWebSocketRequest(req) {
			t.Error("expected false for SSE request")
		}
	})

	t.Run("returns false for regular HTTP request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api", nil)

		if isWebSocketRequest(req) {
			t.Error("expected false for regular HTTP request")
		}
	})
}

func TestIsSSERequest(t *testing.T) {
	t.Run("returns true for SSE accept header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/sse", nil)
		req.Header.Set("Accept", "text/event-stream")

		if !isSSERequest(req) {
			t.Error("expected true for SSE request")
		}
	})

	t.Run("returns true when text/event-stream is part of Accept header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/sse", nil)
		req.Header.Set("Accept", "text/event-stream, application/json")

		if !isSSERequest(req) {
			t.Error("expected true when text/event-stream is included")
		}
	})

	t.Run("returns false for WebSocket request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ws", nil)
		req.Header.Set("Upgrade", "websocket")

		if isSSERequest(req) {
			t.Error("expected false for WebSocket request")
		}
	})

	t.Run("returns false for regular HTTP request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api", nil)
		req.Header.Set("Accept", "application/json")

		if isSSERequest(req) {
			t.Error("expected false for regular HTTP request")
		}
	})
}

func TestConnectionContextAcceptSSE(t *testing.T) {
	t.Run("accepts SSE connection successfully", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept", "text/event-stream")

		var writer http.ResponseWriter = recorder

		connOpts := connectionOptions{
			request:  req,
			response: &writer,
			endpoint: endpoint,
			userId:   "test-sse-id",
			upgrader: websocket.Upgrader{},
			route:    &Route{},
			connCtx:  ctx,
		}
		connCtx := newConnectionContext(connOpts)

		err := connCtx.Accept()
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if recorder.Header().Get("Content-Type") != "text/event-stream" {
			t.Errorf("expected Content-Type text/event-stream, got %s", recorder.Header().Get("Content-Type"))
		}

		if recorder.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("expected Cache-Control no-cache, got %s", recorder.Header().Get("Cache-Control"))
		}

		conn, err := endpoint.connections.Read("test-sse-id")
		if err != nil {
			t.Errorf("expected connection to be stored, got error: %v", err)
		}

		if conn.Type() != TransportSSE {
			t.Errorf("expected SSE transport, got %v", conn.Type())
		}

		conn.Close()
	})

	t.Run("returns error for unsupported transport", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)

		var writer http.ResponseWriter = recorder

		connOpts := connectionOptions{
			request:  req,
			response: &writer,
			endpoint: endpoint,
			userId:   "test-id",
			upgrader: websocket.Upgrader{},
			route:    &Route{},
			connCtx:  ctx,
		}
		connCtx := newConnectionContext(connOpts)

		err := connCtx.Accept()
		if err == nil {
			t.Error("expected error for unsupported transport")
		}
	})

	t.Run("returns error when already responded", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept", "text/event-stream")

		var writer http.ResponseWriter = recorder

		connOpts := connectionOptions{
			request:  req,
			response: &writer,
			endpoint: endpoint,
			userId:   "test-id",
			upgrader: websocket.Upgrader{},
			route:    &Route{},
			connCtx:  ctx,
		}
		connCtx := newConnectionContext(connOpts)
		connCtx.hasResponded = true

		err := connCtx.Accept()
		if err == nil {
			t.Error("expected error when already responded")
		}
	})
}

func TestConnectionContextDecline(t *testing.T) {
	t.Run("declines connection with status code", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)

		var writer http.ResponseWriter = recorder

		connOpts := connectionOptions{
			request:  req,
			response: &writer,
			endpoint: endpoint,
			userId:   "test-id",
			upgrader: websocket.Upgrader{},
			route:    &Route{},
			connCtx:  ctx,
		}
		connCtx := newConnectionContext(connOpts)

		err := connCtx.Decline(http.StatusUnauthorized, "Unauthorized")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if recorder.Code != http.StatusUnauthorized {
			t.Errorf("expected status 401, got %d", recorder.Code)
		}
	})
}

func TestConnectionContextSetAndGetAssigns(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()
	endpoint := newEndpoint(ctx, "/test", opts)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)

	var writer http.ResponseWriter = recorder

	connOpts := connectionOptions{
		request:  req,
		response: &writer,
		endpoint: endpoint,
		userId:   "test-id",
		upgrader: websocket.Upgrader{},
		route:    &Route{},
		connCtx:  ctx,
	}
	connCtx := newConnectionContext(connOpts)

	t.Run("sets and gets assigns", func(t *testing.T) {
		connCtx.SetAssigns("role", "admin")
		connCtx.SetAssigns("userId", 123)

		if connCtx.GetAssign("role") != "admin" {
			t.Errorf("expected role admin, got %v", connCtx.GetAssign("role"))
		}

		if connCtx.GetAssign("userId") != 123 {
			t.Errorf("expected userId 123, got %v", connCtx.GetAssign("userId"))
		}
	})

	t.Run("returns nil for non-existent key", func(t *testing.T) {
		if connCtx.GetAssign("nonexistent") != nil {
			t.Error("expected nil for non-existent key")
		}
	})
}

func TestConnectionContextGetUser(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()
	endpoint := newEndpoint(ctx, "/test", opts)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)

	var writer http.ResponseWriter = recorder

	connOpts := connectionOptions{
		request:  req,
		response: &writer,
		endpoint: endpoint,
		userId:   "test-user-123",
		upgrader: websocket.Upgrader{},
		route:    &Route{},
		connCtx:  ctx,
	}
	connCtx := newConnectionContext(connOpts)
	connCtx.SetAssigns("role", "user")

	user := connCtx.GetUser()

	if user.UserID != "test-user-123" {
		t.Errorf("expected userId test-user-123, got %s", user.UserID)
	}

	if user.Assigns["role"] != "user" {
		t.Errorf("expected role user, got %v", user.Assigns["role"])
	}

	if user.Presence != nil {
		t.Error("expected nil presence")
	}
}

func TestConnectionContextHeaders(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()
	endpoint := newEndpoint(ctx, "/test", opts)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer token123")
	req.Header.Set("X-Custom-Header", "custom-value")

	var writer http.ResponseWriter = recorder

	connOpts := connectionOptions{
		request:  req,
		response: &writer,
		endpoint: endpoint,
		userId:   "test-id",
		upgrader: websocket.Upgrader{},
		route:    &Route{},
		connCtx:  ctx,
	}
	connCtx := newConnectionContext(connOpts)

	headers := connCtx.Headers()

	if headers.Get("Authorization") != "Bearer token123" {
		t.Errorf("expected Authorization header, got %s", headers.Get("Authorization"))
	}

	if headers.Get("X-Custom-Header") != "custom-value" {
		t.Errorf("expected X-Custom-Header, got %s", headers.Get("X-Custom-Header"))
	}
}

func TestConnectionContextContext(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()
	endpoint := newEndpoint(ctx, "/test", opts)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)

	var writer http.ResponseWriter = recorder

	connOpts := connectionOptions{
		request:  req,
		response: &writer,
		endpoint: endpoint,
		userId:   "test-id",
		upgrader: websocket.Upgrader{},
		route:    &Route{},
		connCtx:  ctx,
	}
	connCtx := newConnectionContext(connOpts)

	if connCtx.Context() == nil {
		t.Error("expected non-nil context")
	}
}

func TestConnectionContextReply(t *testing.T) {
	t.Run("replies successfully after accept", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Accept", "text/event-stream")

		var writer http.ResponseWriter = recorder

		connOpts := connectionOptions{
			request:  req,
			response: &writer,
			endpoint: endpoint,
			userId:   "test-reply-id",
			upgrader: websocket.Upgrader{},
			route:    &Route{},
			connCtx:  ctx,
		}
		connCtx := newConnectionContext(connOpts)

		err := connCtx.Reply("welcome", map[string]interface{}{"message": "hello"})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		conn, _ := endpoint.connections.Read("test-reply-id")
		if conn != nil {
			conn.Close()
		}
	})

	t.Run("returns error when accept fails", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)

		var writer http.ResponseWriter = recorder

		connOpts := connectionOptions{
			request:  req,
			response: &writer,
			endpoint: endpoint,
			userId:   "test-id",
			upgrader: websocket.Upgrader{},
			route:    &Route{},
			connCtx:  ctx,
		}
		connCtx := newConnectionContext(connOpts)

		err := connCtx.Reply("welcome", nil)

		if err == nil {
			t.Error("expected error when accept fails")
		}
	})
}

func TestConnectionContextParseAssigns(t *testing.T) {
	type testAssigns struct {
		Role   string `mapstructure:"role"`
		UserID int    `mapstructure:"userId"`
	}

	t.Run("parses assigns successfully", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)

		var writer http.ResponseWriter = recorder

		connOpts := connectionOptions{
			request:  req,
			response: &writer,
			endpoint: endpoint,
			userId:   "test-id",
			upgrader: websocket.Upgrader{},
			route:    &Route{},
			connCtx:  ctx,
		}
		connCtx := newConnectionContext(connOpts)
		connCtx.SetAssigns("role", "admin")
		connCtx.SetAssigns("userId", 123)

		var result testAssigns
		err := connCtx.ParseAssigns(&result)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if result.Role != "admin" {
			t.Errorf("expected role admin, got %s", result.Role)
		}
		if result.UserID != 123 {
			t.Errorf("expected userId 123, got %d", result.UserID)
		}
	})

	t.Run("handles nil assigns", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)

		var writer http.ResponseWriter = recorder

		connOpts := connectionOptions{
			request:  req,
			response: &writer,
			endpoint: endpoint,
			userId:   "test-id",
			upgrader: websocket.Upgrader{},
			route:    &Route{},
			connCtx:  ctx,
		}
		connCtx := newConnectionContext(connOpts)

		var result testAssigns
		err := connCtx.ParseAssigns(&result)

		if err != nil {
			t.Errorf("expected no error for nil assigns, got %v", err)
		}
	})

	t.Run("returns nil for nil assigns map", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)

		var writer http.ResponseWriter = recorder

		connOpts := connectionOptions{
			request:  req,
			response: &writer,
			endpoint: endpoint,
			userId:   "test-id",
			upgrader: websocket.Upgrader{},
			route:    &Route{},
			connCtx:  ctx,
		}
		connCtx := newConnectionContext(connOpts)

		val := connCtx.GetAssign("anything")
		if val != nil {
			t.Error("expected nil for unset assigns")
		}
	})
}
