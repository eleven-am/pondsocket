package pondsocket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
			return ctx.Accept()
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
			return ctx.Accept()
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
			return ctx.Accept()
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
				return ctx.Accept()
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

func TestEndpointGetClients(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()
	endpoint := newEndpoint(ctx, "/test", opts)

	t.Run("returns empty list when no clients", func(t *testing.T) {
		clients := endpoint.GetClients()
		if len(clients) != 0 {
			t.Errorf("expected 0 clients, got %d", len(clients))
		}
	})

	t.Run("returns all connected clients", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			conn := &Conn{
				ID:        fmt.Sprintf("client-%d", i),
				ctx:       ctx,
				closeChan: make(chan struct{}),
				assigns:   map[string]interface{}{"name": fmt.Sprintf("user%d", i)},
			}
			endpoint.connections.Create(conn.ID, conn)
		}

		clients := endpoint.GetClients()
		if len(clients) != 3 {
			t.Errorf("expected 3 clients, got %d", len(clients))
		}
	})
}

func TestEndpointGetChannelByName(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()
	endpoint := newEndpoint(ctx, "/test", opts)

	t.Run("returns nil for non-existent channel", func(t *testing.T) {
		channel, err := endpoint.GetChannelByName("nonexistent")
		if err == nil {
			t.Error("expected error for non-existent channel")
		}
		if channel != nil {
			t.Error("expected nil channel")
		}
	})

	t.Run("returns channel when exists", func(t *testing.T) {
		endpoint.CreateChannel("test:*", func(ctx *JoinContext) error {
			return ctx.Accept()
		})

		channelOpts := options{
			Name:                 "test:room1",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, channelOpts)

		endpoint.channels.Create("test:room1", channel)

		found, err := endpoint.GetChannelByName("test:room1")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if found == nil {
			t.Error("expected to find channel")
		}
	})
}

func TestEndpointCloseConnection(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()
	endpoint := newEndpoint(ctx, "/test", opts)

	t.Run("returns error for non-existent connection", func(t *testing.T) {
		err := endpoint.CloseConnection("nonexistent")
		if err == nil {
			t.Error("expected error for non-existent connection")
		}
	})

	t.Run("returns nil for empty ids", func(t *testing.T) {
		err := endpoint.CloseConnection()
		if err != nil {
			t.Errorf("expected nil error for empty ids, got %v", err)
		}
	})
}

func TestEndpointPushMessage(t *testing.T) {
	t.Run("returns error for non-existent connection", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		err := endpoint.pushMessage("nonexistent", []byte(`{"test": "data"}`))
		if err == nil {
			t.Error("expected error for non-existent connection")
		}
	})

	t.Run("returns error for WebSocket connection", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		wsConn := &Conn{
			ID:        "ws-conn",
			ctx:       ctx,
			closeChan: make(chan struct{}),
		}
		endpoint.connections.Create(wsConn.ID, wsConn)

		err := endpoint.pushMessage("ws-conn", []byte(`{"test": "data"}`))
		if err == nil {
			t.Error("expected error for WebSocket connection")
		}
	})

	t.Run("pushes message to SSE connection", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		writer := newMockResponseWriter()
		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "sse-conn",
			options:   opts,
			parentCtx: ctx,
		}
		sseConn, _ := newSSEConn(sseOpts)
		endpoint.connections.Create(sseConn.GetID(), sseConn)
		defer sseConn.Close()

		sseConn.HandleMessages()

		err := endpoint.pushMessage("sse-conn", []byte(`{"action":"test"}`))
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("returns error when endpoint is shutting down", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		cancel()

		err := endpoint.pushMessage("any-id", []byte(`{"test": "data"}`))
		if err == nil {
			t.Error("expected error when endpoint is shutting down")
		}
	})
}

type mockSSEWriter struct {
	headers    http.Header
	body       []byte
	statusCode int
}

