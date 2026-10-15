package pondsocket

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type failingPublishPubSub struct {
	PubSub
}

func (f *failingPublishPubSub) Publish(string, []byte) error {
	return errors.New("publish failed")
}

func waitForMembershipState(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("membership state did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}

func requireUserStateRemoved(t *testing.T, channel *Channel, userID string) {
	t.Helper()
	waitForMembershipState(t, func() bool {
		_, assignsErr := channel.store.Read(userID)
		_, connectionErr := channel.connections.Read(userID)
		_, membershipErr := channel.memberships.Read(userID)
		_, presenceErr := channel.presence.Get(userID)
		return assignsErr != nil && connectionErr != nil && membershipErr != nil && presenceErr != nil
	})
}

func TestMembershipConnectionCloseRemovesAllUserState(t *testing.T) {
	channel := createTestChannel(t)
	defer channel.Close()
	conn := createTestConn("closing-user", map[string]interface{}{"role": "viewer"})

	if err := channel.addUser(conn); err != nil {
		t.Fatal(err)
	}
	if err := channel.trackConnectedUser(conn.GetID(), map[string]interface{}{"online": true}); err != nil {
		t.Fatal(err)
	}

	conn.Close()
	requireUserStateRemoved(t, channel, conn.GetID())
}

func TestMembershipClosedBeforeTrackingCannotLeavePresence(t *testing.T) {
	channel := createTestChannel(t)
	defer channel.Close()
	conn := createTestConn("closed-before-track", nil)

	if err := channel.addUser(conn); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	_ = channel.trackConnectedUser(conn.GetID(), map[string]interface{}{"online": true})

	requireUserStateRemoved(t, channel, conn.GetID())
}

func TestClosedConnectionCannotJoinChannel(t *testing.T) {
	channel := createTestChannel(t)
	defer channel.Close()
	conn := createTestConn("closed-before-join", nil)
	conn.Close()

	if err := channel.addUser(conn); err == nil {
		t.Fatal("closed connection joined channel")
	}
	if channel.store.Len() != 0 || channel.connections.Len() != 0 || channel.memberships.Len() != 0 || channel.presence.store.Len() != 0 {
		t.Fatal("closed connection left channel state behind")
	}
}

func TestJoinContextCloseBeforeTrackCannotLeavePresence(t *testing.T) {
	channel := createTestChannel(t)
	defer channel.Close()
	conn := createTestConn("join-close-before-track", nil)
	join := newJoinContext(context.Background(), channel, nil, conn, createTestEvent("join-close", "join", nil))

	join.Accept()
	if join.err != nil {
		t.Fatal(join.err)
	}
	conn.Close()
	join.Track(map[string]interface{}{"online": true})

	requireUserStateRemoved(t, channel, conn.GetID())
}

func TestImmediateReconnectReplacesExpiredMembership(t *testing.T) {
	channel := createTestChannel(t)
	defer channel.Close()
	original := createTestConn("reconnecting-user", map[string]interface{}{"connection": "original"})
	replacement := createTestConn("reconnecting-user", map[string]interface{}{"connection": "replacement"})

	if err := channel.addUser(original); err != nil {
		t.Fatal(err)
	}
	original.Close()
	if err := channel.addUser(replacement); err != nil {
		t.Fatal(err)
	}

	stored, err := channel.connections.Read(replacement.GetID())
	if err != nil {
		t.Fatal(err)
	}
	user, err := channel.GetUser(replacement.GetID())
	if err != nil {
		t.Fatal(err)
	}
	if stored != replacement || user.Assigns["connection"] != "replacement" {
		t.Fatal("replacement connection did not own the membership")
	}
}

func TestExplicitRemovalCancelsMembershipWithReason(t *testing.T) {
	channel := createTestChannel(t)
	defer channel.Close()
	conn := createTestConn("removed-user", nil)

	if err := channel.addUser(conn); err != nil {
		t.Fatal(err)
	}
	membership, err := channel.memberships.Read(conn.GetID())
	if err != nil {
		t.Fatal(err)
	}
	if err := channel.RemoveUser(conn.GetID(), "explicit_leave"); err != nil {
		t.Fatal(err)
	}

	select {
	case <-membership.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("membership context was not cancelled")
	}
	if cause := context.Cause(membership.ctx); cause == nil || cause.Error() != "explicit_leave" {
		t.Fatalf("expected explicit_leave cause, got %v", cause)
	}
	requireUserStateRemoved(t, channel, conn.GetID())
}

func TestChannelCloseCancelsMembershipAndClearsState(t *testing.T) {
	channel := createTestChannel(t)
	conn := createTestConn("channel-close-user", nil)

	if err := channel.addUser(conn); err != nil {
		t.Fatal(err)
	}
	if err := channel.trackConnectedUser(conn.GetID(), map[string]interface{}{"online": true}); err != nil {
		t.Fatal(err)
	}
	membership, err := channel.memberships.Read(conn.GetID())
	if err != nil {
		t.Fatal(err)
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-membership.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("membership context was not cancelled")
	}
	if channel.store.Len() != 0 || channel.connections.Len() != 0 || channel.memberships.Len() != 0 || channel.presence.store.Len() != 0 {
		t.Fatal("channel close left membership state behind")
	}
}

func TestDuplicateJoinPreservesOriginalMembership(t *testing.T) {
	channel := createTestChannel(t)
	defer channel.Close()
	original := createTestConn("duplicate-user", map[string]interface{}{"source": "original"})
	replacement := createTestConn("duplicate-user", map[string]interface{}{"source": "replacement"})

	if err := channel.addUser(original); err != nil {
		t.Fatal(err)
	}
	membership, err := channel.memberships.Read(original.GetID())
	if err != nil {
		t.Fatal(err)
	}
	if err := channel.addUser(replacement); err == nil {
		t.Fatal("duplicate join succeeded")
	}

	storedConnection, err := channel.connections.Read(original.GetID())
	if err != nil {
		t.Fatal(err)
	}
	storedMembership, err := channel.memberships.Read(original.GetID())
	if err != nil {
		t.Fatal(err)
	}
	user, err := channel.GetUser(original.GetID())
	if err != nil {
		t.Fatal(err)
	}
	if storedConnection != original || storedMembership != membership || user.Assigns["source"] != "original" {
		t.Fatal("duplicate join replaced or removed the original membership")
	}
}

func TestConcurrentMembershipCloseAndTrackLeavesNoState(t *testing.T) {
	channel := createTestChannel(t)
	defer channel.Close()
	const users = 64
	connections := make([]*Conn, 0, users)

	for i := 0; i < users; i++ {
		conn := createTestConn(fmt.Sprintf("concurrent-user-%d", i), nil)
		if err := channel.addUser(conn); err != nil {
			t.Fatal(err)
		}
		connections = append(connections, conn)
	}

	var group sync.WaitGroup
	for _, conn := range connections {
		conn := conn
		group.Add(2)
		go func() {
			defer group.Done()
			conn.Close()
		}()
		go func() {
			defer group.Done()
			_ = channel.trackConnectedUser(conn.GetID(), map[string]interface{}{"online": true})
		}()
	}
	group.Wait()

	waitForMembershipState(t, func() bool {
		return channel.store.Len() == 0 && channel.connections.Len() == 0 && channel.memberships.Len() == 0 && channel.presence.store.Len() == 0
	})
}

func TestConnectionCloseRemovesMembershipFromEveryChannel(t *testing.T) {
	first := createTestChannel(t)
	second := createTestChannel(t)
	defer first.Close()
	defer second.Close()
	conn := createTestConn("multi-channel-user", nil)

	if err := first.addUser(conn); err != nil {
		t.Fatal(err)
	}
	if err := second.addUser(conn); err != nil {
		t.Fatal(err)
	}
	if err := first.trackConnectedUser(conn.GetID(), map[string]interface{}{"room": "first"}); err != nil {
		t.Fatal(err)
	}
	if err := second.trackConnectedUser(conn.GetID(), map[string]interface{}{"room": "second"}); err != nil {
		t.Fatal(err)
	}

	conn.Close()
	requireUserStateRemoved(t, first, conn.GetID())
	requireUserStateRemoved(t, second, conn.GetID())
}

func TestConnectionCloseRunsLeaveHandlerOnce(t *testing.T) {
	reasons := make(chan string, 2)
	handler := LeaveEventHandler(func(ctx *LeaveContext) error {
		reasons <- ctx.GetReason()
		return nil
	})
	channel := newChannel(context.Background(), options{
		Name:                 "leave-reason-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		Leave:                &handler,
		InternalQueueTimeout: time.Second,
	})
	defer channel.Close()
	conn := createTestConn("leaving-user", nil)

	if err := channel.addUser(conn); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	requireUserStateRemoved(t, channel, conn.GetID())

	select {
	case reason := <-reasons:
		if reason != "connection_closed" {
			t.Fatalf("expected connection_closed, got %s", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("leave handler was not called")
	}
	select {
	case reason := <-reasons:
		t.Fatalf("leave handler was called twice with %s", reason)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestSSEContextCancellationRemovesMembership(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	recorder := httptest.NewRecorder()
	conn, err := newSSEConn(sseOptions{
		writer:    recorder,
		assigns:   map[string]interface{}{},
		id:        "sse-user",
		options:   DefaultOptions(),
		parentCtx: parent,
	})
	if err != nil {
		t.Fatal(err)
	}
	channel := createTestChannel(t)
	defer channel.Close()

	if err := channel.addUser(conn); err != nil {
		t.Fatal(err)
	}
	if err := channel.trackConnectedUser(conn.GetID(), map[string]interface{}{"online": true}); err != nil {
		t.Fatal(err)
	}
	cancel()

	requireUserStateRemoved(t, channel, conn.GetID())
	select {
	case <-conn.closeChan:
	case <-time.After(time.Second):
		t.Fatal("SSE connection did not close after context cancellation")
	}
}

func TestChannelParentCancellationRemovesMembership(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	channel := newChannel(parent, options{
		Name:                 "parent-cancel-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: time.Second,
	})
	conn := createTestConn("parent-cancel-user", nil)

	if err := channel.addUser(conn); err != nil {
		t.Fatal(err)
	}
	if err := channel.trackConnectedUser(conn.GetID(), map[string]interface{}{"online": true}); err != nil {
		t.Fatal(err)
	}
	cancel()

	requireUserStateRemoved(t, channel, conn.GetID())
}

func TestCleanupFailureStillDestroysEmptyChannel(t *testing.T) {
	parent := context.Background()
	pubsub := &failingPublishPubSub{PubSub: NewLocalPubSub(parent, 16)}
	destroyed := make(chan struct{}, 1)
	channel := newChannel(parent, options{
		Name:                 "cleanup-failure-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		PubSub:               pubsub,
		InternalQueueTimeout: time.Second,
		OnDestroy: func() error {
			destroyed <- struct{}{}
			return nil
		},
	})
	channel.endpointPath = "/test"
	conn := createTestConn("cleanup-failure-user", nil)

	if err := channel.addUser(conn); err != nil {
		t.Fatal(err)
	}
	_ = channel.trackConnectedUser(conn.GetID(), map[string]interface{}{"online": true})
	conn.Close()
	requireUserStateRemoved(t, channel, conn.GetID())

	select {
	case <-destroyed:
	case <-time.After(time.Second):
		t.Fatal("empty channel was not destroyed after cleanup error")
	}
}

func TestConcurrentCloseAndOnCloseRegistrationRunsOnce(t *testing.T) {
	const attempts = 128
	for i := 0; i < attempts; i++ {
		conn := createTestConn(fmt.Sprintf("close-registration-%d", i), nil)
		var calls atomic.Int32
		start := make(chan struct{})
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			conn.OnClose(func(Transport) error {
				calls.Add(1)
				return nil
			})
		}()
		go func() {
			defer group.Done()
			<-start
			conn.Close()
		}()
		close(start)
		group.Wait()
		if calls.Load() != 1 {
			t.Fatalf("close handler ran %d times", calls.Load())
		}
	}
}

func TestConcurrentSSECloseAndOnCloseRegistrationRunsOnce(t *testing.T) {
	const attempts = 64
	for i := 0; i < attempts; i++ {
		parent, cancel := context.WithCancel(context.Background())
		conn, err := newSSEConn(sseOptions{
			writer:    httptest.NewRecorder(),
			assigns:   map[string]interface{}{},
			id:        fmt.Sprintf("sse-close-registration-%d", i),
			options:   DefaultOptions(),
			parentCtx: parent,
		})
		if err != nil {
			t.Fatal(err)
		}
		var calls atomic.Int32
		start := make(chan struct{})
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			conn.OnClose(func(Transport) error {
				calls.Add(1)
				return nil
			})
		}()
		go func() {
			defer group.Done()
			<-start
			conn.Close()
		}()
		close(start)
		group.Wait()
		cancel()
		if calls.Load() != 1 {
			t.Fatalf("SSE close handler ran %d times", calls.Load())
		}
	}
}
