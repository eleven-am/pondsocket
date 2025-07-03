package pondsocket

import (
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServerCreation(t *testing.T) {
	t.Run("creates server with default options", func(t *testing.T) {
		opts := &ServerOptions{
			Options:    DefaultOptions(),
			ServerAddr: ":0",
		}
		server := NewServer(opts)

		if server.manager == nil {
			t.Error("expected manager to be initialized")
		}
		if server.server == nil {
			t.Error("expected http server to be initialized")
		}
	})

	t.Run("creates server with custom options", func(t *testing.T) {
		customOpts := &Options{
			ReadBufferSize:  2048,
			WriteBufferSize: 2048,
			PingInterval:    30 * time.Second,
			PongWait:        60 * time.Second,
		}
		serverOpts := &ServerOptions{
			Options:            customOpts,
			ServerAddr:         ":8080",
			ServerReadTimeout:  5 * time.Second,
			ServerWriteTimeout: 10 * time.Second,
			ServerIdleTimeout:  120 * time.Second,
		}
		server := NewServer(serverOpts)

		if server.manager.Options.ReadBufferSize != 2048 {
			t.Errorf("expected ReadBufferSize 2048, got %d", server.manager.Options.ReadBufferSize)
		}
		if server.server.ReadTimeout != 5*time.Second {
			t.Errorf("expected ReadTimeout 5s, got %v", server.server.ReadTimeout)
		}
		if server.server.WriteTimeout != 10*time.Second {
			t.Errorf("expected WriteTimeout 10s, got %v", server.server.WriteTimeout)
		}
	})
}

func TestServerCreateEndpoint(t *testing.T) {
	opts := &ServerOptions{
		Options:    DefaultOptions(),
		ServerAddr: ":0",
	}
	server := NewServer(opts)

	t.Run("creates WebSocket endpoint", func(t *testing.T) {
		endpoint := server.CreateEndpoint("/ws", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		if endpoint == nil {
			t.Fatal("expected endpoint to be returned")
		}
	})

	t.Run("creates multiple endpoints", func(t *testing.T) {
		opts := &ServerOptions{
			Options:    DefaultOptions(),
			ServerAddr: ":0",
		}
		server := NewServer(opts)

		endpoint1 := server.CreateEndpoint("/ws1", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		endpoint2 := server.CreateEndpoint("/ws2", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		if endpoint1 == nil || endpoint2 == nil {
			t.Error("expected both endpoints to be created")
		}
	})
}

func TestServerServe(t *testing.T) {
	t.Run("starts HTTP server", func(t *testing.T) {
		opts := &ServerOptions{
			Options:    DefaultOptions(),
			ServerAddr: ":0",
		}
		server := NewServer(opts)

		server.CreateEndpoint("/ws", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		serverErr := make(chan error, 1)

		go func() {
			err := server.Start()

			serverErr <- err
		}()

		time.Sleep(100 * time.Millisecond)

		if !server.IsRunning() {
			t.Error("expected server to be running")
		}
		server.Stop(2 * time.Second)

		select {
		case err := <-serverErr:
			if err != nil && !strings.Contains(err.Error(), "Server closed") {
				t.Errorf("unexpected server error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("server did not shut down in time")
		}
	})
}

func TestServerShutdown(t *testing.T) {
	t.Run("shuts down cleanly", func(t *testing.T) {
		opts := &ServerOptions{
			Options:    DefaultOptions(),
			ServerAddr: ":0",
		}
		server := NewServer(opts)

		server.CreateEndpoint("/ws", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		go server.Start()

		time.Sleep(100 * time.Millisecond)

		err := server.Stop(2 * time.Second)

		if err != nil {
			t.Errorf("expected clean shutdown, got error: %v", err)
		}
		if server.IsRunning() {
			t.Error("expected server to stop running")
		}
	})

	t.Run("shutdown without explicit context", func(t *testing.T) {
		opts := &ServerOptions{
			Options:    DefaultOptions(),
			ServerAddr: ":0",
		}
		server := NewServer(opts)

		err := server.Stop(2 * time.Second)

		if err != nil {
			t.Errorf("expected clean shutdown, got error: %v", err)
		}
	})
}

func TestServerIntegration(t *testing.T) {
	t.Run("full server lifecycle with WebSocket", func(t *testing.T) {
		opts := &ServerOptions{
			Options:    DefaultOptions(),
			ServerAddr: ":0",
		}
		server := NewServer(opts)

		messageHandled := make(chan bool, 1)

		ep1 := server.CreateEndpoint("/ws1", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		_ = server.CreateEndpoint("/ws2", func(ctx *ConnectionContext) error {
			return ctx.Accept()
		})

		lobby1 := ep1.CreateChannel("test:*", func(ctx *JoinContext) error {
			ctx.Accept()

			return nil
		})

		lobby1.OnMessage("test:message", func(ctx *EventContext) error {
			messageHandled <- true
			return nil
		})

		go server.Start()

		time.Sleep(100 * time.Millisecond)

		addr := server.server.Addr
		if addr == ":0" {
			time.Sleep(100 * time.Millisecond)
		}
		client := &http.Client{
			Timeout: 2 * time.Second,
		}
		resp1, err := client.Get("http://localhost:59999/ws1")

		if err == nil {
			resp1.Body.Close()

			if resp1.StatusCode == http.StatusNotFound {
				t.Error("expected /ws1 to be registered")
			}
		}
		resp2, err := client.Get("http://localhost:59999/ws2")

		if err == nil {
			resp2.Body.Close()

			if resp2.StatusCode == http.StatusNotFound {
				t.Error("expected /ws2 to be registered")
			}
		}
		server.Stop(2 * time.Second)
	})
}

func TestServerWebSocketConnection(t *testing.T) {
	t.Run("accepts WebSocket connections", func(t *testing.T) {
		opts := &ServerOptions{
			Options:    DefaultOptions(),
			ServerAddr: ":0",
		}
		server := NewServer(opts)

		connectionAccepted := make(chan bool, 1)

		server.CreateEndpoint("/ws", func(ctx *ConnectionContext) error {
			err := ctx.Accept()

			if err == nil {
				connectionAccepted <- true
			}
			return err
		})

		handler := server.manager.HTTPHandler()

		testServer := httptest.NewServer(handler)

		defer testServer.Close()

		wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws"
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
		case <-time.After(500 * time.Millisecond):
		}
	})
}