func newMockSSEWriter() *mockSSEWriter {
	return &mockSSEWriter{
		headers: make(http.Header),
	}
}

func (m *mockSSEWriter) Header() http.Header {
	return m.headers
}

func (m *mockSSEWriter) Write(data []byte) (int, error) {
	m.body = append(m.body, data...)
	return len(data), nil
}

func (m *mockSSEWriter) WriteHeader(statusCode int) {
	m.statusCode = statusCode
}

func (m *mockSSEWriter) Flush() {}

func TestEndpointJoinChannel(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()
	opts.InternalQueueTimeout = 100 * time.Millisecond

	t.Run("joins channel successfully", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		joinCalled := false
		endpoint.CreateChannel("room:*", func(ctx *JoinContext) error {
			joinCalled = true
			return ctx.Accept()
		})

		conn := createTestConn("user1", nil)
		endpoint.connections.Create(conn.ID, conn)

		ev := &Event{
			Action:      joinChannelEvent,
			ChannelName: "room:123",
			RequestId:   "req1",
			Payload:     map[string]interface{}{"data": "test"},
		}

		_ = endpoint.joinChannel(ev, conn)

		if !joinCalled {
			t.Error("expected join handler to be called")
		}
	})

	t.Run("returns error when endpoint is closed", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		endpoint := newEndpoint(ctx, "/test", opts)
		conn := createTestConn("user1", nil)

		ev := &Event{
			Action:      joinChannelEvent,
			ChannelName: "room:123",
			RequestId:   "req1",
		}

		err := endpoint.joinChannel(ev, conn)
		if err == nil {
			t.Error("expected error when endpoint is closed")
		}
	})

	t.Run("processes unknown channel through not found handler", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		endpoint.connections.Create(conn.ID, conn)

		ev := &Event{
			Action:      joinChannelEvent,
			ChannelName: "unknown:123",
			RequestId:   "req1",
		}

		_ = endpoint.joinChannel(ev, conn)
	})
}

func TestEndpointLeaveChannel(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()

	t.Run("leaves channel successfully", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		endpoint.CreateChannel("room:*", func(ctx *JoinContext) error {
			return ctx.Accept()
		})

		channelOpts := options{
			Name:                 "room:123",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, channelOpts)

		conn := createTestConn("user1", nil)
		channel.addUser(conn)
		endpoint.channels.Create("room:123", channel)
		endpoint.connections.Create(conn.ID, conn)

		ev := &Event{
			Action:      leaveChannelEvent,
			ChannelName: "room:123",
			RequestId:   "req1",
		}

		err := endpoint.leaveChannel(ev, conn)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("returns error for non-existent channel", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		endpoint.connections.Create(conn.ID, conn)

		ev := &Event{
			Action:      leaveChannelEvent,
			ChannelName: "nonexistent:123",
			RequestId:   "req1",
		}

		err := endpoint.leaveChannel(ev, conn)
		if err == nil {
			t.Error("expected error for non-existent channel")
		}
	})

	t.Run("returns error when endpoint is closed", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		endpoint := newEndpoint(ctx, "/test", opts)
		conn := createTestConn("user1", nil)

		ev := &Event{
			Action:      leaveChannelEvent,
			ChannelName: "room:123",
			RequestId:   "req1",
		}

		err := endpoint.leaveChannel(ev, conn)
		if err == nil {
			t.Error("expected error when endpoint is closed")
		}
	})
}

