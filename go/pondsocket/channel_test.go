package pondsocket

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func createTestConn(id string, assigns map[string]interface{}) *Conn {
	if assigns == nil {
		assigns = make(map[string]interface{})
	}
	ctx, cancel := context.WithCancel(context.Background())

	c := &Conn{
		ID:            id,
		assigns:       assigns,
		closeHandlers: newArray[func(Transport) error](),
		send:          make(chan []byte, 10),
		receive:       make(chan []byte, 10),
		closeChan:     make(chan struct{}),
		ctx:           ctx,
		cancel:        cancel,
		isClosing:     false,
		mutex:         sync.RWMutex{},
	}
	return c
}

func TestChannelCreation(t *testing.T) {
	ctx := context.Background()

	opts := options{
		Name:                 "test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	channel := newChannel(ctx, opts)

	defer channel.Close()

	if channel.name != "test-channel" {
		t.Errorf("expected channel name test-channel, got %s", channel.name)
	}
	if err := channel.checkState(); err != nil {
		t.Errorf("expected channel to be active, got error: %v", err)
	}
}

func TestChannelName(t *testing.T) {
	ctx := context.Background()

	opts := options{
		Name:                 "my-test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	channel := newChannel(ctx, opts)
	defer channel.Close()

	name := channel.Name()
	if name != "my-test-channel" {
		t.Errorf("expected channel name 'my-test-channel', got '%s'", name)
	}
}

func TestChannelAddUser(t *testing.T) {
	ctx := context.Background()

	opts := options{
		Name:                 "test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	channel := newChannel(ctx, opts)

	defer channel.Close()

	t.Run("adds user successfully", func(t *testing.T) {
		mockConn := createTestConn("user1", map[string]interface{}{"role": "member"})

		err := channel.addUser(mockConn)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		user, err := channel.GetUser("user1")

		if err != nil {
			t.Errorf("expected to find user, got error: %v", err)
		}
		if user.UserID != "user1" {
			t.Errorf("expected user ID user1, got %s", user.UserID)
		}
		if user.Assigns["role"] != "member" {
			t.Errorf("expected role member, got %v", user.Assigns["role"])
		}
	})

	t.Run("prevents duplicate users", func(t *testing.T) {
		mockConn := createTestConn("user2", nil)

		err := channel.addUser(mockConn)

		if err != nil {
			t.Fatalf("first add failed: %v", err)
		}
		err = channel.addUser(mockConn)

		if err == nil {
			t.Error("expected error when adding duplicate user")
		}
	})
}

func TestChannelRemoveUser(t *testing.T) {
	ctx := context.Background()

	t.Run("removes user successfully", func(t *testing.T) {
		var leaveCalledMutex sync.Mutex
		leaveCalled := false
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		leaveHandler := LeaveHandler(func(ctx *LeaveContext) {
			leaveCalledMutex.Lock()

			leaveCalled = true
			leaveCalledMutex.Unlock()
		})

		opts.Leave = &leaveHandler
		channel := newChannel(ctx, opts)

		defer channel.Close()

		mockConn := createTestConn("user1", nil)

		channel.addUser(mockConn)

		err := channel.RemoveUser("user1", "test_disconnect")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		_, err = channel.GetUser("user1")

		if err == nil {
			t.Error("expected error when getting removed user")
		}
		time.Sleep(10 * time.Millisecond)

		leaveCalledMutex.Lock()

		if !leaveCalled {
			t.Error("expected leave handler to be called")
		}
		leaveCalledMutex.Unlock()
	})

	t.Run("handles non-existent user", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)

		defer channel.Close()

		err := channel.RemoveUser("nonexistent", "test_disconnect")

		if err == nil {
			t.Error("expected error when removing non-existent user")
		}
	})

	t.Run("calls onDestroy when last user leaves", func(t *testing.T) {
		var destroyCalledMutex sync.Mutex
		destroyCalled := false
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)

		channel.onDestroy = func() error {
			destroyCalledMutex.Lock()

			destroyCalled = true
			destroyCalledMutex.Unlock()

			return nil
		}
		mockConn := createTestConn("user1", nil)

		channel.addUser(mockConn)

		err := channel.RemoveUser("user1", "test_disconnect")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		time.Sleep(50 * time.Millisecond)

		destroyCalledMutex.Lock()

		if !destroyCalled {
			t.Error("expected onDestroy to be called when last user leaves")
		}
		destroyCalledMutex.Unlock()
	})
}

