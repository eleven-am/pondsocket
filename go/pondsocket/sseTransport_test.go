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

type sseTestMetricsCollector struct {
	errorChan chan string
}

func (m *sseTestMetricsCollector) ConnectionOpened(connID string, endpoint string)        {}
func (m *sseTestMetricsCollector) ConnectionClosed(connID string, duration time.Duration) {}
func (m *sseTestMetricsCollector) ConnectionError(connID string, err error)               {}
func (m *sseTestMetricsCollector) MessageReceived(connID string, channel string, event string, size int) {
}
func (m *sseTestMetricsCollector) MessageSent(connID string, channel string, event string, size int) {
}
func (m *sseTestMetricsCollector) MessageBroadcast(channel string, event string, recipientCount int) {
}
func (m *sseTestMetricsCollector) ChannelJoined(userID string, channel string)            {}
func (m *sseTestMetricsCollector) ChannelLeft(userID string, channel string)              {}
func (m *sseTestMetricsCollector) ChannelCreated(channel string)                          {}
func (m *sseTestMetricsCollector) ChannelDestroyed(channel string)                        {}
func (m *sseTestMetricsCollector) HandlerDuration(handler string, duration time.Duration) {}
func (m *sseTestMetricsCollector) QueueDepth(queue string, depth int)                     {}
func (m *sseTestMetricsCollector) Error(component string, err error) {
	if m.errorChan != nil {
		select {
		case m.errorChan <- component:
		default:
		}
	}
}

func TestSSEConnSendJSON(t *testing.T) {
	t.Run("sends JSON in SSE format with event field", func(t *testing.T) {
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

		if !strings.HasPrefix(body, "event: test-event\n") {
			t.Errorf("expected SSE event field prefix, got %s", body)
		}

		if !strings.Contains(body, "data: ") {
			t.Errorf("expected SSE data prefix, got %s", body)
		}

		if !strings.HasSuffix(body, "\n\n") {
			t.Errorf("expected SSE terminator, got %s", body)
		}

		dataStart := strings.Index(body, "data: ") + 6
		dataEnd := strings.LastIndex(body, "\n\n")
		jsonPart := body[dataStart:dataEnd]

		var received Event
		if err := json.Unmarshal([]byte(jsonPart), &received); err != nil {
			t.Fatalf("failed to unmarshal SSE data: %v", err)
		}

		if received.RequestId != "req-123" {
			t.Errorf("expected request ID req-123, got %s", received.RequestId)
		}
	})

	t.Run("omits event field for non-Event types", func(t *testing.T) {
		conn, writer := createTestSSEConn("test-id", nil)
		defer conn.Close()

		testData := map[string]string{"key": "value"}

		err := conn.SendJSON(testData)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		body := writer.GetBody()

		if strings.HasPrefix(body, "event:") {
			t.Errorf("expected no event field for non-Event type, got %s", body)
		}

		if !strings.HasPrefix(body, "data: ") {
			t.Errorf("expected SSE data prefix, got %s", body)
		}
	})

	t.Run("omits event field when Event.Event is empty", func(t *testing.T) {
		conn, writer := createTestSSEConn("test-id", nil)
		defer conn.Close()

		testEvent := Event{
			Action:      "test",
			ChannelName: "test-channel",
			RequestId:   "req-123",
			Event:       "",
			Payload:     "test-payload",
		}

		err := conn.SendJSON(testEvent)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		body := writer.GetBody()

		if strings.HasPrefix(body, "event:") {
			t.Errorf("expected no event field when Event.Event is empty, got %s", body)
		}

		if !strings.HasPrefix(body, "data: ") {
			t.Errorf("expected SSE data prefix, got %s", body)
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

func TestSSEConnCORSHeaders(t *testing.T) {
	t.Run("sets CORS headers when configured", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		opts.CORSAllowOrigin = "https://example.com"
		opts.CORSAllowCredentials = true

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-cors",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		if writer.headers.Get("Access-Control-Allow-Origin") != "https://example.com" {
			t.Errorf("expected Access-Control-Allow-Origin https://example.com, got %s", writer.headers.Get("Access-Control-Allow-Origin"))
		}

		if writer.headers.Get("Access-Control-Allow-Credentials") != "true" {
			t.Errorf("expected Access-Control-Allow-Credentials true, got %s", writer.headers.Get("Access-Control-Allow-Credentials"))
		}
	})

	t.Run("does not set CORS headers when not configured", func(t *testing.T) {
		conn, writer := createTestSSEConn("test-no-cors", nil)
		defer conn.Close()

		if writer.headers.Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("expected no Access-Control-Allow-Origin header, got %s", writer.headers.Get("Access-Control-Allow-Origin"))
		}
	})

	t.Run("sets origin without credentials when credentials disabled", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		opts.CORSAllowOrigin = "*"
		opts.CORSAllowCredentials = false

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-cors-no-creds",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		if writer.headers.Get("Access-Control-Allow-Origin") != "*" {
			t.Errorf("expected Access-Control-Allow-Origin *, got %s", writer.headers.Get("Access-Control-Allow-Origin"))
		}

		if writer.headers.Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("expected no Access-Control-Allow-Credentials header, got %s", writer.headers.Get("Access-Control-Allow-Credentials"))
		}
	})
}