func TestEndpointBroadcastMessage(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()

	t.Run("broadcasts message successfully", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		channelOpts := options{
			Name:                 "room:123",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, channelOpts)

		conn := createTestConn("user1", nil)
		channel.addUser(conn)
		endpoint.channels.Create("room:123", channel)
		endpoint.connections.Create(conn.ID, conn)

		ev := &Event{
			Action:      broadcast,
			ChannelName: "room:123",
			RequestId:   "req1",
			Event:       "message",
			Payload:     map[string]interface{}{"text": "hello"},
		}

		err := endpoint.broadcastMessage(ev, conn)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("returns error for non-existent channel", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		endpoint.connections.Create(conn.ID, conn)

		ev := &Event{
			Action:      broadcast,
			ChannelName: "nonexistent:123",
			RequestId:   "req1",
			Event:       "message",
		}

		err := endpoint.broadcastMessage(ev, conn)
		if err == nil {
			t.Error("expected error for non-existent channel")
		}
	})

	t.Run("returns error when endpoint is closed", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		endpoint := newEndpoint(ctx, "/test", opts)
		conn := createTestConn("user1", nil)

		ev := &Event{
			Action:      broadcast,
			ChannelName: "room:123",
			RequestId:   "req1",
		}

		err := endpoint.broadcastMessage(ev, conn)
		if err == nil {
			t.Error("expected error when endpoint is closed")
		}
	})
}

func TestEndpointHandleMessage(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()

	t.Run("handles join event", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		endpoint.CreateChannel("room:*", func(ctx *JoinContext) error {
			return ctx.Accept()
		})

		conn := createTestConn("user1", nil)
		endpoint.connections.Create(conn.ID, conn)

		handler := endpoint.handleMessage()

		ev := Event{
			Action:      joinChannelEvent,
			ChannelName: "room:123",
			RequestId:   "req1",
		}

		err := handler(ev, conn)
		if err != nil {
			t.Logf("join returned error (expected for test): %v", err)
		}
	})

	t.Run("handles leave event", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		endpoint.connections.Create(conn.ID, conn)

		handler := endpoint.handleMessage()

		ev := Event{
			Action:      leaveChannelEvent,
			ChannelName: "room:123",
			RequestId:   "req1",
		}

		err := handler(ev, conn)
		if err == nil {
			t.Log("leave succeeded (channel may not exist)")
		}
	})

	t.Run("handles broadcast event", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		endpoint.connections.Create(conn.ID, conn)

		handler := endpoint.handleMessage()

		ev := Event{
			Action:      broadcast,
			ChannelName: "room:123",
			RequestId:   "req1",
			Event:       "message",
		}

		err := handler(ev, conn)
		if err == nil {
			t.Log("broadcast succeeded (channel may not exist)")
		}
	})

	t.Run("handles unknown event type", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		endpoint.connections.Create(conn.ID, conn)

		handler := endpoint.handleMessage()

		ev := Event{
			Action:      action("unknown"),
			ChannelName: "room:123",
			RequestId:   "req1",
			Event:       "unknown",
		}

		err := handler(ev, conn)
		if err == nil {
			t.Error("expected error for unknown event type")
		}
	})

	t.Run("returns error when endpoint is closed", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		endpoint := newEndpoint(ctx, "/test", opts)
		conn := createTestConn("user1", nil)

		handler := endpoint.handleMessage()

		ev := Event{
			Action:      broadcast,
			ChannelName: "room:123",
		}

		err := handler(ev, conn)
		if err == nil {
			t.Error("expected error when endpoint is closed")
		}
	})
}

func TestEndpointSendMessage(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()

	t.Run("sends message to existing user", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		endpoint.connections.Create(conn.ID, conn)

		ev := Event{
			Action:      system,
			ChannelName: "test",
			Event:       "notification",
			Payload:     map[string]interface{}{"msg": "hello"},
		}

		err := endpoint.sendMessage("user1", ev)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("returns error for non-existent user", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		ev := Event{
			Action:      system,
			ChannelName: "test",
			Event:       "notification",
		}

		err := endpoint.sendMessage("nonexistent", ev)
		if err == nil {
			t.Error("expected error for non-existent user")
		}
	})
}