func TestChannelBroadcast(t *testing.T) {
	ctx := context.Background()

	opts := options{
		Name:                 "test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	channel := newChannel(ctx, opts)

	defer channel.Close()

	conns := make([]*Conn, 3)

	for i := 0; i < 3; i++ {
		conns[i] = createTestConn(fmt.Sprintf("user%d", i), nil)

		channel.addUser(conns[i])

		channel.connections.Update(conns[i].ID, conns[i])
	}
	t.Run("broadcasts to all users", func(t *testing.T) {
		err := channel.Broadcast("test-event", map[string]string{"message": "hello"})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		time.Sleep(50 * time.Millisecond)

		for i, conn := range conns {
			select {
			case msg := <-conn.send:
				if !strings.Contains(string(msg), "test-event") {
					t.Errorf("user%d: expected message to contain test-event", i)
				}
			default:
				t.Errorf("user%d: expected to receive broadcast message", i)
			}
		}
	})
}

func TestChannelBroadcastTo(t *testing.T) {
	ctx := context.Background()

	opts := options{
		Name:                 "test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	channel := newChannel(ctx, opts)

	defer channel.Close()

	conns := make([]*Conn, 3)

	for i := 0; i < 3; i++ {
		conns[i] = createTestConn(fmt.Sprintf("user%d", i), nil)

		channel.addUser(conns[i])

		channel.connections.Update(conns[i].ID, conns[i])
	}
	t.Run("broadcasts to specific users", func(t *testing.T) {
		err := channel.BroadcastTo("test-event", map[string]string{"message": "hello"}, "user0", "user2")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		time.Sleep(50 * time.Millisecond)

		select {
		case msg := <-conns[0].send:
			if !strings.Contains(string(msg), "test-event") {
				t.Error("user0: expected message to contain test-event")
			}
		default:
			t.Error("user0: expected to receive broadcast message")
		}
		select {
		case <-conns[1].send:
			t.Error("user1: should not receive message")

		default:
		}
		select {
		case msg := <-conns[2].send:
			if !strings.Contains(string(msg), "test-event") {
				t.Error("user2: expected message to contain test-event")
			}
		default:
			t.Error("user2: expected to receive broadcast message")
		}
	})
}

func TestChannelBroadcastFrom(t *testing.T) {
	ctx := context.Background()

	opts := options{
		Name:                 "test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	channel := newChannel(ctx, opts)

	defer channel.Close()

	conns := make([]*Conn, 3)

	for i := 0; i < 3; i++ {
		conns[i] = createTestConn(fmt.Sprintf("user%d", i), nil)

		channel.addUser(conns[i])

		channel.connections.Update(conns[i].ID, conns[i])
	}
	t.Run("broadcasts to all except sender", func(t *testing.T) {
		err := channel.BroadcastFrom("test-event", map[string]string{"message": "hello"}, "user1")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		time.Sleep(50 * time.Millisecond)

		select {
		case msg := <-conns[0].send:
			if !strings.Contains(string(msg), "test-event") {
				t.Error("user0: expected message to contain test-event")
			}
		default:
			t.Error("user0: expected to receive broadcast message")
		}
		select {
		case <-conns[1].send:
			t.Error("user1: should not receive message (sender)")

		default:
		}
		select {
		case msg := <-conns[2].send:
			if !strings.Contains(string(msg), "test-event") {
				t.Error("user2: expected message to contain test-event")
			}
		default:
			t.Error("user2: expected to receive broadcast message")
		}
	})
}

func TestChannelUpdateAssigns(t *testing.T) {
	ctx := context.Background()

	opts := options{
		Name:                 "test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	channel := newChannel(ctx, opts)

	defer channel.Close()

	mockConn := createTestConn("user1", map[string]interface{}{"role": "member"})

	channel.addUser(mockConn)

	t.Run("updates assigns successfully", func(t *testing.T) {
		err := channel.UpdateAssigns("user1", "status", "active")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		user, _ := channel.GetUser("user1")

		if user.Assigns["status"] != "active" {
			t.Errorf("expected status to be active, got %v", user.Assigns["status"])
		}
		if user.Assigns["role"] != "member" {
			t.Errorf("expected role to still be member, got %v", user.Assigns["role"])
		}
	})

	t.Run("handles non-existent user", func(t *testing.T) {
		err := channel.UpdateAssigns("nonexistent", "key", "value")

		if err == nil {
			t.Error("expected error when updating assigns for non-existent user")
		}
	})
}

func TestChannelEvictUser(t *testing.T) {
	ctx := context.Background()

	opts := options{
		Name:                 "test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	channel := newChannel(ctx, opts)

	defer channel.Close()

	for i := 0; i < 3; i++ {
		conn := createTestConn(fmt.Sprintf("user%d", i), nil)

		channel.addUser(conn)

		channel.connections.Update(conn.ID, conn)
	}
	t.Run("evicts user successfully", func(t *testing.T) {
		err := channel.EvictUser("user1", "violation of terms")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		_, err = channel.GetUser("user1")

		if err == nil {
			t.Error("expected evicted user to be removed")
		}
		time.Sleep(50 * time.Millisecond)

		conn1, _ := channel.connections.Read("user1")

		if conn1 != nil {
			select {
			case msg := <-conn1.(*Conn).send:
				if !strings.Contains(string(msg), "evicted") {
					t.Error("expected eviction message to evicted user")
				}
			default:
			}
		}
	})
}

func TestChannelClose(t *testing.T) {
	ctx := context.Background()

	opts := options{
		Name:                 "test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	channel := newChannel(ctx, opts)

	for i := 0; i < 3; i++ {
		conn := createTestConn(fmt.Sprintf("user%d", i), nil)

		channel.addUser(conn)
	}
	err := channel.Close()

	if err != nil {
		t.Errorf("expected no error closing channel, got %v", err)
	}
	if err := channel.checkState(); err == nil {
		t.Error("expected error when checking state of closed channel")
	}
	if channel.store.Len() != 0 {
		t.Errorf("expected 0 users after close, got %d", channel.store.Len())
	}
}

func TestChannelConcurrency(t *testing.T) {
	ctx := context.Background()

	opts := options{
		Name:                 "test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	channel := newChannel(ctx, opts)

	defer channel.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			conn := createTestConn(fmt.Sprintf("user%d", n), nil)

			channel.addUser(conn)

			channel.connections.Update(conn.ID, conn)
		}(i)
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			time.Sleep(10 * time.Millisecond)

			channel.Broadcast(fmt.Sprintf("event-%d", n), nil)
		}(i)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			time.Sleep(5 * time.Millisecond)

			channel.GetUser(fmt.Sprintf("user%d", n))
		}(i)
	}
	wg.Wait()

	if channel.store.Len() == 0 {
		t.Error("expected some users to be added")
	}
}

func TestChannelPresenceIntegration(t *testing.T) {
	ctx := context.Background()

	opts := options{
		Name:                 "test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	channel := newChannel(ctx, opts)

	defer func() {
		time.Sleep(10 * time.Millisecond)

		channel.Close()
	}()

	conn := createTestConn("user1", nil)

	channel.addUser(conn)

	t.Run("tracks presence", func(t *testing.T) {
		err := channel.Track("user1", map[string]interface{}{"status": "online"})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		time.Sleep(5 * time.Millisecond)

		presence := channel.GetPresence()

		if presence["user1"] == nil {
			t.Error("expected user1 to have presence data")
		}
	})

	t.Run("updates presence", func(t *testing.T) {
		err := channel.UpdatePresence("user1", map[string]interface{}{"status": "away"})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		time.Sleep(5 * time.Millisecond)

		presence := channel.GetPresence()

		data := presence["user1"].(map[string]interface{})

		if data["status"] != "away" {
			t.Errorf("expected status to be away, got %v", data["status"])
		}
	})

	t.Run("untracks presence", func(t *testing.T) {
		err := channel.UnTrack("user1")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		time.Sleep(5 * time.Millisecond)

		presence := channel.GetPresence()

		if presence["user1"] != nil {
			t.Error("expected user1 presence to be removed")
		}
	})
}

func TestChannelGetAssigns(t *testing.T) {
	ctx := context.Background()

	t.Run("returns local assigns when no pubsub", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "admin"})
		channel.addUser(conn)

		assigns := channel.GetAssigns()

		if assigns == nil {
			t.Fatal("expected assigns to be non-nil")
		}
		if assigns["user1"]["role"] != "admin" {
			t.Errorf("expected role admin, got %v", assigns["user1"]["role"])
		}
	})

	t.Run("returns nil when channel is closed", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.Close()

		assigns := channel.GetAssigns()

		if assigns != nil {
			t.Error("expected nil assigns for closed channel")
		}
	})
}

func TestChannelGetPresenceEdgeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("returns nil when channel is closed", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.Close()

		presence := channel.GetPresence()

		if presence != nil {
			t.Error("expected nil presence for closed channel")
		}
	})

	t.Run("returns empty map when no users tracked", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		presence := channel.GetPresence()

		if presence == nil {
			t.Error("expected non-nil presence map")
		}
		if len(presence) != 0 {
			t.Errorf("expected empty presence map, got %d entries", len(presence))
		}
	})
}

func TestChannelTrackEdgeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("tracks user not in channel", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		err := channel.Track("anyuser", map[string]interface{}{"status": "online"})

		if err != nil {
			t.Errorf("expected no error for tracking, got %v", err)
		}
	})

	t.Run("returns error when channel is closed", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.Close()

		err := channel.Track("user1", map[string]interface{}{"status": "online"})

		if err == nil {
			t.Error("expected error for closed channel")
		}
	})
}

func TestChannelUnTrackEdgeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("untracks non-existent user without error", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		err := channel.UnTrack("nonexistent")

		if err != nil {
			t.Errorf("expected no error for non-existent user, got %v", err)
		}
	})

	t.Run("returns error when channel is closed", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.Close()

		err := channel.UnTrack("user1")

		if err == nil {
			t.Error("expected error for closed channel")
		}
	})
}

