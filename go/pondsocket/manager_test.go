package pondsocket

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
	t.Run("allows all origins only with explicit opt out", func(t *testing.T) {
		ctx := context.Background()

		opts := Options{
			AllowAnyOrigin: true,
		}
		mgr := NewManager(ctx, opts)

		req := httptest.NewRequest("GET", "/ws", nil)

		req.Header.Set("Origin", "http://localhost:3000")

		if !mgr.upgrader.CheckOrigin(req) {
			t.Error("expected origin check to pass when AllowAnyOrigin is true")
		}
	})

	t.Run("defaults to same-origin and permits non-browser clients", func(t *testing.T) {
		mgr := NewManager(context.Background())
		req := httptest.NewRequest("GET", "https://socket.example/ws", nil)
		req.Host = "socket.example"
		req.Header.Set("Origin", "https://socket.example")
		if !mgr.upgrader.CheckOrigin(req) {
			t.Error("expected same-origin browser request to pass")
		}
		req.Header.Set("Origin", "https://evil.example")
		if mgr.upgrader.CheckOrigin(req) {
			t.Error("expected cross-origin browser request to fail")
		}
		req.Header.Del("Origin")
		if !mgr.upgrader.CheckOrigin(req) {
			t.Error("expected client without Origin header to pass")
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

	t.Run("CheckOriginFunc receives the request and can compare against its host", func(t *testing.T) {
		ctx := context.Background()

		opts := Options{
			CheckOrigin: true,
			CheckOriginFunc: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				return origin == "https://"+r.Host
			},
		}
		mgr := NewManager(ctx, opts)

		req := httptest.NewRequest("GET", "/ws", nil)
		req.Host = "tenant-a.example.com"
		req.Header.Set("Origin", "https://tenant-a.example.com")

		if !mgr.upgrader.CheckOrigin(req) {
			t.Error("expected origin matching the request host to pass check")
		}

		req.Header.Set("Origin", "https://tenant-b.example.com")

		if mgr.upgrader.CheckOrigin(req) {
			t.Error("expected origin not matching the request host to fail check")
		}
	})

	t.Run("CheckOriginFunc takes precedence over AllowedOrigins", func(t *testing.T) {
		ctx := context.Background()

		opts := Options{
			CheckOrigin:    true,
			AllowedOrigins: []string{"http://localhost:3000"},
			CheckOriginFunc: func(r *http.Request) bool {
				return false
			},
		}
		mgr := NewManager(ctx, opts)

		req := httptest.NewRequest("GET", "/ws", nil)
		req.Header.Set("Origin", "http://localhost:3000")

		if mgr.upgrader.CheckOrigin(req) {
			t.Error("expected CheckOriginFunc to override the AllowedOrigins list")
		}
	})
}

func TestManagerSSECORSPreflight(t *testing.T) {
	opts := *DefaultOptions()
	opts.CORSAllowOrigin = "https://app.example"
	opts.CORSAllowCredentials = true
	mgr := NewManager(context.Background(), opts)
	mgr.CreateEndpoint("/events", func(ctx *ConnectionContext) error { return ctx.Accept() })

	req := httptest.NewRequest(http.MethodOptions, "/events", nil)
	response := httptest.NewRecorder()
	mgr.HTTPHandler().ServeHTTP(response, req)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if got := response.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Fatalf("allow origin = %q", got)
	}
	if got := response.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-Connection-ID") {
		t.Fatalf("allow headers = %q", got)
	}
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

func TestManagerGetEndpoints(t *testing.T) {
	t.Run("returns empty slice when no endpoints", func(t *testing.T) {
		ctx := context.Background()
		mgr := NewManager(ctx)

		endpoints := mgr.GetEndpoints()

		if endpoints == nil {
			t.Error("expected non-nil slice")
		}
		if len(endpoints) != 0 {
			t.Errorf("expected 0 endpoints, got %d", len(endpoints))
		}
	})

	t.Run("returns all created endpoints", func(t *testing.T) {
		ctx := context.Background()
		mgr := NewManager(ctx)

		mgr.CreateEndpoint("/ws1", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})
		mgr.CreateEndpoint("/ws2", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})
		mgr.CreateEndpoint("/ws3", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		endpoints := mgr.GetEndpoints()

		if len(endpoints) != 3 {
			t.Errorf("expected 3 endpoints, got %d", len(endpoints))
		}
	})
}

