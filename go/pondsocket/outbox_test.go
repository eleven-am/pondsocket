package pondsocket

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type orderedTestTransport struct {
	id         string
	active     atomic.Bool
	first      sync.Once
	started    chan struct{}
	release    chan struct{}
	deliveries chan int
	assigns    map[string]interface{}
}

type sliceTransport []*orderedTestTransport

func (t sliceTransport) base() *orderedTestTransport             { return t[0] }
func (t sliceTransport) GetID() string                           { return t.base().GetID() }
func (t sliceTransport) SendJSON(value interface{}) error        { return t.base().SendJSON(value) }
func (t sliceTransport) GetAssign(key string) interface{}        { return t.base().GetAssign(key) }
func (t sliceTransport) SetAssign(key string, value interface{}) { t.base().SetAssign(key, value) }
func (t sliceTransport) GetAssigns() map[string]interface{}      { return t.base().GetAssigns() }
func (t sliceTransport) CloneAssigns() map[string]interface{}    { return t.base().CloneAssigns() }
func (t sliceTransport) IsActive() bool                          { return t.base().IsActive() }
func (t sliceTransport) Close()                                  { t.base().Close() }
func (t sliceTransport) OnClose(handler func(Transport) error)   {}
func (t sliceTransport) OnMessage(func(Event, Transport) error)  {}
func (t sliceTransport) HandleMessages()                         {}
func (t sliceTransport) Type() TransportType                     { return t.base().Type() }
func (t sliceTransport) PushMessage(message []byte) error        { return t.base().PushMessage(message) }

func newOrderedTestTransport(id string) *orderedTestTransport {
	t := &orderedTestTransport{
		id:         id,
		started:    make(chan struct{}),
		release:    make(chan struct{}),
		deliveries: make(chan int, 64),
		assigns:    make(map[string]interface{}),
	}
	t.active.Store(true)
	return t
}

func (t *orderedTestTransport) GetID() string { return t.id }
func (t *orderedTestTransport) SendJSON(value interface{}) error {
	t.first.Do(func() {
		close(t.started)
		<-t.release
	})
	var event Event
	switch typed := value.(type) {
	case Event:
		event = typed
	case *Event:
		event = *typed
	default:
		return fmt.Errorf("expected Event, got %T", value)
	}
	sequence, ok := event.Payload.(int)
	if !ok {
		return fmt.Errorf("expected integer payload, got %T", event.Payload)
	}
	t.deliveries <- sequence
	return nil
}
func (t *orderedTestTransport) GetAssign(key string) interface{} { return t.assigns[key] }
func (t *orderedTestTransport) SetAssign(key string, value interface{}) {
	t.assigns[key] = value
}
func (t *orderedTestTransport) GetAssigns() map[string]interface{} { return t.assigns }
func (t *orderedTestTransport) CloneAssigns() map[string]interface{} {
	clone := make(map[string]interface{}, len(t.assigns))
	for key, value := range t.assigns {
		clone[key] = value
	}
	return clone
}
func (t *orderedTestTransport) IsActive() bool                         { return t.active.Load() }
func (t *orderedTestTransport) Close()                                 { t.active.Store(false) }
func (t *orderedTestTransport) OnClose(func(Transport) error)          {}
func (t *orderedTestTransport) OnMessage(func(Event, Transport) error) {}
func (t *orderedTestTransport) HandleMessages()                        {}
func (t *orderedTestTransport) Type() TransportType                    { return TransportSSE }
func (t *orderedTestTransport) PushMessage([]byte) error               { return nil }

