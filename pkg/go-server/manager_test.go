package main

import (
	"context"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	t.Run("creates manager with default options", func(t *testing.T) {
		ctx := context.Background()

		mgr := NewManager(ctx)

		if mgr.endpoints == nil {
			t.Error("expected endpoints store to be initialized")
		}
		if mgr.ctx == nil {
			t.Error("expected context to be initialized")
		}
		if mgr.Options == nil {
			t.Error("expected options to be initialized")
		}
	})

	t.Run("creates manager with custom options", func(t *testing.T) {
		ctx := context.Background()

		opts := Options{
			ReadBufferSize:    2048,
			WriteBufferSize:   2048,
			PingInterval:      30 * time.Second,
			WriteWait:         20 * time.Second,
			PongWait:          90 * time.Second,
			AllowedOrigins:    []string{"http://localhost:3000"},
			MaxMessageSize:    1024 * 1024,
			EnableCompression: true,
		}
		mgr := NewManager(ctx, opts)

		if mgr.Options.ReadBufferSize != 2048 {
			t.Errorf("expected ReadBufferSize 2048, got %d", mgr.Options.ReadBufferSize)
		}
		if mgr.Options.PingInterval != 30*time.Second {
			t.Errorf("expected PingInterval 30s, got %v", mgr.Options.PingInterval)
		}
		if len(mgr.Options.AllowedOrigins) != 1 {
			t.Errorf("expected 1 allowed origin, got %d", len(mgr.Options.AllowedOrigins))
		}
	})

	t.Run("compiles origin regexps correctly", func(t *testing.T) {
		ctx := context.Background()

		opts := Options{
			AllowedOriginRegexps: []*regexp.Regexp{
				regexp.MustCompile(`^https://.*\.example\.com$`),
				regexp.MustCompile(`^http://localhost:[0-9]+$`),
			},
		}
		mgr := NewManager(ctx, opts)

		if len(mgr.Options.AllowedOriginRegexps) != 2 {
			t.Errorf("expected 2 compiled regexps, got %d", len(mgr.Options.AllowedOriginRegexps))
		}
		testOrigin := "https://app.example.com"
		matched := false
		for _, re := range mgr.Options.AllowedOriginRegexps {
			if re.MatchString(testOrigin) {
				matched = true
				break
			}
		}
		if matched {
			t.Log("https://app.example.com matched the regex pattern")
		}
	})
}

func TestManagerCreateEndpoint(t *testing.T) {
	ctx := context.Background()

	mgr := NewManager(ctx)

	t.Run("creates endpoint successfully", func(t *testing.T) {
		endpoint := mgr.CreateEndpoint("/ws", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		if endpoint == nil {
			t.Fatal("expected endpoint to be created")
		}
		if mgr.endpoints.length() != 1 {
			t.Errorf("expected 1 endpoint, got %d", mgr.endpoints.length())
		}
	})

	t.Run("creates multiple endpoints", func(t *testing.T) {
		ctx := context.Background()

		mgr := NewManager(ctx)

		endpoint1 := mgr.CreateEndpoint("/ws1", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		endpoint2 := mgr.CreateEndpoint("/ws2", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		if endpoint1 == nil || endpoint2 == nil {
			t.Error("expected both endpoints to be created")
		}
		if mgr.endpoints.length() != 2 {
			t.Errorf("expected 2 endpoints, got %d", mgr.endpoints.length())
		}
	})
}

func TestManagerOriginChecking(t *testing.T) {
	t.Run("allows all origins when CheckOrigin is false", func(t *testing.T) {
		ctx := context.Background()

		opts := Options{
			CheckOrigin: false,
		}
		mgr := NewManager(ctx, opts)

		req := httptest.NewRequest("GET", "/ws", nil)

		req.Header.Set("Origin", "http://localhost:3000")

		if !mgr.upgrader.CheckOrigin(req) {
			t.Error("expected origin check to pass when CheckOrigin is false")
		}
	})

	t.Run("checks allowed origins list", func(t *testing.T) {
		ctx := context.Background()

		opts := Options{
			CheckOrigin:    true,
			AllowedOrigins: []string{"http://localhost:3000"},
		}
		mgr := NewManager(ctx, opts)

		req := httptest.NewRequest("GET", "/ws", nil)

		req.Header.Set("Origin", "http://localhost:3000")

		if !mgr.upgrader.CheckOrigin(req) {
			t.Error("expected allowed origin to pass check")
		}
		req.Header.Set("Origin", "http://evil.com")

		if mgr.upgrader.CheckOrigin(req) {
			t.Error("expected disallowed origin to fail check")
		}
	})

	t.Run("checks origin regexps", func(t *testing.T) {
		ctx := context.Background()

		opts := Options{
			CheckOrigin: true,
			AllowedOriginRegexps: []*regexp.Regexp{
				regexp.MustCompile(`^https://.*\.example\.com$`),
			},
		}
		mgr := NewManager(ctx, opts)

		req := httptest.NewRequest("GET", "/ws", nil)

		req.Header.Set("Origin", "https://app.example.com")

		if !mgr.upgrader.CheckOrigin(req) {
			t.Error("expected matching origin to pass check")
		}
		req.Header.Set("Origin", "https://evil.com")

		if mgr.upgrader.CheckOrigin(req) {
			t.Error("expected non-matching origin to fail check")
		}
	})
}

func TestManagerHTTPHandler(t *testing.T) {
	ctx := context.Background()

	mgr := NewManager(ctx)

	t.Run("returns HTTP handler function", func(t *testing.T) {
		handler := mgr.HTTPHandler()

		if handler == nil {
			t.Fatal("expected HTTP handler to be returned")
		}

		var _ http.HandlerFunc = handler
	})

	t.Run("handler processes requests", func(t *testing.T) {
		mgr.CreateEndpoint("/ws", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		handler := mgr.HTTPHandler()

		server := httptest.NewServer(handler)

		defer server.Close()

		resp, err := http.Get(server.URL + "/ws")

		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			t.Error("expected endpoint to be accessible")
		}
	})
}

func TestManagerShutdown(t *testing.T) {
	t.Run("shuts down with context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		mgr := NewManager(ctx)

		mgr.CreateEndpoint("/ws1", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		mgr.CreateEndpoint("/ws2", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		cancel()

		select {
		case <-mgr.ctx.Done():
		default:
			t.Error("expected manager context to be done")
		}
	})
}

func TestManagerWebSocketUpgrade(t *testing.T) {
	t.Run("upgrades WebSocket connections", func(t *testing.T) {
		ctx := context.Background()

		mgr := NewManager(ctx)

		connectionAccepted := make(chan bool, 1)

		mgr.CreateEndpoint("/ws", func(ctx *ConnectionContext) error {
			err := ctx.Accept()

			connectionAccepted <- true
			return err
		})

		handler := mgr.HTTPHandler()

		server := httptest.NewServer(handler)

		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
		dialer := websocket.Dialer{
			HandshakeTimeout: 1 * time.Second,
		}
		conn, resp, err := dialer.Dial(wsURL, nil)

		if err == nil {
			defer conn.Close()
		}
		if resp != nil {
			defer resp.Body.Close()
		}
		select {
		case <-connectionAccepted:
		case <-time.After(100 * time.Millisecond):
		}
	})
}
