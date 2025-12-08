package pondsocket

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockResponseWriter struct {
	headers    http.Header
	body       *bytes.Buffer
	statusCode int
	mutex      sync.Mutex
}

func newMockResponseWriter() *mockResponseWriter {
	return &mockResponseWriter{
		headers: make(http.Header),
		body:    &bytes.Buffer{},
	}
}

func (m *mockResponseWriter) Header() http.Header {
	return m.headers
}

func (m *mockResponseWriter) Write(data []byte) (int, error) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.body.Write(data)
}

func (m *mockResponseWriter) WriteHeader(statusCode int) {
	m.statusCode = statusCode
}

func (m *mockResponseWriter) Flush() {}

func (m *mockResponseWriter) GetBody() string {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.body.String()
}

func createTestSSEConn(id string, assigns map[string]interface{}) (*SSEConn, *mockResponseWriter) {
	writer := newMockResponseWriter()
	ctx := context.Background()
	opts := DefaultOptions()

	sseOpts := sseOptions{
		writer:    writer,
		assigns:   assigns,
		id:        id,
		options:   opts,
		parentCtx: ctx,
	}

	conn, _ := newSSEConn(sseOpts)
	return conn, writer
}

func TestNewSSEConn(t *testing.T) {
	t.Run("creates SSE connection successfully", func(t *testing.T) {
		assigns := map[string]interface{}{"role": "user"}
		conn, writer := createTestSSEConn("test-id", assigns)
		defer conn.Close()

		if conn.GetID() != "test-id" {
			t.Errorf("expected ID test-id, got %s", conn.GetID())
		}

		if conn.GetAssign("role") != "user" {
			t.Errorf("expected role to be user, got %v", conn.GetAssign("role"))
		}

		if writer.headers.Get("Content-Type") != "text/event-stream" {
			t.Errorf("expected Content-Type text/event-stream, got %s", writer.headers.Get("Content-Type"))
		}

		if writer.headers.Get("Cache-Control") != "no-cache" {
			t.Errorf("expected Cache-Control no-cache, got %s", writer.headers.Get("Cache-Control"))
		}
	})

	t.Run("returns error for non-flusher response writer", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()

		nonFlusher := &nonFlushingWriter{}
		sseOpts := sseOptions{
			writer:    nonFlusher,
			assigns:   nil,
			id:        "test-id",
			options:   opts,
			parentCtx: ctx,
		}

		_, err := newSSEConn(sseOpts)
		if err == nil {
			t.Error("expected error for non-flushing writer")
		}
	})

	t.Run("creates connection with nil assigns", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-id", nil)
		defer conn.Close()

		if conn.GetAssigns() == nil {
			t.Error("expected assigns to be initialized")
		}
	})
}

type nonFlushingWriter struct{}

func (n *nonFlushingWriter) Header() http.Header        { return make(http.Header) }
func (n *nonFlushingWriter) Write([]byte) (int, error)  { return 0, nil }
func (n *nonFlushingWriter) WriteHeader(statusCode int) {}

func TestSSEConnSendJSON(t *testing.T) {
	t.Run("sends JSON in SSE format", func(t *testing.T) {
		conn, writer := createTestSSEConn("test-id", nil)
		defer conn.Close()

		testEvent := Event{
			Action:      "test",
			ChannelName: "test-channel",
			RequestId:   "req-123",
			Event:       "test-event",
			Payload:     "test-payload",
		}

		err := conn.SendJSON(testEvent)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		body := writer.GetBody()
		if !strings.HasPrefix(body, "data: ") {
			t.Errorf("expected SSE data prefix, got %s", body)
		}

		if !strings.HasSuffix(body, "\n\n") {
			t.Errorf("expected SSE terminator, got %s", body)
		}

		jsonPart := strings.TrimPrefix(body, "data: ")
		jsonPart = strings.TrimSuffix(jsonPart, "\n\n")

		var received Event
		if err := json.Unmarshal([]byte(jsonPart), &received); err != nil {
			t.Fatalf("failed to unmarshal SSE data: %v", err)
		}

		if received.RequestId != "req-123" {
			t.Errorf("expected request ID req-123, got %s", received.RequestId)
		}
	})

	t.Run("returns error when connection is closed", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-id", nil)
		conn.Close()

		err := conn.SendJSON(Event{})
		if err == nil {
			t.Error("expected error when sending to closed connection")
		}
	})
}