func TestOutboxBackpressuresWithoutReordering(t *testing.T) {
	transport := newOrderedTestTransport("ordered")
	channel := newChannel(context.Background(), options{
		Name:                 "ordered",
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		InternalQueueTimeout: time.Second,
		SendBuffer:           1,
	})
	defer channel.cancel()
	if err := channel.store.Create(transport.id, map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	if err := channel.connections.Create(transport.id, transport); err != nil {
		t.Fatal(err)
	}

	if err := channel.enqueue(transport, Event{Payload: 1}, nil); err != nil {
		t.Fatal(err)
	}
	<-transport.started
	if err := channel.enqueue(transport, Event{Payload: 2}, nil); err != nil {
		t.Fatal(err)
	}

	thirdDone := make(chan error, 1)
	go func() {
		thirdDone <- channel.enqueue(transport, Event{Payload: 3}, nil)
	}()
	select {
	case err := <-thirdDone:
		t.Fatalf("third enqueue did not apply backpressure: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(transport.release)
	if err := <-thirdDone; err != nil {
		t.Fatal(err)
	}
	for want := 1; want <= 3; want++ {
		select {
		case got := <-transport.deliveries:
			if got != want {
				t.Fatalf("delivery order: got %d, want %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for delivery %d", want)
		}
	}
}

func TestOutboxReconnectReplacesStaleTransport(t *testing.T) {
	shared := newOutboxes()
	oldTransport := newOrderedTestTransport("same-id")
	newTransport := newOrderedTestTransport("same-id")
	close(newTransport.release)

	makeChannel := func(name string, transport Transport) *Channel {
		shared.register(transport)
		channel := newChannel(context.Background(), options{
			Name:                 name,
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			InternalQueueTimeout: time.Second,
			SendBuffer:           8,
			Outboxes:             shared,
		})
		if err := channel.store.Create(transport.GetID(), map[string]interface{}{}); err != nil {
			t.Fatal(err)
		}
		if err := channel.connections.Create(transport.GetID(), transport); err != nil {
			t.Fatal(err)
		}
		return channel
	}

	oldChannel := makeChannel("old", oldTransport)
	defer oldChannel.cancel()

	if err := oldChannel.enqueue(oldTransport, Event{Payload: 1}, nil); err != nil {
		t.Fatal(err)
	}
	<-oldTransport.started
	newChannel := makeChannel("new", newTransport)
	defer newChannel.cancel()
	if err := newChannel.enqueue(newTransport, Event{Payload: 2}, nil); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-newTransport.deliveries:
		if got != 2 {
			t.Fatalf("replacement transport received %d, want 2", got)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement transport did not receive its event")
	}
	close(oldTransport.release)
}

func TestOutboxWakesEveryBlockedEnqueuer(t *testing.T) {
	transport := newOrderedTestTransport("many-waiters")
	channel := newChannel(context.Background(), options{
		Name:                 "many-waiters",
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		InternalQueueTimeout: time.Second,
		SendBuffer:           1,
	})
	defer channel.cancel()
	if err := channel.store.Create(transport.id, map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	if err := channel.connections.Create(transport.id, transport); err != nil {
		t.Fatal(err)
	}

	if err := channel.enqueue(transport, Event{Payload: 0}, nil); err != nil {
		t.Fatal(err)
	}
	<-transport.started
	if err := channel.enqueue(transport, Event{Payload: 1}, nil); err != nil {
		t.Fatal(err)
	}

	const waiters = 8
	results := make(chan error, waiters)
	for i := 0; i < waiters; i++ {
		go func(sequence int) {
			results <- channel.enqueue(transport, Event{Payload: sequence}, nil)
		}(i + 2)
	}
	time.Sleep(20 * time.Millisecond)
	close(transport.release)

	for i := 0; i < waiters; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("blocked enqueue failed: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a blocked enqueuer missed the space notification")
		}
	}
}

func TestOutboxSupportsNonComparableTransport(t *testing.T) {
	base := newOrderedTestTransport("slice-transport")
	close(base.release)
	transport := sliceTransport{base}
	shared := newOutboxes()
	shared.register(transport)
	channel := newChannel(context.Background(), options{
		Name:                 "slice-transport",
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		InternalQueueTimeout: time.Second,
		Outboxes:             shared,
	})
	defer channel.cancel()
	if err := channel.store.Create(transport.GetID(), map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	if err := channel.connections.Create(transport.GetID(), transport); err != nil {
		t.Fatal(err)
	}

	if err := channel.enqueue(transport, Event{Payload: 7}, nil); err != nil {
		t.Fatalf("non-comparable transport was rejected: %v", err)
	}
	select {
	case got := <-base.deliveries:
		if got != 7 {
			t.Fatalf("received %d, want 7", got)
		}
	case <-time.After(time.Second):
		t.Fatal("non-comparable transport did not receive its event")
	}
}