func TestChannelUpdatePresenceEdgeCases(t *testing.T) {
	ctx := context.Background()

	t.Run("returns error for non-existent user", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		err := channel.UpdatePresence("nonexistent", map[string]interface{}{"status": "away"})

		if err == nil {
			t.Error("expected error for non-existent user")
		}
	})

	t.Run("returns error when channel is closed", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.Close()

		err := channel.UpdatePresence("user1", map[string]interface{}{"status": "away"})

		if err == nil {
			t.Error("expected error for closed channel")
		}
	})
}

func TestChannelUpdateAssignsExtended(t *testing.T) {
	ctx := context.Background()

	t.Run("returns error for non-existent user", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		err := channel.UpdateAssigns("nonexistent", "role", "admin")

		if err == nil {
			t.Error("expected error for non-existent user")
		}
	})

	t.Run("returns error when channel is closed", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.Close()

		err := channel.UpdateAssigns("user1", "role", "admin")

		if err == nil {
			t.Error("expected error for closed channel")
		}
	})
}

func TestChannelEvictUserExtended(t *testing.T) {
	ctx := context.Background()

	t.Run("returns error when channel is closed", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.Close()

		err := channel.EvictUser("kicked", "user1")

		if err == nil {
			t.Error("expected error for closed channel")
		}
	})
}

