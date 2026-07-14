package pondsocket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// PondClient represents a WebSocket client for PondSocket
type PondClient struct {
	address         *url.URL
	config          *ClientConfig
	disconnecting   bool
	conn            *websocket.Conn
	eventBroadcast  chan ChannelEvent
	connectionState chan bool
	connectionValue bool
	writeMu         sync.Mutex
	channels        map[string]*Channel
	channelsMu      sync.RWMutex
	connMu          sync.RWMutex
	reconnectMu     sync.Mutex
	stateMu         sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
	reconnectCount  int

	// Connection state subscribers
	connectionSubs   map[int]*boolSubscriber
	nextConnSubID    int
	connectionSubsMu sync.RWMutex

	// Event broadcast subscribers
	eventSubs   map[int]*clientEventSubscriber
	nextEventID int
	eventSubsMu sync.RWMutex
}

type boolSubscriber struct {
	ch   chan bool
	done chan struct{}
	once sync.Once
}

func (s *boolSubscriber) close() {
	s.once.Do(func() {
		close(s.done)
	})
}

type clientEventSubscriber struct {
	ch   chan ChannelEvent
	done chan struct{}
	once sync.Once
}

func (s *clientEventSubscriber) close() {
	s.once.Do(func() {
		close(s.done)
	})
}

// NewPondClient creates a new PondSocket client
func NewPondClient(endpoint string, params map[string]interface{}) (*PondClient, error) {
	return NewPondClientWithConfig(endpoint, params, DefaultClientConfig())
}

// NewPondClientWithConfig creates a new PondSocket client with custom configuration
func NewPondClientWithConfig(endpoint string, params map[string]interface{}, config *ClientConfig) (*PondClient, error) {
	address, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}

	if config == nil {
		config = DefaultClientConfig()
	} else {
		cfgCopy := *config
		config = &cfgCopy
	}

	// Set WebSocket protocol based on HTTP scheme
	switch address.Scheme {
	case "http":
		address.Scheme = "ws"
	case "https":
		address.Scheme = "wss"
	case "ws", "wss":
		// Already correct
	default:
		return nil, fmt.Errorf("unsupported scheme: %s", address.Scheme)
	}

	// Add query parameters
	if params != nil {
		q := address.Query()
		for key, value := range params {
			q.Add(key, fmt.Sprintf("%v", value))
		}
		address.RawQuery = q.Encode()
	}

	ctx, cancel := context.WithCancel(context.Background())

	client := &PondClient{
		address:         address,
		config:          config,
		disconnecting:   false,
		eventBroadcast:  make(chan ChannelEvent, 100),
		connectionState: make(chan bool, 1),
		connectionValue: false,
		channels:        make(map[string]*Channel),
		ctx:             ctx,
		cancel:          cancel,
		reconnectCount:  0,
		connectionSubs:  make(map[int]*boolSubscriber),
		eventSubs:       make(map[int]*clientEventSubscriber),
	}

	// Start the event dispatcher
	go client.dispatchEvents()
	go client.dispatchConnectionChanges()

	client.init()
	return client, nil
}

// Connect establishes a WebSocket connection to the server
func (c *PondClient) Connect() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.disconnecting {
		return fmt.Errorf("client is disconnecting")
	}
	if c.conn != nil {
		return nil
	}

	c.disconnecting = false

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(c.address.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", c.address.String(), err)
	}
	if err := c.refreshReadDeadline(conn); err != nil {
		conn.Close()
		return fmt.Errorf("failed to configure WebSocket read deadline: %w", err)
	}
	conn.SetPongHandler(func(string) error {
		return c.refreshReadDeadline(conn)
	})

	c.conn = conn
	c.reconnectCount = 0
	done := make(chan struct{})

	go c.writePings(conn, done)
	go c.readMessages(conn, done)

	return nil
}

// Disconnect closes the WebSocket connection
func (c *PondClient) Disconnect() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	c.disconnecting = true

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	c.setConnectionState(false)

	c.cancel()

	// Clear all channels
	c.channelsMu.Lock()
	for name := range c.channels {
		delete(c.channels, name)
	}
	c.channelsMu.Unlock()

	return nil
}

// GetState returns the current connection state
func (c *PondClient) GetState() bool {
	c.stateMu.RLock()
	defer c.stateMu.RUnlock()
	return c.connectionValue
}

// CreateChannel creates or returns an existing channel
func (c *PondClient) CreateChannel(name string, params JoinParams) *Channel {
	c.channelsMu.Lock()
	defer c.channelsMu.Unlock()

	if channel, exists := c.channels[name]; exists && channel.State() != Closed {
		return channel
	}

	connectionChan, unsubscribeConnection := c.subscribeToConnection()
	channel := NewChannelWithCleanup(c.createPublisher(), connectionChan, unsubscribeConnection, name, params)
	c.channels[name] = channel

	return channel
}

