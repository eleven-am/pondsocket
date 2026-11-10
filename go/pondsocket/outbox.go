package pondsocket

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"
)

type outboxItem struct {
	channel *Channel
	event   Event
	done    chan error
}

type outbox struct {
	mutex    sync.Mutex
	conn     Transport
	items    []outboxItem
	space    chan struct{}
	draining bool
	retired  bool
}

type outboxes struct {
	mutex sync.Mutex
	byID  map[string]*outbox
}

func newOutboxes() *outboxes {
	return &outboxes{byID: make(map[string]*outbox)}
}

func newOutbox(conn Transport) *outbox {
	return &outbox{conn: conn, space: make(chan struct{})}
}

func sameTransport(left, right Transport) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	if leftValue.Type() != rightValue.Type() || !leftValue.Type().Comparable() {
		if leftValue.Kind() != rightValue.Kind() {
			return false
		}
		switch leftValue.Kind() {
		case reflect.Chan, reflect.Map, reflect.Pointer, reflect.UnsafePointer:
			return leftValue.Pointer() == rightValue.Pointer()
		case reflect.Slice:
			return leftValue.Pointer() == rightValue.Pointer() && leftValue.Len() == rightValue.Len() && leftValue.Cap() == rightValue.Cap()
		default:
			return false
		}
	}
	return leftValue.Interface() == rightValue.Interface()
}

func (o *outboxes) get(conn Transport) *outbox {
	o.mutex.Lock()
	box, ok := o.byID[conn.GetID()]
	if ok && !box.retired {
		o.mutex.Unlock()
		return box
	}
	box = newOutbox(conn)
	o.byID[conn.GetID()] = box
	o.mutex.Unlock()
	return box
}

func (o *outboxes) register(conn Transport) {
	o.mutex.Lock()
	box, ok := o.byID[conn.GetID()]
	if ok && sameTransport(box.conn, conn) && !box.retired {
		o.mutex.Unlock()
		return
	}

	var dropped []outboxItem
	if ok {
		box.mutex.Lock()
		box.retired = true
		dropped = box.items
		box.items = nil
		box.signalSpaceLocked()
		box.mutex.Unlock()
	}
	box = newOutbox(conn)
	o.byID[conn.GetID()] = box
	o.mutex.Unlock()

	completeDropped(dropped)
}

func (o *outboxes) unregister(conn Transport) {
	o.mutex.Lock()
	box, ok := o.byID[conn.GetID()]
	if !ok || !sameTransport(box.conn, conn) {
		o.mutex.Unlock()
		return
	}
	box.mutex.Lock()
	box.retired = true
	dropped := box.items
	box.items = nil
	box.signalSpaceLocked()
	box.mutex.Unlock()
	delete(o.byID, conn.GetID())
	o.mutex.Unlock()
	completeDropped(dropped)
}

func (b *outbox) signalSpaceLocked() {
	close(b.space)
	b.space = make(chan struct{})
}

func completeDropped(items []outboxItem) {
	for _, item := range items {
		if item.done != nil {
			item.done <- context.Canceled
			close(item.done)
		}
	}
}

func (o *outboxes) find(id string) *outbox {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	return o.byID[id]
}

func (o *outboxes) retireIfIdle(box *outbox) bool {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	box.mutex.Lock()
	defer box.mutex.Unlock()

	if len(box.items) > 0 {
		return false
	}
	box.draining = false
	box.retired = true
	box.signalSpaceLocked()
	if o.byID[box.conn.GetID()] == box {
		delete(o.byID, box.conn.GetID())
	}
	return true
}

func (b *outbox) purge(channel *Channel) {
	b.mutex.Lock()
	kept := b.items[:0]
	var dropped []outboxItem
	for _, item := range b.items {
		if item.channel == channel {
			dropped = append(dropped, item)
			continue
		}
		kept = append(kept, item)
	}
	for i := len(kept); i < len(b.items); i++ {
		b.items[i] = outboxItem{}
	}
	b.items = kept
	if len(dropped) > 0 {
		b.signalSpaceLocked()
	}
	b.mutex.Unlock()

	completeDropped(dropped)
}