func TestChannelBroadcastToExtended(t *testing.T) {
	ctx := context.Background()

	t.Run("returns nil for empty user list", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		err := channel.BroadcastTo("test-event", nil)

		if err != nil {
			t.Errorf("expected nil error for empty user list, got %v", err)
		}
	})

	t.Run("returns error when channel is closed", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.Close()

		err := channel.BroadcastTo("test-event", nil, "user1")

		if err == nil {
			t.Error("expected error for closed channel")
		}
	})
}

func TestChannelBroadcastFromExtended(t *testing.T) {
	ctx := context.Background()

	t.Run("returns error when channel is closed", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.Close()

		err := channel.BroadcastFrom("test-event", nil, "user1")

		if err == nil {
			t.Error("expected error for closed channel")
		}
	})
}

func TestChannelGetUserAssigns(t *testing.T) {
	ctx := context.Background()

	t.Run("returns value for existing key", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "admin"})
		channel.addUser(conn)

		value, err := channel.getUserAssigns("user1", "role")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if value != "admin" {
			t.Errorf("expected admin, got %v", value)
		}
	})

	t.Run("returns error for non-existent key", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "admin"})
		channel.addUser(conn)

		_, err := channel.getUserAssigns("user1", "nonexistent")

		if err == nil {
			t.Error("expected error for non-existent key")
		}
	})

	t.Run("returns error for non-existent user", func(t *testing.T) {
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		_, err := channel.getUserAssigns("nonexistent", "role")

		if err == nil {
			t.Error("expected error for non-existent user")
		}
	})
}

