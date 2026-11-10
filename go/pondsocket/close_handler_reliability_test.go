package pondsocket

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestLastLeaveDoesNotDestroyConcurrentRejoin(t *testing.T) {
	leaveStarted := make(chan struct{})
	releaseLeave := make(chan struct{})
	var destroyed atomic.Bool
	leave := LeaveEventHandler(func(*LeaveContext) error {
		close(leaveStarted)
		<-releaseLeave
		return nil
	})
	channel := newChannel(context.Background(), options{
		Name:                 "rejoin-during-leave",
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		InternalQueueTimeout: time.Second,
		Leave:                &leave,
		OnDestroy: func() error {
			destroyed.Store(true)
			return nil
		},
	})
	defer channel.Close()
	original := createTestConn("original", nil)
	if err := channel.addUser(original); err != nil {
		t.Fatal(err)
	}

	removed := make(chan error, 1)
	go func() { removed <- channel.RemoveUser(original.GetID(), "leave") }()
	select {
	case <-leaveStarted:
	case <-time.After(time.Second):
		t.Fatal("leave handler did not start")
	}

	replacement := createTestConn("replacement", nil)
	if err := channel.addUser(replacement); err != nil {
		t.Fatalf("concurrent rejoin failed: %v", err)
	}
	close(releaseLeave)
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	if destroyed.Load() {
		t.Fatal("last-user cleanup destroyed a channel with a new member")
	}
	if _, err := channel.GetUser(replacement.GetID()); err != nil {
		t.Fatalf("replacement member was removed: %v", err)
	}
}

func TestFailedProvisionalJoinDoesNotEmitLeave(t *testing.T) {
	var leaves atomic.Int32
	leave := LeaveEventHandler(func(*LeaveContext) error {
		leaves.Add(1)
		return nil
	})
	shared := newOutboxes()
	joining := newOrderedTestTransport("provisional")
	replacement := newOrderedTestTransport("provisional")
	shared.register(replacement)
	channel := newChannel(context.Background(), options{
		Name:                 "failed-provisional",
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		InternalQueueTimeout: time.Second,
		Outboxes:             shared,
		Leave:                &leave,
	})
	defer channel.Close()
	ack := Event{Action: system, ChannelName: channel.name, Event: string(acknowledgeEvent)}

	if err := channel.addUserAcknowledged(joining, &ack); err == nil {
		t.Fatal("superseded transport unexpectedly completed its join")
	}
	if leaves.Load() != 0 {
		t.Fatal("failed provisional join invoked the leave handler")
	}
	if channel.store.Len() != 0 || channel.connections.Len() != 0 || channel.memberships.Len() != 0 {
		t.Fatal("failed provisional join retained channel state")
	}

	join := newJoinContext(context.Background(), channel, nil, joining, &Event{RequestId: "failed-join"})
	join.Accept()
	if join.accepted {
		t.Fatal("failed Accept remained marked as accepted")
	}
}

func TestConnCloseRecoversHandlerPanicAndFinishesCleanup(t *testing.T) {
	conn := createTestConn("panic-close", nil)
	var subsequentRan atomic.Bool
	conn.OnClose(func(Transport) error {
		panic("broken close callback")
	})
	conn.OnClose(func(Transport) error {
		subsequentRan.Store(true)
		return nil
	})

	conn.Close()

	if !subsequentRan.Load() {
		t.Fatal("panic prevented a later close handler from running")
	}
	select {
	case <-conn.closeChan:
	default:
		t.Fatal("connection did not signal completed cleanup")
	}
}

func TestSSECloseRecoversHandlerPanicAndWaitsForCleanup(t *testing.T) {
	conn, _ := createTestSSEConn("panic-sse-close", nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var subsequentRan atomic.Bool
	conn.OnClose(func(Transport) error {
		panic("broken SSE close callback")
	})
	conn.OnClose(func(Transport) error {
		close(started)
		<-release
		subsequentRan.Store(true)
		return nil
	})

	go conn.Close()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("close handler did not start")
	}

	waited := make(chan struct{})
	go func() {
		conn.Wait()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("Wait returned before close handlers completed")
	case <-time.After(20 * time.Millisecond):
	}

	close(release)
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after close handlers completed")
	}
	if !subsequentRan.Load() {
		t.Fatal("panic prevented a later SSE close handler from running")
	}
}