func TestManagerSSEConnection(t *testing.T) {
	t.Run("accepts SSE connections", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		mgr := NewManager(ctx)

		connectionAccepted := make(chan string, 1)

		mgr.CreateEndpoint("/events", func(ctx *ConnectionContext) error {
			err := ctx.Accept()
			if err == nil {
				connectionAccepted <- ctx.userId
			}
			return err
		})

		handler := mgr.HTTPHandler()
		server := httptest.NewServer(handler)
		defer server.Close()

		reqCtx, reqCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer reqCancel()

		req, _ := http.NewRequestWithContext(reqCtx, "GET", server.URL+"/events", nil)
		req.Header.Set("Accept", "text/event-stream")

		client := &http.Client{}

		respChan := make(chan *http.Response, 1)
		errChan := make(chan error, 1)

		go func() {
			resp, err := client.Do(req)
			if err != nil {
				errChan <- err
				return
			}
			respChan <- resp
		}()

		select {
		case <-connectionAccepted:
		case <-time.After(200 * time.Millisecond):
			t.Error("timeout waiting for connection to be accepted")
		}

		select {
		case resp := <-respChan:
			defer resp.Body.Close()
			if resp.Header.Get("Content-Type") != "text/event-stream" {
				t.Errorf("expected Content-Type text/event-stream, got %s", resp.Header.Get("Content-Type"))
			}
		case err := <-errChan:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("unexpected error: %v", err)
			}
		case <-time.After(600 * time.Millisecond):
		}

		cancel()
	})
}

func TestManagerSSEMessageHandler(t *testing.T) {
	t.Run("returns error when X-Connection-ID header is missing", func(t *testing.T) {
		ctx := context.Background()
		mgr := NewManager(ctx)

		mgr.CreateEndpoint("/events", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		handler := mgr.HTTPHandler()
		server := httptest.NewServer(handler)
		defer server.Close()

		req, _ := http.NewRequest("POST", server.URL+"/events", strings.NewReader(`{"action":"test"}`))
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", resp.StatusCode)
		}
	})

	t.Run("returns error when connection not found", func(t *testing.T) {
		ctx := context.Background()
		mgr := NewManager(ctx)

		mgr.CreateEndpoint("/events", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		handler := mgr.HTTPHandler()
		server := httptest.NewServer(handler)
		defer server.Close()

		req, _ := http.NewRequest("POST", server.URL+"/events", strings.NewReader(`{"action":"test"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Connection-ID", "nonexistent-id")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
	})

	t.Run("rejects request body larger than configured message size", func(t *testing.T) {
		ctx := context.Background()
		opts := *DefaultOptions()
		opts.MaxMessageSize = 8
		mgr := NewManager(ctx, opts)

		mgr.CreateEndpoint("/events", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		handler := mgr.HTTPHandler()
		req := httptest.NewRequest(http.MethodPost, "/events", strings.NewReader("123456789"))
		req.Header.Set("X-Connection-ID", "any-id")
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected status 413, got %d", recorder.Code)
		}
	})
}

func TestManagerSSERequestCancellationClosesConnection(t *testing.T) {
	managerCtx, managerCancel := context.WithCancel(context.Background())
	defer managerCancel()
	opts := *DefaultOptions()
	opts.PingInterval = 10 * time.Millisecond
	mgr := NewManager(managerCtx, opts)
	endpoint := mgr.CreateEndpoint("/events", func(ctx *ConnectionContext) error {
		return ctx.Accept()
	})
	server := httptest.NewServer(mgr.HTTPHandler())
	defer server.Close()

	requestCtx, requestCancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, server.URL+"/events", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to establish SSE connection: %v", err)
	}
	if resp.Header.Get("X-Connection-ID") == "" {
		t.Fatal("SSE response did not expose a connection ID")
	}
	if endpoint.connections.Len() != 1 {
		t.Fatalf("expected one active SSE connection, got %d", endpoint.connections.Len())
	}

	requestCancel()
	_ = resp.Body.Close()

	deadline := time.Now().Add(time.Second)
	for endpoint.connections.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if endpoint.connections.Len() != 0 {
		t.Fatalf("cancelled SSE request retained %d connections", endpoint.connections.Len())
	}
}

func TestManagerSSEDisconnectHandler(t *testing.T) {
	t.Run("returns error when X-Connection-ID header is missing", func(t *testing.T) {
		ctx := context.Background()
		mgr := NewManager(ctx)

		mgr.CreateEndpoint("/events", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		handler := mgr.HTTPHandler()
		server := httptest.NewServer(handler)
		defer server.Close()

		req, _ := http.NewRequest("DELETE", server.URL+"/events", nil)

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected status 400, got %d", resp.StatusCode)
		}
	})

	t.Run("returns error when connection not found", func(t *testing.T) {
		ctx := context.Background()
		mgr := NewManager(ctx)

		mgr.CreateEndpoint("/events", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		handler := mgr.HTTPHandler()
		server := httptest.NewServer(handler)
		defer server.Close()

		req, _ := http.NewRequest("DELETE", server.URL+"/events", nil)
		req.Header.Set("X-Connection-ID", "nonexistent-id")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
	})
}
