package pondsocket

import (
	"context"
	"errors"
	"fmt"
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

func (o *outboxes) get(conn Transport) *outbox {
	o.mutex.Lock()
	defer o.mutex.Unlock()

	box, ok := o.byID[conn.GetID()]
	if !ok {
		box = &outbox{conn: conn, space: make(chan struct{}, 1)}
		o.byID[conn.GetID()] = box
	}
	return box
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
	b.mutex.Unlock()
	if len(dropped) > 0 {
		select {
		case b.space <- struct{}{}:
		default:
		}
	}

	for _, item := range dropped {
		if item.done != nil {
			item.done <- context.Canceled
			close(item.done)
		}
	}
}

func (b *outbox) discardAll() {
	b.mutex.Lock()
	items := b.items
	b.items = nil
	b.mutex.Unlock()
	select {
	case b.space <- struct{}{}:
	default:
	}

	for _, item := range items {
		if item.done != nil {
			item.done <- context.Canceled
			close(item.done)
		}
	}
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
		box.mutex.Unlock()
		if timer == nil {
			timer = time.NewTimer(timeout)
			timeoutC = timer.C
		}

		select {
		case <-box.space:
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
		box.mutex.Unlock()
		select {
		case box.space <- struct{}{}:
		default:
		}

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