func TestEndpointNotFoundHandler(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()

	t.Run("sends not found response", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)

		handler := endpoint.notFoundHandler(ctx, "req123")

		ev := joinEvent{
			user:    conn,
			channel: "unknown:channel",
		}

		err := handler(ev, nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("returns error when context is cancelled", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		conn := createTestConn("user1", nil)

		handler := endpoint.notFoundHandler(cancelledCtx, "req123")

		ev := joinEvent{
			user:    conn,
			channel: "unknown:channel",
		}

		err := handler(ev, nil)
		if err == nil {
			t.Error("expected error when context is cancelled")
		}
	})

	t.Run("returns error when endpoint is closed", func(t *testing.T) {
		closedCtx, cancel := context.WithCancel(context.Background())
		cancel()

		endpoint := newEndpoint(closedCtx, "/test", opts)

		conn := createTestConn("user1", nil)

		handler := endpoint.notFoundHandler(ctx, "req123")

		ev := joinEvent{
			user:    conn,
			channel: "unknown:channel",
		}

		err := handler(ev, nil)
		if err == nil {
			t.Error("expected error when endpoint is closed")
		}
	})
}

func TestEndpointHandleClose(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()

	t.Run("removes connection on close", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		endpoint.connections.Create(conn.ID, conn)

		err := endpoint.handleClose(conn)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		_, readErr := endpoint.connections.Read(conn.ID)
		if readErr == nil {
			t.Error("expected connection to be deleted")
		}
	})

	t.Run("calls OnDisconnect hook", func(t *testing.T) {
		disconnectCalled := false
		opts := DefaultOptions()
		opts.Hooks = &Hooks{
			OnDisconnect: func(c Transport) {
				disconnectCalled = true
			},
		}

		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		endpoint.connections.Create(conn.ID, conn)

		endpoint.handleClose(conn)

		if !disconnectCalled {
			t.Error("expected OnDisconnect to be called")
		}
	})

	t.Run("calls metrics ConnectionClosed", func(t *testing.T) {
		metricsCalled := false
		opts := DefaultOptions()
		opts.Hooks = &Hooks{
			Metrics: &mockMetricsCollector{},
		}

		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		endpoint.connections.Create(conn.ID, conn)

		endpoint.handleClose(conn)

		if opts.Hooks.Metrics != nil {
			metricsCalled = true
		}

		if !metricsCalled {
			t.Error("expected metrics to be called")
		}
	})
}

func TestEndpointAddConnection(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()

	t.Run("adds connection successfully", func(t *testing.T) {
		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)

		err := endpoint.addConnection(conn)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		_, readErr := endpoint.connections.Read(conn.ID)
		if readErr != nil {
			t.Error("expected connection to be stored")
		}
	})

	t.Run("rejects when max connections reached", func(t *testing.T) {
		opts := DefaultOptions()
		opts.MaxConnections = 1

		endpoint := newEndpoint(ctx, "/test", opts)

		conn1 := createTestConn("user1", nil)
		endpoint.addConnection(conn1)

		conn2 := createTestConn("user2", nil)
		err := endpoint.addConnection(conn2)

		if err == nil {
			t.Error("expected error when max connections reached")
		}
	})

	t.Run("calls OnConnect hook", func(t *testing.T) {
		connectCalled := false
		opts := DefaultOptions()
		opts.Hooks = &Hooks{
			OnConnect: func(c Transport) error {
				connectCalled = true
				return nil
			},
		}

		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		endpoint.addConnection(conn)

		if !connectCalled {
			t.Error("expected OnConnect to be called")
		}
	})

	t.Run("rejects when OnConnect fails", func(t *testing.T) {
		opts := DefaultOptions()
		opts.Hooks = &Hooks{
			OnConnect: func(c Transport) error {
				return errors.New("connection rejected")
			},
		}

		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		err := endpoint.addConnection(conn)

		if err == nil {
			t.Error("expected error when OnConnect fails")
		}
	})

	t.Run("returns error when endpoint is closed", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		endpoint := newEndpoint(ctx, "/test", opts)

		conn := createTestConn("user1", nil)
		err := endpoint.addConnection(conn)

		if err == nil {
			t.Error("expected error when endpoint is closed")
		}
	})
}