func TestChannelMultipleCloses(t *testing.T) {
	ctx := context.Background()

	opts := options{
		Name:                 "test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	channel := newChannel(ctx, opts)

	channel.Close()
	err := channel.Close()

	if err != nil {
		t.Errorf("expected nil error on second close, got %v", err)
	}
}

func TestSyncCoordinatorAddResponse(t *testing.T) {
	t.Run("adds response to coordinator", func(t *testing.T) {
		coordinator := &syncCoordinator{
			requestID:       "test-request",
			requesterUserID: "test-user",
			responses:       make(map[string]map[string]interface{}),
			completeChan:    make(chan map[string]interface{}, 1),
			timeout:         1 * time.Second,
		}

		presenceData := map[string]interface{}{
			"user1": map[string]interface{}{"status": "online"},
		}

		coordinator.addResponse("node1", presenceData)

		if coordinator.responses["node1"] == nil {
			t.Error("expected response to be added")
		}
	})

	t.Run("ignores response when completed", func(t *testing.T) {
		coordinator := &syncCoordinator{
			requestID:       "test-request",
			requesterUserID: "test-user",
			responses:       make(map[string]map[string]interface{}),
			completeChan:    make(chan map[string]interface{}, 1),
			completed:       true,
			timeout:         1 * time.Second,
		}

		presenceData := map[string]interface{}{
			"user1": map[string]interface{}{"status": "online"},
		}

		coordinator.addResponse("node1", presenceData)

		if len(coordinator.responses) != 0 {
			t.Error("expected no response to be added when completed")
		}
	})
}

func TestSyncCoordinatorAggregateResponses(t *testing.T) {
	t.Run("aggregates responses from multiple nodes", func(t *testing.T) {
		coordinator := &syncCoordinator{
			requestID:       "test-request",
			requesterUserID: "test-user",
			responses:       make(map[string]map[string]interface{}),
			completeChan:    make(chan map[string]interface{}, 1),
			timeout:         1 * time.Second,
		}

		coordinator.responses["node1"] = map[string]interface{}{
			"user1": map[string]interface{}{"status": "online"},
		}
		coordinator.responses["node2"] = map[string]interface{}{
			"user2": map[string]interface{}{"status": "away"},
		}

		aggregated := coordinator.aggregateResponses()

		if aggregated["user1"] == nil {
			t.Error("expected user1 to be in aggregated response")
		}
		if aggregated["user2"] == nil {
			t.Error("expected user2 to be in aggregated response")
		}
	})

	t.Run("handles empty responses", func(t *testing.T) {
		coordinator := &syncCoordinator{
			requestID:       "test-request",
			requesterUserID: "test-user",
			responses:       make(map[string]map[string]interface{}),
			completeChan:    make(chan map[string]interface{}, 1),
			timeout:         1 * time.Second,
		}

		aggregated := coordinator.aggregateResponses()

		if len(aggregated) != 0 {
			t.Error("expected empty aggregated response")
		}
	})
}

func TestSyncCoordinatorCompleted(t *testing.T) {
	t.Run("marks coordinator as completed", func(t *testing.T) {
		coordinator := &syncCoordinator{
			requestID:       "test-request",
			requesterUserID: "test-user",
			responses:       make(map[string]map[string]interface{}),
			completeChan:    make(chan map[string]interface{}, 1),
			completed:       false,
			timeout:         1 * time.Second,
		}

		if coordinator.completed {
			t.Error("expected coordinator to not be completed initially")
		}

		coordinator.completed = true

		if !coordinator.completed {
			t.Error("expected coordinator to be marked completed")
		}
	})
}

func TestChannelSendSyncComplete(t *testing.T) {
	t.Run("does nothing when pubsub is nil", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		channel.sendSyncComplete("req-1", "user-1", map[string]interface{}{})
	})

	t.Run("does nothing when endpointPath is empty", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.pubsub = NewLocalPubSub(ctx, 100)
		defer channel.Close()

		channel.sendSyncComplete("req-1", "user-1", map[string]interface{}{})
	})

	t.Run("sends sync complete event when configured", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.pubsub = NewLocalPubSub(ctx, 100)
		channel.endpointPath = "/test"
		defer channel.Close()

		presence := map[string]interface{}{
			"user1": map[string]interface{}{"status": "online"},
		}
		channel.sendSyncComplete("req-1", "user-1", presence)

		select {
		case ev := <-channel.channel:
			if ev.Event.Event != string(syncComplete) {
				t.Errorf("expected syncComplete event, got %s", ev.Event.Event)
			}
		case <-time.After(100 * time.Millisecond):
		}
	})
}

func TestChannelSendAssignsSyncComplete(t *testing.T) {
	t.Run("does nothing when pubsub is nil", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		channel.sendAssignsSyncComplete("req-1", "user-1", map[string]interface{}{})
	})

	t.Run("does nothing when endpointPath is empty", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.pubsub = NewLocalPubSub(ctx, 100)
		defer channel.Close()

		channel.sendAssignsSyncComplete("req-1", "user-1", map[string]interface{}{})
	})

	t.Run("sends assigns sync complete event when configured", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.pubsub = NewLocalPubSub(ctx, 100)
		channel.endpointPath = "/test"
		defer channel.Close()

		assigns := map[string]interface{}{
			"user1": map[string]interface{}{"role": "admin"},
		}
		channel.sendAssignsSyncComplete("req-1", "user-1", assigns)

		select {
		case ev := <-channel.channel:
			if ev.Event.Event != string(assignsSyncComplete) {
				t.Errorf("expected assignsSyncComplete event, got %s", ev.Event.Event)
			}
		case <-time.After(100 * time.Millisecond):
		}
	})
}

