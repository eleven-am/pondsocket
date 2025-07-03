package pondsocket

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mockRateLimiter struct {
	allowFunc func(ctx context.Context, key string) (bool, error)
	resetFunc func(key string)
	calls     []string
}

func (m *mockRateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	m.calls = append(m.calls, "Allow:"+key)
	if m.allowFunc != nil {
		return m.allowFunc(ctx, key)
	}
	return true, nil
}

func (m *mockRateLimiter) Reset(key string) {
	m.calls = append(m.calls, "Reset:"+key)
	if m.resetFunc != nil {
		m.resetFunc(key)
	}
}

type mockMetricsCollector struct {
	calls []string
}

func (m *mockMetricsCollector) ConnectionOpened(connID string, endpoint string) {
	m.calls = append(m.calls, "ConnectionOpened:"+connID+":"+endpoint)
}

func (m *mockMetricsCollector) ConnectionClosed(connID string, duration time.Duration) {
	m.calls = append(m.calls, "ConnectionClosed:"+connID)
}

func (m *mockMetricsCollector) ConnectionError(connID string, err error) {
	m.calls = append(m.calls, "ConnectionError:"+connID)
}

func (m *mockMetricsCollector) MessageReceived(connID string, channel string, event string, size int) {
	m.calls = append(m.calls, "MessageReceived:"+connID+":"+channel+":"+event)
}

func (m *mockMetricsCollector) MessageSent(connID string, channel string, event string, size int) {
	m.calls = append(m.calls, "MessageSent:"+connID+":"+channel+":"+event)
}

func (m *mockMetricsCollector) MessageBroadcast(channel string, event string, recipientCount int) {
	m.calls = append(m.calls, "MessageBroadcast:"+channel+":"+event)
}

func (m *mockMetricsCollector) ChannelJoined(userID string, channel string) {
	m.calls = append(m.calls, "ChannelJoined:"+userID+":"+channel)
}

func (m *mockMetricsCollector) ChannelLeft(userID string, channel string) {
	m.calls = append(m.calls, "ChannelLeft:"+userID+":"+channel)
}

func (m *mockMetricsCollector) ChannelCreated(channel string) {
	m.calls = append(m.calls, "ChannelCreated:"+channel)
}

func (m *mockMetricsCollector) ChannelDestroyed(channel string) {
	m.calls = append(m.calls, "ChannelDestroyed:"+channel)
}

func (m *mockMetricsCollector) HandlerDuration(handler string, duration time.Duration) {
	m.calls = append(m.calls, "HandlerDuration:"+handler)
}

func (m *mockMetricsCollector) QueueDepth(queue string, depth int) {
	m.calls = append(m.calls, "QueueDepth:"+queue)
}

func (m *mockMetricsCollector) Error(component string, err error) {
	m.calls = append(m.calls, "Error:"+component)
}