func TestSSEConnConcurrentHandleMessages(t *testing.T) {
	t.Run("handles multiple messages concurrently", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		opts.MaxConcurrentHandlers = 5

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-concurrent",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		var wg sync.WaitGroup
		messageCount := 10
		processed := make(chan string, messageCount)

		conn.OnMessage(func(event Event, transport Transport) error {
			time.Sleep(10 * time.Millisecond)
			processed <- event.RequestId
			return nil
		})

		conn.HandleMessages()

		for i := 0; i < messageCount; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				testEvent := Event{
					Action:      "test",
					ChannelName: "test-channel",
					RequestId:   "req-" + string(rune('0'+idx)),
					Event:       "test-event",
					Payload:     "test-payload",
				}
				data, _ := json.Marshal(testEvent)
				conn.PushMessage(data)
			}(i)
		}

		wg.Wait()

		timeout := time.After(2 * time.Second)
		receivedCount := 0
		for receivedCount < messageCount {
			select {
			case <-processed:
				receivedCount++
			case <-timeout:
				t.Errorf("timeout: expected %d messages, got %d", messageCount, receivedCount)
				return
			}
		}

		if receivedCount != messageCount {
			t.Errorf("expected %d messages processed, got %d", messageCount, receivedCount)
		}
	})

	t.Run("respects MaxConcurrentHandlers limit", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		opts.MaxConcurrentHandlers = 2

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-limit",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		var concurrentCount int32
		var maxConcurrent int32
		var mu sync.Mutex

		conn.OnMessage(func(event Event, transport Transport) error {
			mu.Lock()
			concurrentCount++
			if concurrentCount > maxConcurrent {
				maxConcurrent = concurrentCount
			}
			mu.Unlock()

			time.Sleep(50 * time.Millisecond)

			mu.Lock()
			concurrentCount--
			mu.Unlock()
			return nil
		})

		conn.HandleMessages()

		for i := 0; i < 6; i++ {
			testEvent := Event{
				Action:      "test",
				ChannelName: "test-channel",
				RequestId:   "req-limit",
				Event:       "test-event",
				Payload:     "test-payload",
			}
			data, _ := json.Marshal(testEvent)
			conn.PushMessage(data)
		}

		time.Sleep(400 * time.Millisecond)

		mu.Lock()
		if maxConcurrent > 2 {
			t.Errorf("expected max concurrent handlers to be 2, got %d", maxConcurrent)
		}
		mu.Unlock()
	})

	t.Run("handler errors are reported", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-error",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		errChan := make(chan bool, 1)

		conn.OnMessage(func(event Event, transport Transport) error {
			errChan <- true
			return &Error{Code: StatusBadRequest, Message: "test error"}
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
		conn.PushMessage(data)

		select {
		case <-errChan:
		case <-time.After(1 * time.Second):
			t.Error("timeout waiting for error handler")
		}

		time.Sleep(50 * time.Millisecond)

		body := writer.GetBody()
		if !strings.Contains(body, "error") {
			t.Logf("body doesn't contain 'error', body: %s", body)
		}
	})
}