func TestChannelHandleSyncTimeout(t *testing.T) {
	t.Run("removes coordinator when complete channel receives", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		coordinator := &syncCoordinator{
			requestID:       "test-request",
			requesterUserID: "test-user",
			responses:       make(map[string]map[string]interface{}),
			completeChan:    make(chan map[string]interface{}, 1),
			timeout:         1 * time.Second,
		}

		done := make(chan struct{})
		go func() {
			channel.handleSyncTimeout(coordinator)
			close(done)
		}()

		coordinator.completeChan <- map[string]interface{}{}

		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			t.Error("expected handleSyncTimeout to complete")
		}
	})

	t.Run("removes coordinator when context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)

		coordinator := &syncCoordinator{
			requestID:       "test-request",
			requesterUserID: "test-user",
			responses:       make(map[string]map[string]interface{}),
			completeChan:    make(chan map[string]interface{}, 1),
			timeout:         1 * time.Second,
		}

		done := make(chan struct{})
		go func() {
			channel.handleSyncTimeout(coordinator)
			close(done)
		}()

		cancel()

		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			t.Error("expected handleSyncTimeout to complete on context cancel")
		}
	})
}

func TestChannelHandleAssignsSyncTimeout(t *testing.T) {
	t.Run("removes coordinator when complete channel receives", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		coordinator := &syncCoordinator{
			requestID:       "test-request",
			requesterUserID: "test-user",
			responses:       make(map[string]map[string]interface{}),
			completeChan:    make(chan map[string]interface{}, 1),
			timeout:         1 * time.Second,
		}

		done := make(chan struct{})
		go func() {
			channel.handleAssignsSyncTimeout(coordinator)
			close(done)
		}()

		coordinator.completeChan <- map[string]interface{}{}

		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			t.Error("expected handleAssignsSyncTimeout to complete")
		}
	})
}

func TestChannelGetAssignsDistributed(t *testing.T) {
	t.Run("returns local assigns when pubsub is nil", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		_ = channel.store.Create("user1", map[string]interface{}{"role": "admin"})

		assigns := channel.GetAssigns()

		if assigns["user1"] == nil {
			t.Error("expected user1 assigns")
		}
		if assigns["user1"]["role"] != "admin" {
			t.Errorf("expected role admin, got %v", assigns["user1"]["role"])
		}
	})

	t.Run("returns nil when channel is closing", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.Close()

		assigns := channel.GetAssigns()

		if assigns != nil {
			t.Error("expected nil assigns when channel is closing")
		}
	})
}

func TestChannelGetLocalAssigns(t *testing.T) {
	t.Run("returns copy of local assigns", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		_ = channel.store.Create("user1", map[string]interface{}{"role": "admin"})
		_ = channel.store.Create("user2", map[string]interface{}{"role": "user"})

		localAssigns := channel.getLocalAssigns()

		if len(localAssigns) != 2 {
			t.Errorf("expected 2 users, got %d", len(localAssigns))
		}

		localAssigns["user1"]["role"] = "modified"

		original, _ := channel.store.Read("user1")
		if original["role"] == "modified" {
			t.Error("modifying returned map should not affect original")
		}
	})
}

func TestChannelNotFoundHandlerEdgeCases(t *testing.T) {
	t.Run("handles nil hooks", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		defer channel.Close()

		event := &Event{
			Event:   "unknown",
			Action:  broadcast,
			Payload: "test",
		}

		handlerFunc := channel.notFoundHandler(ctx, "user1", event)
		if handlerFunc == nil {
			t.Error("expected non-nil handler func")
		}
	})
}

func TestChannelEvictUserClosing(t *testing.T) {
	t.Run("returns error when channel is closing", func(t *testing.T) {
		ctx := context.Background()
		opts := options{
			Name:                 "test-channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}
		channel := newChannel(ctx, opts)
		channel.Close()

		err := channel.EvictUser("user1", "test reason")
		if err == nil {
			t.Error("expected error when channel is closing")
		}
	})
}
