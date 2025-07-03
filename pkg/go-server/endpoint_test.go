package pondsocket

import (
	"context"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEndpointCreation(t *testing.T) {
	ctx := context.Background()

	opts := DefaultOptions()

	t.Run("creates endpoint successfully", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		if endpoint.connections == nil {
			t.Error("expected connections store to be initialized")
		}
		if endpoint.channels == nil {
			t.Error("expected channels store to be initialized")
		}
	})

	t.Run("respects MaxConnections option", func(t *testing.T) {
		opts := DefaultOptions()

		opts.MaxConnections = 10
		endpoint := newEndpoint(ctx, "/test", opts)

		if endpoint.options.MaxConnections != 10 {
			t.Errorf("expected MaxConnections 10, got %d", endpoint.options.MaxConnections)
		}
	})
}

func TestEndpointHandleConnection(t *testing.T) {
	t.Run("accepts valid WebSocket connections", func(t *testing.T) {
		ctx := context.Background()

		opts := DefaultOptions()

		endpoint := newEndpoint(ctx, "/test", opts)

		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := upgrader.Upgrade(w, r, nil)

			if err != nil {
				t.Fatalf("Failed to upgrade: %v", err)

				return
			}
			defer conn.Close()

			assigns := map[string]interface{}{"user": "test"}
			wsConn, err := newConn(ctx, conn, assigns, "test-id", opts)

			if err != nil {
				t.Errorf("Failed to create connection: %v", err)

				return
			}
			err = endpoint.connections.Create(wsConn.ID, wsConn)

			if err != nil {
				t.Errorf("Failed to store connection: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
		}))

		defer server.Close()

		wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)

		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer conn.Close()

		time.Sleep(50 * time.Millisecond)

		if endpoint.connections.Len() != 1 {
			t.Errorf("expected 1 connection, got %d", endpoint.connections.Len())
		}
	})

	t.Run("enforces MaxConnections limit", func(t *testing.T) {
		ctx := context.Background()

		opts := DefaultOptions()

		opts.MaxConnections = 2
		endpoint := newEndpoint(ctx, "/test", opts)

		for i := 0; i < 2; i++ {
			conn := &Conn{
				ID:        fmt.Sprintf("Conn-%d", i),
				ctx:       ctx,
				closeChan: make(chan struct{}),
			}
			endpoint.connections.Create(conn.ID, conn)
		}
		results := make(chan error, 1)

		go func() {
			if endpoint.connections.Len() >= endpoint.options.MaxConnections {
				results <- fmt.Errorf("max connections reached")
			} else {
				results <- nil
			}
		}()

		err := <-results
		if err == nil {
			t.Error("expected error when exceeding MaxConnections")
		}
	})
}

func TestEndpointChannelCreation(t *testing.T) {
	ctx := context.Background()

	opts := DefaultOptions()

	endpoint := newEndpoint(ctx, "/test", opts)

	t.Run("can create channels", func(t *testing.T) {
		lobby := endpoint.CreateChannel("room:*", func(ctx *JoinContext) error {
			ctx.Accept()

			return ctx.Err()
		})

		if lobby == nil {
			t.Fatal("expected lobby to be returned")
		}
		_, err := endpoint.channels.Read("room:123")

		if err == nil {
			t.Error("expected channel not to exist before join")
		}
	})

	t.Run("channel handlers work", func(t *testing.T) {
		lobby := endpoint.CreateChannel("test:*", func(ctx *JoinContext) error {
			ctx.Accept()

			return ctx.Err()
		})

		lobby.OnMessage("test:event", func(ctx *EventContext) error {
			return nil
		})
	})
}

func TestEndpointShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	opts := DefaultOptions()

	endpoint := newEndpoint(ctx, "/test", opts)

	t.Run("shuts down when context cancelled", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			conn := &Conn{
				ID:        fmt.Sprintf("Conn-%d", i),
				ctx:       ctx,
				closeChan: make(chan struct{}),
				cancel:    func() {},
			}
			endpoint.connections.Create(conn.ID, conn)
		}
		endpoint.CreateChannel("test:*", func(ctx *JoinContext) error {
			ctx.Accept()

			return ctx.Err()
		})

		cancel()

		time.Sleep(50 * time.Millisecond)

		select {
		case <-ctx.Done():
		default:
			t.Error("expected context to be done")
		}
	})
}

func TestEndpointConcurrentAccess(t *testing.T) {
	ctx := context.Background()

	opts := DefaultOptions()

	endpoint := newEndpoint(ctx, "/test", opts)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			conn := &Conn{
				ID:        fmt.Sprintf("Conn-%d", n),
				ctx:       ctx,
				closeChan: make(chan struct{}),
			}
			endpoint.connections.Create(conn.ID, conn)
		}(i)
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			endpoint.CreateChannel(fmt.Sprintf("channel-%d:*", n), func(ctx *JoinContext) error {
				ctx.Accept()

				return ctx.Err()
			})
		}(i)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_ = endpoint.connections.Len()
		}()
	}
	wg.Wait()

	if endpoint.connections.Len() == 0 {
		t.Error("expected some connections to be stored")
	}
}