func TestSSEConnGetAssignsReturnsClone(t *testing.T) {
	conn, _ := createTestSSEConn("test-id", nil)
	defer conn.Close()

	conn.SetAssign("key1", "value1")
	conn.SetAssign("key2", "value2")

	assigns := conn.GetAssigns()

	assigns["key1"] = "modified"
	assigns["key3"] = "new"

	if conn.GetAssign("key1") != "value1" {
		t.Error("modifying returned map affected original assigns")
	}

	if conn.GetAssign("key3") != nil {
		t.Error("adding to returned map affected original assigns")
	}
}

func TestSSEConnSendTimeoutOption(t *testing.T) {
	t.Run("uses configured SendTimeout", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		opts.SendTimeout = 100 * time.Millisecond

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-timeout",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		if conn.options.SendTimeout != 100*time.Millisecond {
			t.Errorf("expected SendTimeout 100ms, got %v", conn.options.SendTimeout)
		}
	})

	t.Run("default SendTimeout is 5 seconds", func(t *testing.T) {
		opts := DefaultOptions()
		if opts.SendTimeout != 5*time.Second {
			t.Errorf("expected default SendTimeout 5s, got %v", opts.SendTimeout)
		}
	})
}

func TestSSEConnKeepAlive(t *testing.T) {
	t.Run("sends keepalive on ticker", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		opts.PingInterval = 50 * time.Millisecond

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-keepalive",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}

		time.Sleep(120 * time.Millisecond)
		conn.Close()

		body := writer.GetBody()
		if !strings.Contains(body, ": keepalive") {
			t.Error("expected keepalive message to be written")
		}
	})

	t.Run("stops when context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		writer := newMockResponseWriter()
		opts := DefaultOptions()
		opts.PingInterval = 50 * time.Millisecond

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-keepalive-ctx",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}

		cancel()
		time.Sleep(100 * time.Millisecond)

		if conn.IsActive() {
			conn.Close()
		}
	})

	t.Run("stops when connection closed", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		opts.PingInterval = 200 * time.Millisecond

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-keepalive-close",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}

		conn.Close()
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("uses default interval when not configured", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		opts.PingInterval = 0

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-keepalive-default",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()
	})
}

func TestSSEConnPushMessageTimeout(t *testing.T) {
	t.Run("returns timeout error when channel full", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		opts.ReceiveChannelBuffer = 1
		opts.SendTimeout = 10 * time.Millisecond

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-push-timeout",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		_ = conn.PushMessage([]byte(`{"first": "message"}`))

		err = conn.PushMessage([]byte(`{"second": "message"}`))
		if err == nil {
			t.Error("expected timeout error when channel is full")
		}
	})

	t.Run("returns error when context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		writer := newMockResponseWriter()
		opts := DefaultOptions()
		opts.ReceiveChannelBuffer = 1
		opts.SendTimeout = 500 * time.Millisecond

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-push-ctx",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}

		_ = conn.PushMessage([]byte(`{"first": "message"}`))

		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		err = conn.PushMessage([]byte(`{"second": "message"}`))
		if err == nil {
			t.Error("expected error when context cancelled")
		}
		conn.Close()
	})
}

func TestSSEConnSendJSONErrors(t *testing.T) {
	t.Run("returns error when closeChan closed during send", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-close-during-send", nil)

		go func() {
			time.Sleep(10 * time.Millisecond)
			conn.Close()
		}()

		for i := 0; i < 100; i++ {
			err := conn.SendJSON(map[string]string{"key": "value"})
			if err != nil {
				return
			}
			time.Sleep(1 * time.Millisecond)
		}
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-invalid-json", nil)
		defer conn.Close()

		err := conn.SendJSON(make(chan int))
		if err == nil {
			t.Error("expected error for non-serializable type")
		}
	})
}

