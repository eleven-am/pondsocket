package pondsocket

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Channel represents a PondSocket channel
type Channel struct {
	name           string
	queue          []ClientMessage
	presence       []PondPresence
	params         JoinParams
	publisher      func(ClientMessage)
	eventChan      chan ChannelEvent
	connectionChan <-chan bool
	channelState   chan ChannelState
	currentState   ChannelState
	mu             sync.RWMutex
	stateMu        sync.RWMutex
	ctx            chan struct{}
	closeOnce      sync.Once
	cleanups       []func()
	cleanupsMu     sync.Mutex

	// Subscribers
	eventSubs   map[int]*channelEventSubscriber
	nextEventID int
	eventSubsMu sync.RWMutex
	stateSubs   map[int]*channelStateSubscriber
	nextStateID int
	stateSubsMu sync.RWMutex
}

type channelEventSubscriber struct {
	ch   chan ChannelEvent
	done chan struct{}
	once sync.Once
}

func (s *channelEventSubscriber) close() {
	s.once.Do(func() { close(s.done) })
}

type channelStateSubscriber struct {
	ch   chan ChannelState
	done chan struct{}
	once sync.Once
}

func (s *channelStateSubscriber) close() {
	s.once.Do(func() { close(s.done) })
}

// NewChannel creates a new channel instance
func NewChannel(publisher func(ClientMessage), connectionChan <-chan bool, name string, params JoinParams) *Channel {
	return NewChannelWithCleanup(publisher, connectionChan, nil, name, params)
}

func NewChannelWithCleanup(publisher func(ClientMessage), connectionChan <-chan bool, unsubscribeConnection func(), name string, params JoinParams) *Channel {
	c := &Channel{
		name:           name,
		queue:          make([]ClientMessage, 0),
		presence:       make([]PondPresence, 0),
		params:         params,
		publisher:      publisher,
		eventChan:      make(chan ChannelEvent, 100),
		connectionChan: connectionChan,
		channelState:   make(chan ChannelState, 1),
		currentState:   Idle,
		ctx:            make(chan struct{}),
		eventSubs:      make(map[int]*channelEventSubscriber),
		stateSubs:      make(map[int]*channelStateSubscriber),
	}
	if unsubscribeConnection != nil {
		c.cleanups = append(c.cleanups, unsubscribeConnection)
	}

	// Start event and state dispatchers
	go c.dispatchEvents()
	go c.dispatchStateChanges()
	go c.handleConnectionChanges()

	return c
}

// State returns the current channel state
func (c *Channel) State() ChannelState {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.currentState
}

// Presence returns the current presence list
func (c *Channel) Presence() []PondPresence {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]PondPresence, len(c.presence))
	copy(result, c.presence)
	return result
}

// Join attempts to join the channel
func (c *Channel) Join() {
	if c.State() == Joined {
		return
	}

	message := ClientMessage{
		Action:      JoinChannel,
		Event:       string(JoinChannel),
		Payload:     c.params,
		ChannelName: c.name,
		RequestID:   uuid.New().String(),
	}

	c.setState(Joining)
	c.publish(message)
}

// Leave disconnects from the channel
func (c *Channel) Leave() {
	if c.State() == Closed {
		return
	}
	message := ClientMessage{
		Action:      LeaveChannel,
		Event:       string(LeaveChannel),
		ChannelName: c.name,
		RequestID:   uuid.New().String(),
		Payload:     map[string]interface{}{},
	}

	c.publish(message)
	c.close()
}

// SendMessage sends a message to the channel
func (c *Channel) SendMessage(event string, payload PondMessage) {
	message := ClientMessage{
		Action:      Broadcast,
		Event:       event,
		Payload:     payload,
		ChannelName: c.name,
		RequestID:   uuid.New().String(),
	}

	c.publish(message)
}

// SendForResponse sends a message and waits for a response
func (c *Channel) SendForResponse(event string, payload PondMessage, timeout time.Duration) (<-chan PondMessage, error) {
	requestID := uuid.New().String()
	responseChan := make(chan PondMessage, 1)
	var once sync.Once
	finish := func() {
		once.Do(func() {
			close(responseChan)
		})
	}

	c.eventSubsMu.Lock()
	sub := &channelEventSubscriber{ch: make(chan ChannelEvent, 100), done: make(chan struct{})}
	id := c.nextEventID
	c.nextEventID++
	c.eventSubs[id] = sub
	c.eventSubsMu.Unlock()

	unsubscribe := func() {
		c.eventSubsMu.Lock()
		if existing, ok := c.eventSubs[id]; ok {
			delete(c.eventSubs, id)
			existing.close()
		}
		c.eventSubsMu.Unlock()
	}

	go func() {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		defer unsubscribe()

		for {
			select {
			case received := <-sub.ch:
				if received.Event == event && received.RequestID == requestID {
					select {
					case responseChan <- ToPondMessage(received.Payload):
					default:
					}
					finish()
					return
				}
			case <-sub.done:
				finish()
				return
			case <-timer.C:
				finish()
				return
			case <-c.ctx:
				finish()
				return
			}
		}
	}()

	message := ClientMessage{
		Action:      Broadcast,
		Event:       event,
		Payload:     payload,
		ChannelName: c.name,
		RequestID:   requestID,
	}

	c.publish(message)
	return responseChan, nil
}