func TestWithRateLimiter(t *testing.T) {
	t.Run("allows request when rate limiter approves", func(t *testing.T) {
		mockRL := &mockRateLimiter{
			allowFunc: func(ctx context.Context, key string) (bool, error) {
				return true, nil
			},
		}

		hooks := &Hooks{
			RateLimiter: mockRL,
		}

		keyFunc := func(conn *Conn) string {
			return conn.ID
		}

		middleware := WithRateLimiter(hooks, keyFunc)

		conn := &Conn{ID: "user123"}
		ctx := context.WithValue(context.Background(), connectionContextKey, conn)

		event := &Event{
			ChannelName: "test-channel",
			Event:       "test-event",
		}

		called := false
		nextFunc := func() error {
			called = true
			return nil
		}

		err := middleware(ctx, event, nil, nextFunc)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if !called {
			t.Error("expected next function to be called")
		}

		if len(mockRL.calls) != 1 || mockRL.calls[0] != "Allow:user123" {
			t.Errorf("expected Allow to be called with user123, got %v", mockRL.calls)
		}
	})

	t.Run("blocks request when rate limiter denies", func(t *testing.T) {
		mockRL := &mockRateLimiter{
			allowFunc: func(ctx context.Context, key string) (bool, error) {
				return false, nil
			},
		}

		mockMetrics := &mockMetricsCollector{}

		hooks := &Hooks{
			RateLimiter: mockRL,
			Metrics:     mockMetrics,
		}

		keyFunc := func(conn *Conn) string {
			return conn.ID
		}

		middleware := WithRateLimiter(hooks, keyFunc)

		conn := &Conn{ID: "user123"}
		ctx := context.WithValue(context.Background(), connectionContextKey, conn)

		event := &Event{
			ChannelName: "test-channel",
			Event:       "test-event",
		}

		called := false
		nextFunc := func() error {
			called = true
			return nil
		}

		err := middleware(ctx, event, nil, nextFunc)

		if err == nil {
			t.Error("expected error, got nil")
		}

		var pondErr *Error
		if !errors.As(err, &pondErr) {
			t.Errorf("expected *Error type, got %T", err)
		} else if pondErr.Code != StatusTooManyRequests {
			t.Errorf("expected status %d, got %d", StatusTooManyRequests, pondErr.Code)
		}

		if called {
			t.Error("expected next function not to be called")
		}

		hasErrorCall := false
		for _, call := range mockMetrics.calls {
			if call == "Error:rate_limiter" {
				hasErrorCall = true
				break
			}
		}
		if !hasErrorCall {
			t.Error("expected metrics error to be recorded")
		}
	})

	t.Run("handles rate limiter error", func(t *testing.T) {
		mockRL := &mockRateLimiter{
			allowFunc: func(ctx context.Context, key string) (bool, error) {
				return false, errors.New("rate limiter unavailable")
			},
		}

		hooks := &Hooks{
			RateLimiter: mockRL,
		}

		keyFunc := func(conn *Conn) string {
			return conn.ID
		}

		middleware := WithRateLimiter(hooks, keyFunc)

		conn := &Conn{ID: "user123"}
		ctx := context.WithValue(context.Background(), connectionContextKey, conn)

		event := &Event{
			ChannelName: "test-channel",
		}

		err := middleware(ctx, event, nil, func() error { return nil })

		if err == nil {
			t.Error("expected error, got nil")
		}

		if !contains(err.Error(), "rate limiter error") {
			t.Errorf("expected rate limiter error, got %v", err)
		}
	})

	t.Run("proceeds when no rate limiter configured", func(t *testing.T) {
		middleware := WithRateLimiter(nil, func(conn *Conn) string { return conn.ID })

		ctx := context.Background()
		event := &Event{}

		called := false
		err := middleware(ctx, event, nil, func() error {
			called = true
			return nil
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if !called {
			t.Error("expected next function to be called")
		}
	})

	t.Run("proceeds when no connection in context", func(t *testing.T) {
		hooks := &Hooks{
			RateLimiter: &mockRateLimiter{},
		}

		middleware := WithRateLimiter(hooks, func(conn *Conn) string { return conn.ID })

		ctx := context.Background()
		event := &Event{}

		called := false
		err := middleware(ctx, event, nil, func() error {
			called = true
			return nil
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if !called {
			t.Error("expected next function to be called")
		}
	})
}

func TestWithMetrics(t *testing.T) {
	t.Run("records metrics for successful request", func(t *testing.T) {
		mockMetrics := &mockMetricsCollector{}

		hooks := &Hooks{
			Metrics: mockMetrics,
		}

		middleware := WithMetrics(hooks)

		conn := &Conn{ID: "user123"}
		ctx := context.WithValue(context.Background(), connectionContextKey, conn)

		event := &Event{
			ChannelName: "test-channel",
			Event:       "test-event",
		}

		err := middleware(ctx, event, nil, func() error {
			time.Sleep(10 * time.Millisecond)
			return nil
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		expectedCalls := []string{
			"MessageReceived:user123:test-channel:test-event",
			"HandlerDuration:test-event",
		}

		if len(mockMetrics.calls) != len(expectedCalls) {
			t.Errorf("expected %d calls, got %d: %v", len(expectedCalls), len(mockMetrics.calls), mockMetrics.calls)
		}

		for i, expected := range expectedCalls {
			if i < len(mockMetrics.calls) && mockMetrics.calls[i] != expected {
				t.Errorf("call %d: expected %s, got %s", i, expected, mockMetrics.calls[i])
			}
		}
	})

	t.Run("records error metrics for failed request", func(t *testing.T) {
		mockMetrics := &mockMetricsCollector{}

		hooks := &Hooks{
			Metrics: mockMetrics,
		}

		middleware := WithMetrics(hooks)

		conn := &Conn{ID: "user123"}
		ctx := context.WithValue(context.Background(), connectionContextKey, conn)

		event := &Event{
			ChannelName: "test-channel",
			Event:       "test-event",
		}

		expectedErr := errors.New("handler error")
		err := middleware(ctx, event, nil, func() error {
			return expectedErr
		})

		if err != expectedErr {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}

		hasErrorCall := false
		for _, call := range mockMetrics.calls {
			if call == "Error:message_handler" {
				hasErrorCall = true
				break
			}
		}

		if !hasErrorCall {
			t.Error("expected error metric to be recorded")
		}
	})

	t.Run("proceeds when no metrics configured", func(t *testing.T) {
		middleware := WithMetrics(nil)

		ctx := context.Background()
		event := &Event{}

		called := false
		err := middleware(ctx, event, nil, func() error {
			called = true
			return nil
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if !called {
			t.Error("expected next function to be called")
		}
	})

	t.Run("handles missing connection in context", func(t *testing.T) {
		mockMetrics := &mockMetricsCollector{}

		hooks := &Hooks{
			Metrics: mockMetrics,
		}

		middleware := WithMetrics(hooks)

		ctx := context.Background()
		event := &Event{}

		err := middleware(ctx, event, nil, func() error {
			return nil
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		if len(mockMetrics.calls) != 0 {
			t.Errorf("expected no metrics calls without connection, got %v", mockMetrics.calls)
		}
	})
}

func TestNoopMetrics(t *testing.T) {
	t.Run("noop metrics implements interface", func(t *testing.T) {
		noop := NoopMetrics()

		noop.ConnectionOpened("conn1", "/ws")
		noop.ConnectionClosed("conn1", time.Second)
		noop.ConnectionError("conn1", errors.New("test"))
		noop.MessageReceived("conn1", "channel1", "event1", 100)
		noop.MessageSent("conn1", "channel1", "event1", 100)
		noop.MessageBroadcast("channel1", "event1", 5)
		noop.ChannelJoined("user1", "channel1")
		noop.ChannelLeft("user1", "channel1")
		noop.ChannelCreated("channel1")
		noop.ChannelDestroyed("channel1")
		noop.HandlerDuration("handler1", time.Second)
		noop.QueueDepth("queue1", 10)
		noop.Error("component1", errors.New("test"))

	})
}

func TestHooksStructure(t *testing.T) {
	t.Run("hooks can be created with all fields", func(t *testing.T) {
		connectCalled := false
		disconnectCalled := false
		beforeJoinCalled := false
		afterJoinCalled := false
		beforeMessageCalled := false
		afterMessageCalled := false

		hooks := &Hooks{
			RateLimiter:        &mockRateLimiter{},
			ChannelRateLimiter: &mockRateLimiter{},
			Metrics:            &mockMetricsCollector{},
			OnConnect: func(conn *Conn) error {
				connectCalled = true
				return nil
			},
			OnDisconnect: func(conn *Conn) {
				disconnectCalled = true
			},
			BeforeJoin: func(user *User, channel string) error {
				beforeJoinCalled = true
				return nil
			},
			AfterJoin: func(user *User, channel string) {
				afterJoinCalled = true
			},
			BeforeMessage: func(event *Event) error {
				beforeMessageCalled = true
				return nil
			},
			AfterMessage: func(event *Event, err error) {
				afterMessageCalled = true
			},
		}

		conn := &Conn{ID: "test"}
		user := &User{UserID: "test"}
		event := &Event{}

		if hooks.OnConnect != nil {
			hooks.OnConnect(conn)
		}
		if hooks.OnDisconnect != nil {
			hooks.OnDisconnect(conn)
		}
		if hooks.BeforeJoin != nil {
			hooks.BeforeJoin(user, "channel")
		}
		if hooks.AfterJoin != nil {
			hooks.AfterJoin(user, "channel")
		}
		if hooks.BeforeMessage != nil {
			hooks.BeforeMessage(event)
		}
		if hooks.AfterMessage != nil {
			hooks.AfterMessage(event, nil)
		}

		if !connectCalled {
			t.Error("expected OnConnect to be called")
		}
		if !disconnectCalled {
			t.Error("expected OnDisconnect to be called")
		}
		if !beforeJoinCalled {
			t.Error("expected BeforeJoin to be called")
		}
		if !afterJoinCalled {
			t.Error("expected AfterJoin to be called")
		}
		if !beforeMessageCalled {
			t.Error("expected BeforeMessage to be called")
		}
		if !afterMessageCalled {
			t.Error("expected AfterMessage to be called")
		}
	})
}
