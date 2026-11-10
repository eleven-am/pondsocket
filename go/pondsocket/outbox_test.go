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

func newOrderedTestTransport(id string) *orderedTestTransport {
	t := &orderedTestTransport{
		id:         id,
		started:    make(chan struct{}),
		release:    make(chan struct{}),
		deliveries: make(chan int, 3),
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
