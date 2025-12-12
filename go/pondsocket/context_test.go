package pondsocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func createContextTestChannel(ctx context.Context, name string) *Channel {
	opts := options{
		Name:                 name,
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	return newChannel(ctx, opts)
}

func createTestEvent(requestId, event string, payload interface{}) *Event {
	return &Event{
		Action:    system,
		RequestId: requestId,
		Event:     event,
		Payload:   payload,
	}
}

func TestJoinContext(t *testing.T) {
	ctx := context.Background()

	t.Run("Accept adds user to channel", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "member"})
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Accept()

		if joinCtx.err != nil {
			t.Errorf("Expected no error, got %v", joinCtx.err)
		}

		user, err := channel.GetUser("user1")
		if err != nil {
			t.Errorf("Expected user to be in channel, got error: %v", err)
		}
		if user.UserID != "user1" {
			t.Errorf("Expected user ID user1, got %s", user.UserID)
		}
	})

	t.Run("Accept prevents double response", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Accept()
		joinCtx.Accept()

		if joinCtx.err == nil {
			t.Error("Expected error on double accept")
		}
	})

	t.Run("Decline prevents user from joining", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		err := joinCtx.Decline(403, "Not authorized")

		if err != nil {
			t.Logf("Decline returned error (expected without websocket): %v", err)
		}

		_, err = channel.GetUser("user1")
		if err == nil {
			t.Error("Expected user NOT to be in channel after decline")
		}
	})

	t.Run("SetAssigns before accept updates connection assigns", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.SetAssigns("key1", "value1")

		if conn.assigns["key1"] != "value1" {
			t.Errorf("Expected assigns key1=value1, got %v", conn.assigns["key1"])
		}
	})

	t.Run("SetAssigns after accept updates channel assigns", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Accept()
		joinCtx.SetAssigns("key2", "value2")

		user, _ := channel.GetUser("user1")
		if user.Assigns["key2"] != "value2" {
			t.Errorf("Expected channel assigns key2=value2, got %v", user.Assigns["key2"])
		}
	})

	t.Run("Assigns sets multiple values", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Assigns(map[string]interface{}{"a": 1, "b": 2})

		if conn.assigns["a"] != 1 || conn.assigns["b"] != 2 {
			t.Errorf("Expected assigns a=1, b=2, got %v", conn.assigns)
		}
	})

	t.Run("Assigns with non-map returns error", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Assigns("not a map")

		if joinCtx.err == nil {
			t.Error("Expected error for non-map assigns")
		}
	})

	t.Run("GetAssign returns value", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"existing": "value"})
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		value := joinCtx.GetAssign("existing")

		if value != "value" {
			t.Errorf("Expected 'value', got %v", value)
		}
	})

	t.Run("GetPayload returns event payload", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		payload := map[string]interface{}{"token": "abc123"}
		event := createTestEvent("req-1", "join", payload)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		result := joinCtx.GetPayload()

		if result.(map[string]interface{})["token"] != "abc123" {
			t.Errorf("Expected token=abc123, got %v", result)
		}
	})

	t.Run("GetUser returns user info", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "admin"})
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		user := joinCtx.GetUser()

		if user.UserID != "user1" {
			t.Errorf("Expected user ID user1, got %s", user.UserID)
		}
		if user.Assigns["role"] != "admin" {
			t.Errorf("Expected role=admin, got %v", user.Assigns["role"])
		}
	})

	t.Run("Context returns context", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)

		if joinCtx.Context() != ctx {
			t.Error("Expected context to match")
		}
	})

	t.Run("Error returns empty string when no error", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)

		if joinCtx.Error() != "" {
			t.Errorf("Expected empty error, got %s", joinCtx.Error())
		}
	})

	t.Run("Reply requires accept first", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Reply("welcome", nil)

		if joinCtx.err == nil {
			t.Error("Expected error when replying before accept")
		}
	})

	t.Run("Broadcast requires accept first", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Broadcast("event", nil)

		if joinCtx.err == nil {
			t.Error("Expected error when broadcasting before accept")
		}
	})

	t.Run("Track requires accept first", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Track(map[string]interface{}{"status": "online"})

		if joinCtx.err == nil {
			t.Error("Expected error when tracking before accept")
		}
	})

	t.Run("Cancelled context returns error", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(cancelCtx, channel, nil, conn, event)

		if joinCtx.err == nil {
			t.Error("Expected error for cancelled context")
		}
	})

	t.Run("GetAllPresence returns nil when channel is nil", func(t *testing.T) {
		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := &JoinContext{
			Channel: nil,
			conn:    conn,
			event:   event,
			ctx:     ctx,
		}

		if joinCtx.GetAllPresence() != nil {
			t.Error("Expected nil for nil channel")
		}
	})

	t.Run("GetAllAssigns returns nil when channel is nil", func(t *testing.T) {
		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := &JoinContext{
			Channel: nil,
			conn:    conn,
			event:   event,
			ctx:     ctx,
		}

		if joinCtx.GetAllAssigns() != nil {
			t.Error("Expected nil for nil channel")
		}
	})
}