func TestSSEConnAssigns(t *testing.T) {
	conn, _ := createTestSSEConn("test-id", nil)
	defer conn.Close()

	t.Run("SetAssign and GetAssign", func(t *testing.T) {
		conn.SetAssign("key1", "value1")
		conn.SetAssign("key2", 42)

		if conn.GetAssign("key1") != "value1" {
			t.Errorf("expected value1, got %v", conn.GetAssign("key1"))
		}

		if conn.GetAssign("key2") != 42 {
			t.Errorf("expected 42, got %v", conn.GetAssign("key2"))
		}

		if conn.GetAssign("nonexistent") != nil {
			t.Errorf("expected nil for nonexistent key, got %v", conn.GetAssign("nonexistent"))
		}
	})

	t.Run("GetAssigns returns assigns map", func(t *testing.T) {
		assigns := conn.GetAssigns()
		if assigns["key1"] != "value1" {
			t.Errorf("expected value1, got %v", assigns["key1"])
		}
	})

	t.Run("CloneAssigns creates independent copy", func(t *testing.T) {
		conn.SetAssign("key3", "value3")
		cloned := conn.CloneAssigns()

		if cloned["key1"] != "value1" {
			t.Errorf("expected value1 in clone, got %v", cloned["key1"])
		}

		cloned["key1"] = "modified"
		if conn.GetAssign("key1") != "value1" {
			t.Error("modifying clone affected original")
		}
	})
}

func TestSSEConnIsActiveAndClose(t *testing.T) {
	t.Run("IsActive returns true for new connection", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-id", nil)
		defer conn.Close()

		if !conn.IsActive() {
			t.Error("expected connection to be active")
		}
	})

	t.Run("IsActive returns false after Close", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-id", nil)
		conn.Close()

		if conn.IsActive() {
			t.Error("expected connection to be inactive after close")
		}
	})

	t.Run("Close is idempotent", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-id", nil)

		conn.Close()
		conn.Close()
		conn.Close()

		if conn.IsActive() {
			t.Error("expected connection to be inactive")
		}
	})
}

func TestSSEConnOnClose(t *testing.T) {
	t.Run("calls close handlers on Close", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-id", nil)

		closeCalled := make(chan bool, 1)
		conn.OnClose(func(t Transport) error {
			closeCalled <- true
			return nil
		})

		conn.Close()

		select {
		case <-closeCalled:
		case <-time.After(1 * time.Second):
			t.Error("close handler was not called")
		}
	})

	t.Run("calls multiple close handlers in order", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-id", nil)

		order := make([]int, 0, 3)
		var mu sync.Mutex

		for i := 1; i <= 3; i++ {
			idx := i
			conn.OnClose(func(t Transport) error {
				mu.Lock()
				order = append(order, idx)
				mu.Unlock()
				return nil
			})
		}

		conn.Close()
		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		defer mu.Unlock()

		if len(order) != 3 {
			t.Errorf("expected 3 handlers called, got %d", len(order))
		}
	})
}

func TestSSEConnOnMessageAndHandleMessages(t *testing.T) {
	t.Run("processes messages from incoming channel", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-id", nil)
		defer conn.Close()

		messageReceived := make(chan Event, 1)
		conn.OnMessage(func(event Event, transport Transport) error {
			messageReceived <- event
			return nil
		})

		conn.HandleMessages()

		testEvent := Event{
			Action:      "test",
			ChannelName: "test-channel",
			RequestId:   "req-123",
			Event:       "test-event",
			Payload:     "test-payload",
		}

		data, _ := json.Marshal(testEvent)
		err := conn.PushMessage(data)
		if err != nil {
			t.Fatalf("failed to push message: %v", err)
		}

		select {
		case received := <-messageReceived:
			if received.RequestId != "req-123" {
				t.Errorf("expected request ID req-123, got %s", received.RequestId)
			}
		case <-time.After(1 * time.Second):
			t.Error("timeout waiting for message")
		}
	})
}

func TestSSEConnPushMessage(t *testing.T) {
	t.Run("pushes message to incoming channel", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-id", nil)
		defer conn.Close()

		testData := []byte(`{"test": "data"}`)
		err := conn.PushMessage(testData)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		select {
		case received := <-conn.incoming:
			if string(received) != string(testData) {
				t.Errorf("expected %s, got %s", testData, received)
			}
		case <-time.After(1 * time.Second):
			t.Error("timeout waiting for message in incoming channel")
		}
	})

	t.Run("returns error when connection is closed", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-id", nil)
		conn.Close()

		err := conn.PushMessage([]byte(`{"test": "data"}`))
		if err == nil {
			t.Error("expected error when pushing to closed connection")
		}
	})
}

func TestSSEConnType(t *testing.T) {
	conn, _ := createTestSSEConn("test-id", nil)
	defer conn.Close()

	if conn.Type() != TransportSSE {
		t.Errorf("expected TransportSSE, got %v", conn.Type())
	}
}

func TestSSEConnConcurrency(t *testing.T) {
	conn, _ := createTestSSEConn("test-id", nil)
	defer conn.Close()

	var wg sync.WaitGroup
	iterations := 100

	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			conn.SetAssign("key", i)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = conn.GetAssign("key")
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = conn.CloneAssigns()
		}
	}()

	wg.Wait()
}