// OnConnectionChange subscribes to connection state changes
func (c *PondClient) OnConnectionChange(callback ConnectionHandler) func() {
	c.connectionSubsMu.Lock()
	sub := &boolSubscriber{ch: make(chan bool, 1), done: make(chan struct{})}
	id := c.nextConnSubID
	c.nextConnSubID++
	c.connectionSubs[id] = sub
	c.connectionSubsMu.Unlock()

	// Send current state immediately
	go func() {
		callback(c.GetState())
	}()

	// Start listener goroutine
	go func() {
		for {
			select {
			case state := <-sub.ch:
				callback(state)
			case <-sub.done:
				return
			case <-c.ctx.Done():
				draining := true
				for draining {
					select {
					case state := <-sub.ch:
						callback(state)
					default:
						draining = false
					}
				}
				callback(c.GetState())
				return
			}
		}
	}()

	// Return unsubscribe function
	return func() {
		c.connectionSubsMu.Lock()
		if existing, ok := c.connectionSubs[id]; ok {
			delete(c.connectionSubs, id)
			existing.close()
		}
		c.connectionSubsMu.Unlock()
	}
}

// subscribeToConnection returns a channel that receives connection state changes
func (c *PondClient) subscribeToConnection() (<-chan bool, func()) {
	c.connectionSubsMu.Lock()
	sub := &boolSubscriber{ch: make(chan bool, 1), done: make(chan struct{})}
	id := c.nextConnSubID
	c.nextConnSubID++
	c.connectionSubs[id] = sub
	c.connectionSubsMu.Unlock()

	// Send current state immediately
	go func() {
		c.safeSendBool(sub, c.GetState())
	}()

	unsubscribe := func() {
		c.connectionSubsMu.Lock()
		if existing, ok := c.connectionSubs[id]; ok {
			delete(c.connectionSubs, id)
			existing.close()
		}
		c.connectionSubsMu.Unlock()
	}

	return sub.ch, unsubscribe
}

// subscribeToEvents returns a channel that receives all events
func (c *PondClient) subscribeToEvents() <-chan ChannelEvent {
	sub, _ := c.subscribeToEventsWithUnsubscribe()
	return sub
}

func (c *PondClient) subscribeToEventsWithUnsubscribe() (<-chan ChannelEvent, func()) {
	c.eventSubsMu.Lock()
	sub := &clientEventSubscriber{ch: make(chan ChannelEvent, 100), done: make(chan struct{})}
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

	return sub.ch, unsubscribe
}

// createPublisher returns a function that publishes messages to the WebSocket
func (c *PondClient) createPublisher() func(ClientMessage) {
	return func(message ClientMessage) {
		if !c.GetState() {
			return
		}

		c.connMu.RLock()
		conn := c.conn
		c.connMu.RUnlock()

		if conn == nil {
			return
		}

		data, err := json.Marshal(message)
		if err != nil {
			return
		}

		c.writeMu.Lock()
		conn.SetWriteDeadline(time.Now().Add(c.config.WriteTimeout))
		err = conn.WriteMessage(websocket.TextMessage, data)
		c.writeMu.Unlock()
		if err != nil {
			// Connection error, will be handled by readMessages
			return
		}
	}
}

// setConnectionState updates the connection state and notifies subscribers
func (c *PondClient) setConnectionState(connected bool) {
	c.stateMu.Lock()
	oldValue := c.connectionValue
	c.connectionValue = connected
	c.stateMu.Unlock()

	if oldValue != connected {
		c.connectionSubsMu.RLock()
		subs := make([]*boolSubscriber, 0, len(c.connectionSubs))
		for _, sub := range c.connectionSubs {
			subs = append(subs, sub)
		}
		c.connectionSubsMu.RUnlock()

		for _, sub := range subs {
			c.safeSendBool(sub, connected)
		}
	}
}

// dispatchConnectionChanges sends connection state changes to all subscribers
func (c *PondClient) dispatchConnectionChanges() {
	for {
		select {
		case state := <-c.connectionState:
			c.connectionSubsMu.RLock()
			subs := make([]*boolSubscriber, 0, len(c.connectionSubs))
			for _, sub := range c.connectionSubs {
				subs = append(subs, sub)
			}
			c.connectionSubsMu.RUnlock()

			for _, sub := range subs {
				c.safeSendBool(sub, state)
			}
		case <-c.ctx.Done():
			return
		}
	}
}

// dispatchEvents sends events to all subscribers
func (c *PondClient) dispatchEvents() {
	for {
		select {
		case event := <-c.eventBroadcast:
			c.eventSubsMu.RLock()
			subs := make([]*clientEventSubscriber, 0, len(c.eventSubs))
			for _, sub := range c.eventSubs {
				subs = append(subs, sub)
			}
			c.eventSubsMu.RUnlock()

			for _, sub := range subs {
				c.safeSendEvent(sub, event)
			}
		case <-c.ctx.Done():
			return
		}
	}
}