func TestLeaveContext(t *testing.T) {
	ctx := context.Background()

	t.Run("GetReason returns leave reason", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		leaveCtx := newLeaveContext(ctx, channel, user, "disconnected")

		if leaveCtx.GetReason() != "disconnected" {
			t.Errorf("Expected reason 'disconnected', got %s", leaveCtx.GetReason())
		}
	})

	t.Run("RemainingUserCount returns correct count", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn1 := createTestConn("user1", nil)
		conn2 := createTestConn("user2", nil)
		channel.addUser(conn1)
		channel.addUser(conn2)

		user := &User{UserID: "user3", Assigns: map[string]interface{}{}}
		leaveCtx := newLeaveContext(ctx, channel, user, "leave")

		if leaveCtx.RemainingUserCount() != 2 {
			t.Errorf("Expected 2 remaining users, got %d", leaveCtx.RemainingUserCount())
		}
	})

	t.Run("Context returns context", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		leaveCtx := newLeaveContext(ctx, channel, user, "leave")

		if leaveCtx.Context() != ctx {
			t.Error("Expected context to match")
		}
	})

	t.Run("Error returns empty when no error", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		leaveCtx := newLeaveContext(ctx, channel, user, "leave")

		if leaveCtx.Error() != "" {
			t.Errorf("Expected empty error, got %s", leaveCtx.Error())
		}
	})

	t.Run("Broadcast prevents double broadcast", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		leaveCtx := newLeaveContext(ctx, channel, user, "leave")

		leaveCtx.Broadcast("leave", nil)
		leaveCtx.Broadcast("leave", nil)

		if leaveCtx.err == nil {
			t.Error("Expected error on double broadcast")
		}
	})

	t.Run("BroadcastTo prevents double broadcast", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		leaveCtx := newLeaveContext(ctx, channel, user, "leave")

		leaveCtx.BroadcastTo("leave", nil, "user2")
		leaveCtx.BroadcastTo("leave", nil, "user3")

		if leaveCtx.err == nil {
			t.Error("Expected error on double broadcast")
		}
	})

	t.Run("Cancelled context returns error", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		leaveCtx := newLeaveContext(cancelCtx, channel, user, "leave")

		if leaveCtx.err == nil {
			t.Error("Expected error for cancelled context")
		}
	})

	t.Run("GetAllPresence returns nil when channel is nil", func(t *testing.T) {
		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		leaveCtx := &LeaveContext{
			Channel: nil,
			user:    user,
			ctx:     ctx,
		}

		if leaveCtx.GetAllPresence() != nil {
			t.Error("Expected nil for nil channel")
		}
	})

	t.Run("GetAllAssigns returns nil when channel is nil", func(t *testing.T) {
		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		leaveCtx := &LeaveContext{
			Channel: nil,
			user:    user,
			ctx:     ctx,
		}

		if leaveCtx.GetAllAssigns() != nil {
			t.Error("Expected nil for nil channel")
		}
	})

	t.Run("ParseAssigns returns error for nil user", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		leaveCtx := &LeaveContext{
			Channel: channel,
			user:    nil,
			ctx:     ctx,
		}

		var result map[string]interface{}
		err := leaveCtx.ParseAssigns(&result)

		if err == nil {
			t.Error("Expected error for nil user")
		}
	})

	t.Run("ParsePresence returns error for nil user", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		leaveCtx := &LeaveContext{
			Channel: channel,
			user:    nil,
			ctx:     ctx,
		}

		var result map[string]interface{}
		err := leaveCtx.ParsePresence(&result)

		if err == nil {
			t.Error("Expected error for nil user")
		}
	})

	t.Run("GetUser returns user", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{"role": "admin"}}
		leaveCtx := newLeaveContext(ctx, channel, user, "leave")

		result := leaveCtx.GetUser()
		if result == nil {
			t.Error("Expected user to be non-nil")
		}
		if result.UserID != "user1" {
			t.Errorf("Expected UserID user1, got %s", result.UserID)
		}
	})

	t.Run("GetAssign returns value for existing key", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{"role": "admin"}}
		leaveCtx := newLeaveContext(ctx, channel, user, "leave")

		value := leaveCtx.GetAssign("role")
		if value != "admin" {
			t.Errorf("Expected role admin, got %v", value)
		}
	})

	t.Run("GetAssign returns nil for non-existent key", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{"role": "admin"}}
		leaveCtx := newLeaveContext(ctx, channel, user, "leave")

		value := leaveCtx.GetAssign("nonexistent")
		if value != nil {
			t.Errorf("Expected nil, got %v", value)
		}
	})

	t.Run("GetAssign returns nil for nil user", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		leaveCtx := &LeaveContext{
			Channel: channel,
			user:    nil,
			ctx:     ctx,
		}

		value := leaveCtx.GetAssign("role")
		if value != nil {
			t.Errorf("Expected nil, got %v", value)
		}
	})
}