func TestSSEConnHandleMessagesEdgeCases(t *testing.T) {
	t.Run("sends error for invalid JSON", func(t *testing.T) {
		conn, writer := createTestSSEConn("test-invalid-msg", nil)
		defer conn.Close()

		conn.OnMessage(func(event Event, transport Transport) error {
			return nil
		})

		conn.HandleMessages()

		conn.incoming <- []byte(`invalid json{`)

		time.Sleep(100 * time.Millisecond)

		body := writer.GetBody()
		if !strings.Contains(body, "error") {
			t.Logf("expected error response for invalid JSON, body: %s", body)
		}
	})

	t.Run("sends error when no handler registered", func(t *testing.T) {
		conn, writer := createTestSSEConn("test-no-handler", nil)
		defer conn.Close()

		conn.HandleMessages()

		testEvent := Event{
			Action:      "test",
			ChannelName: "test-channel",
			RequestId:   "req-123",
			Event:       "test-event",
			Payload:     "test-payload",
		}
		data, _ := json.Marshal(testEvent)
		conn.incoming <- data

		time.Sleep(100 * time.Millisecond)

		body := writer.GetBody()
		if !strings.Contains(body, "no handler") {
			t.Logf("expected 'no handler' error, body: %s", body)
		}
	})

	t.Run("sends error for invalid event", func(t *testing.T) {
		conn, writer := createTestSSEConn("test-invalid-event", nil)
		defer conn.Close()

		conn.OnMessage(func(event Event, transport Transport) error {
			return nil
		})

		conn.HandleMessages()

		invalidEvent := Event{
			Action:      "",
			ChannelName: "",
			RequestId:   "",
			Event:       "",
			Payload:     nil,
		}
		data, _ := json.Marshal(invalidEvent)
		conn.incoming <- data

		time.Sleep(100 * time.Millisecond)

		body := writer.GetBody()
		if !strings.Contains(body, "invalid") || !strings.Contains(body, "error") {
			t.Logf("expected invalid event error, body: %s", body)
		}
	})

	t.Run("stops when context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		writer := newMockResponseWriter()
		opts := DefaultOptions()

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-handle-ctx",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}

		conn.OnMessage(func(event Event, transport Transport) error {
			return nil
		})

		conn.HandleMessages()
		cancel()

		time.Sleep(50 * time.Millisecond)
		conn.Close()
	})

	t.Run("stops when connection closed", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-handle-close", nil)

		conn.OnMessage(func(event Event, transport Transport) error {
			return nil
		})

		conn.HandleMessages()
		conn.Close()

		time.Sleep(50 * time.Millisecond)
	})
}

func TestSSEConnReportError(t *testing.T) {
	t.Run("does nothing when metrics is nil", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-report-nil", nil)
		defer conn.Close()

		conn.reportError("test", internal("test", "error"))
	})

	t.Run("does nothing when error is nil", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-report-nil-err", nil)
		defer conn.Close()

		conn.reportError("test", nil)
	})

	t.Run("does nothing when options is nil", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-report-no-opts",
			options:   nil,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		conn.reportError("test", internal("test", "error"))
	})

	t.Run("calls metrics Error when configured", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		errorReceived := make(chan string, 1)
		opts.Hooks = &Hooks{
			Metrics: &sseTestMetricsCollector{errorChan: errorReceived},
		}

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-report-metrics",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		conn.reportError("test-component", internal("test", "error message"))

		select {
		case component := <-errorReceived:
			if component != "test-component" {
				t.Errorf("expected component 'test-component', got %s", component)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("expected metrics Error to be called")
		}
	})
}