func (b *outbox) discardAll() {
	b.mutex.Lock()
	items := b.items
	b.items = nil
	b.signalSpaceLocked()
	b.mutex.Unlock()

	completeDropped(items)
}

func (c *Channel) outboxLimit() int {
	if c.sendBuffer > 0 {
		return c.sendBuffer
	}
	return DefaultOptions().SendChannelBuffer
}

func (c *Channel) enqueue(conn Transport, event Event, done chan error) error {
	if !conn.IsActive() {
		err := fmt.Errorf("connection %s is not active", conn.GetID())
		if done != nil {
			done <- err
			close(done)
		}
		return err
	}
	timeout := c.internalQueueTimeout
	if timeout <= 0 {
		timeout = DefaultOptions().InternalQueueTimeout
	}
	var timer *time.Timer
	var timeoutC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	var box *outbox
	for {
		box = c.outboxes.get(conn)
		box.mutex.Lock()
		if !sameTransport(box.conn, conn) {
			box.mutex.Unlock()
			err := fmt.Errorf("connection %s was superseded by a replacement transport", conn.GetID())
			if done != nil {
				done <- err
				close(done)
			}
			return err
		}
		if box.retired {
			box.mutex.Unlock()
			continue
		}
		if len(box.items) < c.outboxLimit() {
			box.items = append(box.items, outboxItem{channel: c, event: event, done: done})
			start := !box.draining
			box.draining = true
			box.mutex.Unlock()

			if start {
				go c.outboxes.drain(box)
			}
			return nil
		}
		space := box.space
		box.mutex.Unlock()
		if timer == nil {
			timer = time.NewTimer(timeout)
			timeoutC = timer.C
		}

		select {
		case <-space:
			continue
		case <-c.ctx.Done():
			if done != nil {
				done <- c.ctx.Err()
				close(done)
			}
			return c.ctx.Err()
		case <-timeoutC:
			err := fmt.Errorf("connection %s did not drain its outbound queue within %s", conn.GetID(), timeout)
			if done != nil {
				done <- err
				close(done)
			}
			c.reportError("channel_slow_consumer", err)
			go conn.Close()
			return err
		}
	}
}

func (c *Channel) deliver(event Event, recipients *array[string]) error {
	var errs []error
	for _, conn := range c.connections.GetByKeys(recipients.items...).items {
		if err := c.enqueue(conn, event, nil); err != nil {
			errs = append(errs, err)
		}
	}
	return combine(errs...)
}

func (c *Channel) deliverAndWait(conn Transport, event Event) error {
	done := make(chan error, 1)
	if err := c.enqueue(conn, event, done); err != nil {
		return err
	}
	return <-done
}

func (c *Channel) dropPending(userID string) {
	if box := c.outboxes.find(userID); box != nil {
		box.purge(c)
	}
}

func (o *outboxes) drain(box *outbox) {
	for {
		box.mutex.Lock()
		if len(box.items) == 0 {
			box.mutex.Unlock()
			if o.retireIfIdle(box) {
				return
			}
			continue
		}
		item := box.items[0]
		box.items[0] = outboxItem{}
		box.items = box.items[1:]
		box.signalSpaceLocked()
		box.mutex.Unlock()

		if !box.conn.IsActive() {
			if item.done != nil {
				item.done <- context.Canceled
				close(item.done)
			}
			box.discardAll()
			continue
		}
		item.channel.deliverOne(box.conn, item)
	}
}

func (c *Channel) deliverOne(conn Transport, item outboxItem) {
	var deliveryErr error
	defer func() {
		if r := recover(); r != nil {
			deliveryErr = fmt.Errorf("panic recovered: %v", r)
			c.reportError("channel_dispatch_panic", deliveryErr)
		}
		if item.done != nil {
			item.done <- deliveryErr
			close(item.done)
		}
	}()

	event := item.event
	if err := c.processOutgoing(&event, conn); err != nil {
		deliveryErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		c.reportError("channel_dispatch", err)
	}
}