// readMessages handles incoming WebSocket messages for one connection.
func (c *PondClient) readMessages(conn *websocket.Conn, done chan struct{}) {
	defer func() {
		close(done)
		conn.Close()
		if !c.releaseConnection(conn) {
			return
		}
		c.setConnectionState(false)
		c.handleDisconnect()
	}()

	c.setConnectionState(true)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			_, data, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					// Log unexpected close
				}
				return
			}
			if err := c.refreshReadDeadline(conn); err != nil {
				return
			}

			var event ChannelEvent
			if err := json.Unmarshal(data, &event); err != nil {
				continue
			}

			select {
			case c.eventBroadcast <- event:
			default:
				// Channel is full, skip
			}
		}
	}
}

func (c *PondClient) writePings(conn *websocket.Conn, done <-chan struct{}) {
	if c.config.PingInterval <= 0 {
		return
	}

	ticker := time.NewTicker(c.config.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			deadline := time.Time{}
			if c.config.WriteTimeout > 0 {
				deadline = time.Now().Add(c.config.WriteTimeout)
			}

			c.writeMu.Lock()
			err := conn.WriteControl(websocket.PingMessage, nil, deadline)
			c.writeMu.Unlock()
			if err != nil {
				conn.Close()
				return
			}
		}
	}
}

func (c *PondClient) refreshReadDeadline(conn *websocket.Conn) error {
	wait := c.config.ReadTimeout
	if c.config.PingInterval > 0 && c.config.PongTimeout > 0 {
		wait = c.config.PingInterval + c.config.PongTimeout
	}

	deadline := time.Time{}
	if wait > 0 {
		deadline = time.Now().Add(wait)
	}
	return conn.SetReadDeadline(deadline)
}

func (c *PondClient) releaseConnection(conn *websocket.Conn) bool {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn != conn {
		return false
	}
	c.conn = nil
	return true
}

// handleDisconnect handles disconnection and reconnection logic
func (c *PondClient) handleDisconnect() {
	c.reconnectMu.Lock()
	defer c.reconnectMu.Unlock()

	for {
		c.connMu.Lock()
		if c.disconnecting {
			c.connMu.Unlock()
			return
		}
		c.reconnectCount++
		reconnectCount := c.reconnectCount
		c.connMu.Unlock()

		if c.config.MaxReconnectTries >= 0 && reconnectCount > c.config.MaxReconnectTries {
			return
		}

		backoff := time.Duration(reconnectCount) * c.config.ReconnectInterval
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}

		timer := time.NewTimer(backoff)
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		if err := c.Connect(); err == nil {
			return
		}
	}
}

// init initializes the client's event handling
func (c *PondClient) init() {
	eventChan, unsubscribe := c.subscribeToEventsWithUnsubscribe()

	go func() {
		defer unsubscribe()
		for event := range eventChan {
			switch event.Event {
			case string(EventAcknowledge):
				c.handleAcknowledge(event)
			case string(EventConnection):
				if event.Action == Connect {
					c.setConnectionState(true)
				}
			case string(EventUnauthorized), string(EventNotFound):
				if event.Action == System {
					c.handleDecline(event)
				}
			case string(EventInternalError):
				if event.Action == System {
					c.handleError(event)
				}
			}
		}
	}()
}

// handleAcknowledge handles channel acknowledgment events
func (c *PondClient) handleAcknowledge(event ChannelEvent) {
	c.channelsMu.Lock()
	defer c.channelsMu.Unlock()

	channel, exists := c.channels[event.ChannelName]
	if !exists {
		return
	}

	eventChan, unsubscribe := c.subscribeToEventsWithUnsubscribe()
	channel.Acknowledge(eventChan, unsubscribe)
}

func (c *PondClient) handleDecline(event ChannelEvent) {
	c.channelsMu.RLock()
	channel, exists := c.channels[event.ChannelName]
	c.channelsMu.RUnlock()

	if exists {
		channel.decline(event)
	}
}

func (c *PondClient) handleError(event ChannelEvent) {
	c.channelsMu.RLock()
	channel, exists := c.channels[event.ChannelName]
	c.channelsMu.RUnlock()

	if exists {
		channel.surface(event)
	}
}

func (c *PondClient) safeSendBool(sub *boolSubscriber, value bool) {
	select {
	case <-sub.done:
	case sub.ch <- value:
	default:
		select {
		case <-sub.ch:
		default:
		}
		select {
		case <-sub.done:
		case sub.ch <- value:
		default:
		}
	}
}

func (c *PondClient) safeSendEvent(sub *clientEventSubscriber, event ChannelEvent) {
	select {
	case <-sub.done:
	case sub.ch <- event:
	default:
	}
}