func TestEventContext(t *testing.T) {
	ctx := context.Background()

	t.Run("Reply sends response", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.addUser(conn)
		channel.connections.Update(conn.ID, conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		eventCtx.Reply("response", map[string]interface{}{"data": "test"})

		if eventCtx.err != nil {
			t.Errorf("Expected no error, got %v", eventCtx.err)
		}
		if !eventCtx.HasResponded {
			t.Error("Expected HasResponded to be true")
		}
	})

	t.Run("Reply prevents double response", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.addUser(conn)
		channel.connections.Update(conn.ID, conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		eventCtx.Reply("response1", nil)
		eventCtx.Reply("response2", nil)

		if eventCtx.err == nil {
			t.Error("Expected error on double reply")
		}
	})

	t.Run("GetPayload returns event payload", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		payload := map[string]interface{}{"message": "hello"}
		event := createTestEvent("req-1", "message", payload)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		result := eventCtx.GetPayload()

		if result.(map[string]interface{})["message"] != "hello" {
			t.Errorf("Expected message=hello, got %v", result)
		}
	})

	t.Run("GetUser returns current user", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "admin"})
		channel.addUser(conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{"role": "admin"}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		result := eventCtx.GetUser()

		if result.UserID != "user1" {
			t.Errorf("Expected user ID user1, got %s", result.UserID)
		}
	})

	t.Run("Context returns context", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)

		if eventCtx.Context() != ctx {
			t.Error("Expected context to match")
		}
	})

	t.Run("Error returns empty when no error", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)

		if eventCtx.Error() != "" {
			t.Errorf("Expected empty error, got %s", eventCtx.Error())
		}
	})

	t.Run("Cancelled context returns error", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(cancelCtx, channel, msgEvent, nil)

		if eventCtx.err == nil {
			t.Error("Expected error for cancelled context")
		}
	})

	t.Run("GetAllPresence returns channel presence", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.addUser(conn)
		channel.Track("user1", map[string]interface{}{"status": "online"})

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		time.Sleep(10 * time.Millisecond)
		presence := eventCtx.GetAllPresence()

		if presence == nil {
			t.Error("Expected presence data")
		}
	})

	t.Run("GetAllAssigns returns channel assigns", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "member"})
		channel.addUser(conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		assigns := eventCtx.GetAllAssigns()

		if assigns == nil {
			t.Error("Expected assigns data")
		}
	})

	t.Run("Broadcast sends to all users", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.addUser(conn)
		channel.connections.Update(conn.ID, conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		eventCtx.Broadcast("notification", nil)

		if eventCtx.err != nil {
			t.Errorf("Expected no error, got %v", eventCtx.err)
		}
	})

	t.Run("BroadcastTo sends to specific users", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.addUser(conn)
		channel.connections.Update(conn.ID, conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		eventCtx.BroadcastTo("notification", nil, "user1")

		if eventCtx.err != nil {
			t.Errorf("Expected no error, got %v", eventCtx.err)
		}
	})

	t.Run("BroadcastFrom excludes sender", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn1 := createTestConn("user1", nil)
		conn2 := createTestConn("user2", nil)
		channel.addUser(conn1)
		channel.addUser(conn2)
		channel.connections.Update(conn1.ID, conn1)
		channel.connections.Update(conn2.ID, conn2)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		eventCtx.BroadcastFrom("notification", nil)

		if eventCtx.err != nil {
			t.Errorf("Expected no error, got %v", eventCtx.err)
		}
	})

	t.Run("SetAssigns updates user assigns", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.addUser(conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		eventCtx.SetAssigns("newKey", "newValue")

		if eventCtx.err != nil {
			t.Errorf("Expected no error, got %v", eventCtx.err)
		}
	})

	t.Run("Assign sets multiple values", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.addUser(conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		eventCtx.Assign(map[string]interface{}{"a": 1, "b": 2})

		if eventCtx.err != nil {
			t.Errorf("Expected no error, got %v", eventCtx.err)
		}
	})

	t.Run("Assign with non-map returns error", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		eventCtx.Assign("not a map")

		if eventCtx.err == nil {
			t.Error("Expected error for non-map assigns")
		}
	})

	t.Run("Track starts presence tracking", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.addUser(conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		eventCtx.Track(map[string]interface{}{"status": "online"})

		if eventCtx.err != nil {
			t.Errorf("Expected no error, got %v", eventCtx.err)
		}
	})

	t.Run("Update updates presence", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.addUser(conn)
		channel.Track("user1", map[string]interface{}{"status": "online"})

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		eventCtx.Update(map[string]interface{}{"status": "away"})

		if eventCtx.err != nil {
			t.Errorf("Expected no error, got %v", eventCtx.err)
		}
	})

	t.Run("UnTrack stops presence tracking", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.addUser(conn)
		channel.Track("user1", map[string]interface{}{"status": "online"})

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		eventCtx.UnTrack()

		if eventCtx.err != nil {
			t.Errorf("Expected no error, got %v", eventCtx.err)
		}
	})

	t.Run("Evict removes user from channel", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		conn2 := createTestConn("user2", nil)
		channel.addUser(conn)
		channel.addUser(conn2)
		channel.connections.Update(conn.ID, conn)
		channel.connections.Update(conn2.ID, conn2)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		eventCtx.Evict("bad behavior", "user2")

		if eventCtx.err != nil {
			t.Errorf("Expected no error, got %v", eventCtx.err)
		}
	})
}

