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
		closeHandlers: newArray[func(*Conn) error](),
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
		leaveHandler := LeaveHandler(func(u User) {
			leaveCalledMutex.Lock()

			leaveCalled = true
			leaveCalledMutex.Unlock()
		})

		opts.Leave = &leaveHandler
		channel := newChannel(ctx, opts)

		defer channel.Close()

		mockConn := createTestConn("user1", nil)

		channel.addUser(mockConn)

		err := channel.RemoveUser("user1")

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

		err := channel.RemoveUser("nonexistent")

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

		err := channel.RemoveUser("user1")

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
			case msg := <-conn1.send:
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