// OnMessage subscribes to all messages on the channel
func (c *Channel) OnMessage(callback EventHandler) func() {
	c.eventSubsMu.Lock()
	sub := &channelEventSubscriber{ch: make(chan ChannelEvent, 100), done: make(chan struct{})}
	id := c.nextEventID
	c.nextEventID++
	c.eventSubs[id] = sub
	c.eventSubsMu.Unlock()

	// Start listener goroutine
	go func() {
		for {
			select {
			case event := <-sub.ch:
				if event.Action != Presence {
					payload := ToPondMessage(event.Payload)
					callback(event.Event, payload)
				}
			case <-sub.done:
				return
			case <-c.ctx:
				return
			}
		}
	}()

	// Return unsubscribe function
	return func() {
		c.eventSubsMu.Lock()
		if existing, ok := c.eventSubs[id]; ok {
			delete(c.eventSubs, id)
			existing.close()
		}
		c.eventSubsMu.Unlock()
	}
}

// OnMessageEvent subscribes to specific message events
func (c *Channel) OnMessageEvent(event string, callback func(PondMessage)) func() {
	return c.OnMessage(func(receivedEvent string, message PondMessage) {
		if receivedEvent == event {
			callback(message)
		}
	})
}

// OnJoin subscribes to user join events
func (c *Channel) OnJoin(callback func(PondPresence)) func() {
	return c.subscribeToPresence(func(eventType PresenceEventTypes, payload PresencePayload) {
		if eventType == PresenceJoin {
			callback(payload.Changed)
		}
	})
}

// OnLeave subscribes to user leave events
func (c *Channel) OnLeave(callback func(PondPresence)) func() {
	return c.subscribeToPresence(func(eventType PresenceEventTypes, payload PresencePayload) {
		if eventType == PresenceLeave {
			callback(payload.Changed)
		}
	})
}

// OnPresenceChange subscribes to presence change events
func (c *Channel) OnPresenceChange(callback func(PresencePayload)) func() {
	return c.subscribeToPresence(func(eventType PresenceEventTypes, payload PresencePayload) {
		if eventType == PresenceUpdate {
			callback(payload)
		}
	})
}

// OnUsersChange subscribes to user list changes
func (c *Channel) OnUsersChange(callback func([]PondPresence)) func() {
	return c.subscribeToPresence(func(_ PresenceEventTypes, payload PresencePayload) {
		callback(payload.Presence)
	})
}

// OnChannelStateChange subscribes to channel state changes
func (c *Channel) OnChannelStateChange(callback ChannelStateHandler) func() {
	c.stateSubsMu.Lock()
	sub := &channelStateSubscriber{ch: make(chan ChannelState, 1), done: make(chan struct{})}
	id := c.nextStateID
	c.nextStateID++
	c.stateSubs[id] = sub
	c.stateSubsMu.Unlock()

	// Send current state immediately
	go func() {
		callback(c.State())
	}()

	// Start listener goroutine
	go func() {
		for {
			select {
			case state := <-sub.ch:
				callback(state)
			case <-sub.done:
				return
			case <-c.ctx:
				return
			}
		}
	}()

	// Return unsubscribe function
	return func() {
		c.stateSubsMu.Lock()
		if existing, ok := c.stateSubs[id]; ok {
			delete(c.stateSubs, id)
			existing.close()
		}
		c.stateSubsMu.Unlock()
	}
}

// Acknowledge acknowledges that the channel has been joined
func (c *Channel) Acknowledge(eventChan <-chan ChannelEvent, unsubscribe ...func()) {
	c.setState(Joined)
	var cleanup func()
	if len(unsubscribe) > 0 {
		cleanup = unsubscribe[0]
	}
	c.init(eventChan, cleanup)
	c.emptyQueue()
}

// init initializes the channel's event handling
func (c *Channel) init(eventChan <-chan ChannelEvent, unsubscribe func()) {
	c.cleanupsMu.Lock()
	if unsubscribe != nil {
		c.cleanups = append(c.cleanups, unsubscribe)
	}
	c.cleanupsMu.Unlock()

	go func() {
		for {
			select {
			case event, ok := <-eventChan:
				if !ok {
					return
				}
				if event.ChannelName == c.name && c.State() == Joined {
					select {
					case c.eventChan <- event:
					case <-c.ctx:
						return
					default:
						// Channel is full, skip
					}
				}
			case <-c.ctx:
				return
			}
		}
	}()
}