func TestOutgoingContext(t *testing.T) {
	ctx := context.Background()

	t.Run("newOutgoingContext creates context", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", map[string]interface{}{"data": "test"})

		outCtx := newOutgoingContext(ctx, channel, event, user, nil)

		if outCtx == nil {
			t.Fatal("Expected outgoing context to be created")
		}
	})

	t.Run("GetPayload returns payload", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		payload := map[string]interface{}{"data": "test"}
		event := createTestEvent("req-1", "message", payload)

		outCtx := newOutgoingContext(ctx, channel, event, user, nil)
		result := outCtx.GetPayload()

		if result.(map[string]interface{})["data"] != "test" {
			t.Errorf("Expected data=test, got %v", result)
		}
	})

	t.Run("GetEvent returns event name", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "my-event", nil)

		outCtx := newOutgoingContext(ctx, channel, event, user, nil)
		result := outCtx.GetEvent()

		if result != "my-event" {
			t.Errorf("Expected event 'my-event', got %s", result)
		}
	})

	t.Run("Context returns context", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)

		outCtx := newOutgoingContext(ctx, channel, event, user, nil)

		if outCtx.Context() != ctx {
			t.Error("Expected context to match")
		}
	})

	t.Run("Error returns empty when no error", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)

		outCtx := newOutgoingContext(ctx, channel, event, user, nil)

		if outCtx.Error() != "" {
			t.Errorf("Expected empty error, got %s", outCtx.Error())
		}
	})

	t.Run("Block blocks the message", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)

		outCtx := newOutgoingContext(ctx, channel, event, user, nil)
		outCtx.Block()

		if !outCtx.IsBlocked() {
			t.Error("Expected message to be blocked")
		}
	})

	t.Run("Unblock unblocks the message", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)

		outCtx := newOutgoingContext(ctx, channel, event, user, nil)
		outCtx.Block()
		outCtx.Unblock()

		if outCtx.IsBlocked() {
			t.Error("Expected message to be unblocked")
		}
	})

	t.Run("IsBlocked returns blocked status", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)

		outCtx := newOutgoingContext(ctx, channel, event, user, nil)

		if outCtx.IsBlocked() {
			t.Error("Expected not blocked initially")
		}

		outCtx.Block()

		if !outCtx.IsBlocked() {
			t.Error("Expected blocked after Block()")
		}
	})

	t.Run("Transform changes payload", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", map[string]interface{}{"old": "data"})

		outCtx := newOutgoingContext(ctx, channel, event, user, nil)
		outCtx.Transform(map[string]interface{}{"new": "data"})

		if !outCtx.HasTransformed() {
			t.Error("Expected HasTransformed to be true")
		}

		newPayload := outCtx.GetPayload().(map[string]interface{})
		if newPayload["new"] != "data" {
			t.Errorf("Expected transformed payload, got %v", newPayload)
		}
	})

	t.Run("HasTransformed returns false initially", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)

		outCtx := newOutgoingContext(ctx, channel, event, user, nil)

		if outCtx.HasTransformed() {
			t.Error("Expected HasTransformed to be false initially")
		}
	})
}