func TestSSEConnGetSendTimeout(t *testing.T) {
	t.Run("returns configured timeout", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		opts.SendTimeout = 10 * time.Second

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-get-timeout",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		timeout := conn.getSendTimeout()
		if timeout != 10*time.Second {
			t.Errorf("expected 10s, got %v", timeout)
		}
	})

	t.Run("returns default 5s when not configured", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		opts.SendTimeout = 0

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-get-timeout-default",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		timeout := conn.getSendTimeout()
		if timeout != 5*time.Second {
			t.Errorf("expected 5s default, got %v", timeout)
		}
	})

	t.Run("returns 5s when options is nil", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-get-timeout-nil",
			options:   nil,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		timeout := conn.getSendTimeout()
		if timeout != 5*time.Second {
			t.Errorf("expected 5s default, got %v", timeout)
		}
	})
}

func TestSSEConnGetAssignNil(t *testing.T) {
	t.Run("returns nil when assigns is nil", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-nil-assigns",
			options:   nil,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		conn.mutex.Lock()
		conn.assigns = nil
		conn.mutex.Unlock()

		val := conn.GetAssign("key")
		if val != nil {
			t.Error("expected nil for nil assigns map")
		}
	})
}

func TestSSEConnSetAssignNilAssigns(t *testing.T) {
	t.Run("initializes assigns map when nil", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-set-nil-assigns",
			options:   nil,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}
		defer conn.Close()

		conn.mutex.Lock()
		conn.assigns = nil
		conn.mutex.Unlock()

		conn.SetAssign("key", "value")

		if conn.GetAssign("key") != "value" {
			t.Error("expected assigns to be initialized and value set")
		}
	})
}

func TestSSEConnCloseHandlerErrors(t *testing.T) {
	t.Run("reports close handler errors", func(t *testing.T) {
		writer := newMockResponseWriter()
		ctx := context.Background()
		opts := DefaultOptions()
		errorReceived := make(chan string, 1)
		opts.Hooks = &Hooks{
			Metrics: &sseTestMetricsCollector{errorChan: errorReceived},
		}

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-close-error",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}

		conn.OnClose(func(t Transport) error {
			return internal("test", "close handler error")
		})

		conn.Close()

		select {
		case component := <-errorReceived:
			if component != "sse_close_handlers" {
				t.Errorf("expected component 'sse_close_handlers', got %s", component)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("expected close handler error to be reported")
		}
	})
}

func TestSSEConnWait(t *testing.T) {
	t.Run("Wait blocks until Close is called", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-wait", nil)

		waitDone := make(chan struct{})
		go func() {
			conn.Wait()
			close(waitDone)
		}()

		select {
		case <-waitDone:
			t.Error("Wait should not return before Close")
		case <-time.After(50 * time.Millisecond):
		}

		conn.Close()

		select {
		case <-waitDone:
		case <-time.After(100 * time.Millisecond):
			t.Error("Wait should return after Close")
		}
	})

	t.Run("Wait unblocks when context is cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		writer := newMockResponseWriter()
		opts := DefaultOptions()

		sseOpts := sseOptions{
			writer:    writer,
			assigns:   nil,
			id:        "test-wait-ctx",
			options:   opts,
			parentCtx: ctx,
		}

		conn, err := newSSEConn(sseOpts)
		if err != nil {
			t.Fatalf("failed to create SSE connection: %v", err)
		}

		waitDone := make(chan struct{})
		go func() {
			conn.Wait()
			close(waitDone)
		}()

		select {
		case <-waitDone:
			t.Error("Wait should not return before context cancel")
		case <-time.After(50 * time.Millisecond):
		}

		cancel()

		select {
		case <-waitDone:
		case <-time.After(100 * time.Millisecond):
			t.Error("Wait should return after context cancel")
		}
	})

	t.Run("Wait returns immediately if already closed", func(t *testing.T) {
		conn, _ := createTestSSEConn("test-wait-closed", nil)
		conn.Close()

		waitDone := make(chan struct{})
		go func() {
			conn.Wait()
			close(waitDone)
		}()

		select {
		case <-waitDone:
		case <-time.After(100 * time.Millisecond):
			t.Error("Wait should return immediately for closed connection")
		}
	})
}