func (c *Channel) close() {
	c.closeOnce.Do(func() {
		c.setState(Closed)

		c.stateSubsMu.RLock()
		stateSubs := make([]*channelStateSubscriber, 0, len(c.stateSubs))
		for _, sub := range c.stateSubs {
			stateSubs = append(stateSubs, sub)
		}
		c.stateSubsMu.RUnlock()
		for _, sub := range stateSubs {
			safeSendChannelState(sub, Closed)
		}

		close(c.ctx)

		c.cleanupsMu.Lock()
		cleanups := append([]func(){}, c.cleanups...)
		c.cleanups = nil
		c.cleanupsMu.Unlock()
		for _, cleanup := range cleanups {
			cleanup()
		}

		c.eventSubsMu.Lock()
		for id, sub := range c.eventSubs {
			delete(c.eventSubs, id)
			sub.close()
		}
		c.eventSubsMu.Unlock()

		c.stateSubsMu.Lock()
		for id, sub := range c.stateSubs {
			delete(c.stateSubs, id)
			sub.close()
		}
		c.stateSubsMu.Unlock()
	})
}

// setState updates the channel state and notifies subscribers
func (c *Channel) setState(state ChannelState) {
	c.stateMu.Lock()
	oldState := c.currentState
	c.currentState = state
	c.stateMu.Unlock()

	if oldState != state {
		select {
		case c.channelState <- state:
		default:
			// Channel is full, skip
		}
	}
}

// publish sends a message, queuing it if the channel is not joined
func (c *Channel) publish(message ClientMessage) {
	if message.Action == JoinChannel || message.Action == LeaveChannel || c.State() == Joined {
		c.publisher(message)
		return
	}

	c.mu.Lock()
	c.queue = append(c.queue, message)
	c.mu.Unlock()
}

// emptyQueue sends all queued messages
func (c *Channel) emptyQueue() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, message := range c.queue {
		c.publisher(message)
	}
	c.queue = c.queue[:0]
}

// subscribeToPresence subscribes to presence events
func (c *Channel) subscribeToPresence(callback func(PresenceEventTypes, PresencePayload)) func() {
	c.eventSubsMu.Lock()
	sub := &channelEventSubscriber{ch: make(chan ChannelEvent, 100), done: make(chan struct{})}
	id := c.nextEventID
	c.nextEventID++
	c.eventSubs[id] = sub
	c.eventSubsMu.Unlock()

	// Start listener goroutine
	go func() {
		for {
			select {
			case event := <-sub.ch:
				if event.Action == Presence {
					payload, err := event.GetPresencePayload()
					if err == nil {
						callback(PresenceEventTypes(event.Event), *payload)
					}
				}
			case <-sub.done:
				return
			case <-c.ctx:
				return
			}
		}
	}()

	// Return unsubscribe function
	return func() {
		c.eventSubsMu.Lock()
		if existing, ok := c.eventSubs[id]; ok {
			delete(c.eventSubs, id)
			existing.close()
		}
		c.eventSubsMu.Unlock()
	}
}

// dispatchEvents sends events to all subscribers
func (c *Channel) dispatchEvents() {
	for {
		select {
		case event := <-c.eventChan:
			if event.Action == Presence {
				payload, err := event.GetPresencePayload()
				if err == nil {
					c.mu.Lock()
					c.presence = payload.Presence
					c.mu.Unlock()
				}
			}

			c.eventSubsMu.RLock()
			subs := make([]*channelEventSubscriber, 0, len(c.eventSubs))
			for _, sub := range c.eventSubs {
				subs = append(subs, sub)
			}
			c.eventSubsMu.RUnlock()

			for _, sub := range subs {
				safeSendChannelEvent(sub, event)
			}
		case <-c.ctx:
			return
		}
	}
}

// dispatchStateChanges sends state changes to all subscribers
func (c *Channel) dispatchStateChanges() {
	for {
		select {
		case state := <-c.channelState:
			c.stateSubsMu.RLock()
			subs := make([]*channelStateSubscriber, 0, len(c.stateSubs))
			for _, sub := range c.stateSubs {
				subs = append(subs, sub)
			}
			c.stateSubsMu.RUnlock()

			for _, sub := range subs {
				safeSendChannelState(sub, state)
			}
		case <-c.ctx:
			return
		}
	}
}

// handleConnectionChanges handles connection state changes
func (c *Channel) handleConnectionChanges() {
	for {
		select {
		case connected := <-c.connectionChan:
			if connected && c.State() == Stalled {
				// Rejoin
				c.Join()
			} else if !connected && c.State() == Joined {
				c.setState(Stalled)
			}
		case <-c.ctx:
			return
		}
	}
}

func safeSendChannelEvent(sub *channelEventSubscriber, event ChannelEvent) {
	select {
	case <-sub.done:
	case sub.ch <- event:
	default:
	}
}

func safeSendChannelState(sub *channelStateSubscriber, state ChannelState) {
	select {
	case <-sub.done:
	case sub.ch <- state:
	default:
	}
}