func TestConnectionContextIntegration(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	t.Run("Accept upgrades connection", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		connectionAccepted := make(chan bool, 1)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route := &Route{}

			connOpts := connectionOptions{
				request:  r,
				response: &w,
				endpoint: endpoint,
				userId:   "test-user",
				upgrader: upgrader,
				connCtx:  ctx,
				route:    route,
			}

			connCtx := newConnectionContext(connOpts)

			connCtx.SetAssigns("role", "member")
			value := connCtx.GetAssign("role")
			if value != "member" {
				t.Errorf("Expected role=member, got %v", value)
			}

			user := connCtx.GetUser()
			if user.UserID != "test-user" {
				t.Errorf("Expected user ID test-user, got %s", user.UserID)
			}

			cookies := connCtx.Cookies()
			if cookies == nil {
				t.Error("Expected cookies to be non-nil")
			}

			headers := connCtx.Headers()
			if headers == nil {
				t.Error("Expected headers to be non-nil")
			}

			innerCtx := connCtx.Context()
			if innerCtx == nil {
				t.Error("Expected context to be non-nil")
			}

			err := connCtx.Accept()
			if err == nil {
				connectionAccepted <- true
			} else {
				connectionAccepted <- false
			}
		}))
		defer server.Close()

		wsURL := "ws" + server.URL[4:] + "/test"
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("Failed to connect: %v", err)
		}
		defer conn.Close()

		select {
		case accepted := <-connectionAccepted:
			if !accepted {
				t.Error("Connection was not accepted")
			}
		case <-time.After(1 * time.Second):
			t.Error("Timeout waiting for connection")
		}
	})

	t.Run("Decline rejects connection", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route := &Route{}

			connOpts := connectionOptions{
				request:  r,
				response: &w,
				endpoint: endpoint,
				userId:   "test-user",
				upgrader: upgrader,
				connCtx:  ctx,
				route:    route,
			}

			connCtx := newConnectionContext(connOpts)
			err := connCtx.Decline(403, "Access denied")
			if err != nil {
				t.Errorf("Decline returned error: %v", err)
			}
		}))
		defer server.Close()

		resp, err := http.Get(server.URL + "/test")
		if err != nil {
			t.Fatalf("Request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 403 {
			t.Errorf("Expected 403, got %d", resp.StatusCode)
		}
	})

	t.Run("Double response returns error", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		endpoint := newEndpoint(ctx, "/test", opts)

		errorChan := make(chan error, 1)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route := &Route{}

			connOpts := connectionOptions{
				request:  r,
				response: &w,
				endpoint: endpoint,
				userId:   "test-user",
				upgrader: upgrader,
				connCtx:  ctx,
				route:    route,
			}

			connCtx := newConnectionContext(connOpts)
			connCtx.Decline(403, "First decline")
			err := connCtx.Decline(403, "Second decline")
			errorChan <- err
		}))
		defer server.Close()

		http.Get(server.URL + "/test")

		select {
		case err := <-errorChan:
			if err == nil {
				t.Error("Expected error on double decline")
			}
		case <-time.After(1 * time.Second):
			t.Error("Timeout")
		}
	})
}

func TestContextConcurrency(t *testing.T) {
	ctx := context.Background()
	channel := createContextTestChannel(ctx, "test-channel")
	defer func() {

		time.Sleep(50 * time.Millisecond)
		channel.Close()
	}()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()

			conn := createTestConn("user-"+string(rune('a'+n)), nil)
			event := createTestEvent("req-1", "join", nil)

			joinCtx := newJoinContext(ctx, channel, nil, conn, event)
			joinCtx.Accept()
			joinCtx.SetAssigns("index", n)
		}(i)
	}
	wg.Wait()
}

func TestEventContextParsePayload(t *testing.T) {
	ctx := context.Background()

	t.Run("ParsePayload successfully unmarshals struct", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.addUser(conn)

		type MessagePayload struct {
			Text   string `json:"text"`
			Number int    `json:"number"`
		}

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		payload := map[string]interface{}{"text": "hello", "number": float64(42)}
		event := createTestEvent("req-1", "message", payload)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		var result MessagePayload
		err := eventCtx.ParsePayload(&result)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if result.Text != "hello" {
			t.Errorf("Expected text 'hello', got '%s'", result.Text)
		}
		if result.Number != 42 {
			t.Errorf("Expected number 42, got %d", result.Number)
		}
	})

	t.Run("ParsePayload with cancelled context returns error", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(cancelCtx, channel, msgEvent, nil)
		var result map[string]interface{}
		err := eventCtx.ParsePayload(&result)

		if err == nil {
			t.Error("Expected error for cancelled context")
		}
	})
}

func TestEventContextParseAssigns(t *testing.T) {
	ctx := context.Background()

	t.Run("ParseAssigns successfully unmarshals struct", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		type UserAssigns struct {
			Role  string `json:"role"`
			Level int    `json:"level"`
		}

		conn := createTestConn("user1", map[string]interface{}{"role": "admin", "level": float64(5)})
		channel.addUser(conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{"role": "admin", "level": float64(5)}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		var result UserAssigns
		err := eventCtx.ParseAssigns(&result)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if result.Role != "admin" {
			t.Errorf("Expected role 'admin', got '%s'", result.Role)
		}
	})

	t.Run("ParseAssigns with cancelled context returns error", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(cancelCtx, channel, msgEvent, nil)

		var result map[string]interface{}
		err := eventCtx.ParseAssigns(&result)

		if err == nil {
			t.Error("Expected error for cancelled context")
		}
	})
}

func TestEventContextParsePresence(t *testing.T) {
	ctx := context.Background()

	t.Run("ParsePresence successfully unmarshals struct", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		type PresenceData struct {
			Status string `json:"status"`
			Online bool   `json:"online"`
		}

		conn := createTestConn("user1", nil)
		channel.addUser(conn)
		channel.Track("user1", map[string]interface{}{"status": "active", "online": true})

		time.Sleep(10 * time.Millisecond)

		user := &User{
			UserID:   "user1",
			Assigns:  map[string]interface{}{},
			Presence: map[string]interface{}{"status": "active", "online": true},
		}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		var result PresenceData
		err := eventCtx.ParsePresence(&result)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if result.Status != "active" {
			t.Errorf("Expected status 'active', got '%s'", result.Status)
		}
	})

	t.Run("ParsePresence returns error when user not in channel", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "nonexistent", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)

		var result map[string]interface{}
		err := eventCtx.ParsePresence(&result)

		if err == nil {
			t.Error("Expected error for user not in channel")
		}
	})
}

func TestEventContextGetAssign(t *testing.T) {
	ctx := context.Background()

	t.Run("GetAssign returns value for existing key", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "admin"})
		channel.addUser(conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{"role": "admin"}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		value, err := eventCtx.GetAssign("role")

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if value != "admin" {
			t.Errorf("Expected 'admin', got %v", value)
		}
	})

	t.Run("GetAssign returns error for non-existent key", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "admin"})
		channel.addUser(conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{"role": "admin"}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(ctx, channel, msgEvent, nil)
		value, err := eventCtx.GetAssign("nonexistent")

		if err == nil {
			t.Error("Expected error for non-existent key")
		}
		if value != nil {
			t.Errorf("Expected nil value, got %v", value)
		}
	})

	t.Run("GetAssign with cancelled context returns error", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)
		msgEvent := &messageEvent{User: user, Event: event}

		eventCtx := newEventContext(cancelCtx, channel, msgEvent, nil)
		_, err := eventCtx.GetAssign("role")

		if err == nil {
			t.Error("Expected error for cancelled context")
		}
	})
}

func TestJoinContextBroadcastTo(t *testing.T) {
	ctx := context.Background()

	t.Run("BroadcastTo after accept succeeds", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn1 := createTestConn("user1", nil)
		conn2 := createTestConn("user2", nil)
		channel.addUser(conn2)
		channel.connections.Update(conn2.ID, conn2)

		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn1, event)
		joinCtx.Accept()
		joinCtx.BroadcastTo("user-joined", map[string]interface{}{"user": "user1"}, "user2")

		if joinCtx.err != nil {
			t.Errorf("Expected no error, got %v", joinCtx.err)
		}
	})

	t.Run("BroadcastTo before accept fails", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.BroadcastTo("user-joined", nil, "user2")

		if joinCtx.err == nil {
			t.Error("Expected error when broadcasting before accept")
		}
	})
}

func TestJoinContextBroadcastFrom(t *testing.T) {
	ctx := context.Background()

	t.Run("BroadcastFrom after accept succeeds", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn1 := createTestConn("user1", nil)
		conn2 := createTestConn("user2", nil)
		channel.addUser(conn2)
		channel.connections.Update(conn2.ID, conn2)

		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn1, event)
		joinCtx.Accept()
		joinCtx.BroadcastFrom("user-joined", map[string]interface{}{"user": "user1"})

		if joinCtx.err != nil {
			t.Errorf("Expected no error, got %v", joinCtx.err)
		}
	})

	t.Run("BroadcastFrom before accept fails", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.BroadcastFrom("user-joined", nil)

		if joinCtx.err == nil {
			t.Error("Expected error when broadcasting before accept")
		}
	})
}

func TestJoinContextReply(t *testing.T) {
	ctx := context.Background()

	t.Run("Reply after accept succeeds", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.connections.Update(conn.ID, conn)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Accept()
		joinCtx.Reply("welcome", map[string]interface{}{"message": "Hello!"})

		if joinCtx.err != nil {
			t.Errorf("Expected no error, got %v", joinCtx.err)
		}
	})

	t.Run("Reply with cancelled context returns early", func(t *testing.T) {
		cancelCtx, cancel := context.WithCancel(ctx)
		cancel()

		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(cancelCtx, channel, nil, conn, event)

		if joinCtx.err == nil {
			t.Error("Expected error for cancelled context")
		}
	})
}

func TestJoinContextParsePayload(t *testing.T) {
	ctx := context.Background()

	t.Run("ParsePayload successfully unmarshals struct", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		type JoinPayload struct {
			Token string `json:"token"`
			Room  string `json:"room"`
		}

		conn := createTestConn("user1", nil)
		payload := map[string]interface{}{"token": "abc123", "room": "general"}
		event := createTestEvent("req-1", "join", payload)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		var result JoinPayload
		err := joinCtx.ParsePayload(&result)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if result.Token != "abc123" {
			t.Errorf("Expected token 'abc123', got '%s'", result.Token)
		}
		if result.Room != "general" {
			t.Errorf("Expected room 'general', got '%s'", result.Room)
		}
	})
}

func TestJoinContextParseAssigns(t *testing.T) {
	ctx := context.Background()

	t.Run("ParseAssigns before accept reads connection assigns", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		type UserAssigns struct {
			Role string `json:"role"`
		}

		conn := createTestConn("user1", map[string]interface{}{"role": "member"})
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		var result UserAssigns
		err := joinCtx.ParseAssigns(&result)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if result.Role != "member" {
			t.Errorf("Expected role 'member', got '%s'", result.Role)
		}
	})
}

func TestJoinContextParsePresence(t *testing.T) {
	ctx := context.Background()

	t.Run("ParsePresence returns error when not tracked", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		var result map[string]interface{}
		err := joinCtx.ParsePresence(&result)

		if err == nil {
			t.Error("Expected error for user not tracked")
		}
	})
}

func TestLeaveContextParseAssignsSuccess(t *testing.T) {
	ctx := context.Background()

	t.Run("ParseAssigns successfully unmarshals struct", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		type UserAssigns struct {
			Role  string `json:"role"`
			Level int    `json:"level"`
		}

		user := &User{
			UserID:  "user1",
			Assigns: map[string]interface{}{"role": "admin", "level": float64(5)},
		}
		leaveCtx := newLeaveContext(ctx, channel, user, "leave")

		var result UserAssigns
		err := leaveCtx.ParseAssigns(&result)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if result.Role != "admin" {
			t.Errorf("Expected role 'admin', got '%s'", result.Role)
		}
	})
}

func TestLeaveContextParsePresenceSuccess(t *testing.T) {
	ctx := context.Background()

	t.Run("ParsePresence successfully unmarshals struct", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		type PresenceData struct {
			Status string `json:"status"`
		}

		user := &User{
			UserID:   "user1",
			Assigns:  map[string]interface{}{},
			Presence: map[string]interface{}{"status": "offline"},
		}
		leaveCtx := newLeaveContext(ctx, channel, user, "leave")

		var result PresenceData
		err := leaveCtx.ParsePresence(&result)

		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if result.Status != "offline" {
			t.Errorf("Expected status 'offline', got '%s'", result.Status)
		}
	})
}

func TestOutgoingContextRefreshUser(t *testing.T) {
	ctx := context.Background()

	t.Run("refreshes user data successfully", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"key": "value"})
		channel.addUser(conn)

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)

		outCtx := newOutgoingContext(ctx, channel, event, user, conn)
		result := outCtx.RefreshUser()

		if result == nil {
			t.Error("Expected RefreshUser to return context")
		}
		if outCtx.User == nil {
			t.Error("Expected User to be set")
		}
	})

	t.Run("handles user not found", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		user := &User{UserID: "nonexistent", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)

		outCtx := newOutgoingContext(ctx, channel, event, user, nil)
		result := outCtx.RefreshUser()

		if result == nil {
			t.Error("Expected RefreshUser to return context")
		}
	})

	t.Run("handles closed channel", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		channel.Close()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)

		outCtx := newOutgoingContext(ctx, channel, event, user, nil)
		result := outCtx.RefreshUser()

		if result == nil {
			t.Error("Expected RefreshUser to return context even for closed channel")
		}
	})

	t.Run("handles cancelled context", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		user := &User{UserID: "user1", Assigns: map[string]interface{}{}}
		event := createTestEvent("req-1", "message", nil)

		outCtx := newOutgoingContext(cancelledCtx, channel, event, user, nil)
		result := outCtx.RefreshUser()

		if result == nil {
			t.Error("Expected RefreshUser to return context")
		}
	})
}

func TestJoinContextGetUserAfterAccept(t *testing.T) {
	ctx := context.Background()

	t.Run("returns user from channel after accept", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "member"})
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Accept()

		user := joinCtx.GetUser()

		if user == nil {
			t.Error("Expected user to be returned")
		}
		if user.UserID != "user1" {
			t.Errorf("Expected userId user1, got %s", user.UserID)
		}
	})

	t.Run("returns basic user before accept", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "member"})
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)

		user := joinCtx.GetUser()

		if user == nil {
			t.Error("Expected user to be returned")
		}
	})

	t.Run("returns user with cancelled context", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(cancelledCtx, channel, nil, conn, event)

		user := joinCtx.GetUser()
		if user == nil {
			t.Error("Expected user to be returned even with cancelled context")
		}
	})
}

func TestJoinContextGetAssignAfterAccept(t *testing.T) {
	ctx := context.Background()

	t.Run("gets assign from channel after accept", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "admin"})
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Accept()

		value := joinCtx.GetAssign("role")
		if value != "admin" {
			t.Errorf("Expected role admin, got %v", value)
		}
	})

	t.Run("gets assign from connection before accept", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "member"})
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)

		value := joinCtx.GetAssign("role")
		if value != "member" {
			t.Errorf("Expected role member, got %v", value)
		}
	})

	t.Run("returns nil for non-existent key", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Accept()

		value := joinCtx.GetAssign("nonexistent")
		if value != nil {
			t.Error("Expected nil for non-existent key")
		}
	})
}

func TestJoinContextBroadcastAfterAccept(t *testing.T) {
	ctx := context.Background()

	t.Run("broadcasts successfully after accept", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		channel.connections.Update(conn.ID, conn)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Accept()
		result := joinCtx.Broadcast("notification", nil)

		if result == nil {
			t.Error("Expected result to be returned")
		}
	})

	t.Run("returns error before accept", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		result := joinCtx.Broadcast("notification", nil)

		if result.err == nil {
			t.Error("Expected error when broadcasting before accept")
		}
	})
}

func TestJoinContextTrackAfterAccept(t *testing.T) {
	ctx := context.Background()

	t.Run("tracks successfully after accept", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		joinCtx.Accept()
		result := joinCtx.Track(map[string]interface{}{"status": "online"})

		if result == nil {
			t.Error("Expected result to be returned")
		}
	})

	t.Run("returns error before accept", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		result := joinCtx.Track(nil)

		if result.err == nil {
			t.Error("Expected error when tracking before accept")
		}
	})
}

func TestJoinContextCheckStateAndContext(t *testing.T) {
	ctx := context.Background()

	t.Run("returns true when context is cancelled", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(cancelledCtx, channel, nil, conn, event)
		result := joinCtx.checkStateAndContext()

		if !result {
			t.Error("Expected true when context is cancelled")
		}
	})

	t.Run("returns false when context is active", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		conn := createTestConn("user1", nil)
		event := createTestEvent("req-1", "join", nil)

		joinCtx := newJoinContext(ctx, channel, nil, conn, event)
		result := joinCtx.checkStateAndContext()

		if result {
			t.Error("Expected false when context is active")
		}
	})
}

func TestChannelReportError(t *testing.T) {
	ctx := context.Background()

	t.Run("reports error when metrics configured", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			Hooks: &Hooks{
				Metrics: &mockMetricsCollector{},
			},
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		channel.reportError("test-component", context.Canceled)
	})

	t.Run("handles nil error", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			Hooks: &Hooks{
				Metrics: &mockMetricsCollector{},
			},
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		channel.reportError("test-component", nil)
	})

	t.Run("handles nil hooks", func(t *testing.T) {
		channel := createContextTestChannel(ctx, "test-channel")
		defer channel.Close()

		channel.reportError("test-component", context.Canceled)
	})

	t.Run("handles nil metrics", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			Hooks:                &Hooks{},
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		channel.reportError("test-component", context.Canceled)
	})
}
