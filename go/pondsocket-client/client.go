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
	channels        map[string]*Channel
	channelsMu      sync.RWMutex
	connMu          sync.RWMutex
	stateMu         sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
	reconnectCount  int

	// Connection state subscribers
	connectionSubs   map[int]chan bool
	nextConnSubID    int
	connectionSubsMu sync.RWMutex

	// Event broadcast subscribers
	eventSubs   map[int]chan ChannelEvent
	nextEventID int
	eventSubsMu sync.RWMutex
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
		connectionSubs:  make(map[int]chan bool),
		eventSubs:       make(map[int]chan ChannelEvent),
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

	c.disconnecting = false

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.Dial(c.address.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", c.address.String(), err)
	}

	c.conn = conn
	c.reconnectCount = 0

	// Start message reading goroutine
	go c.readMessages()

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

	channel := NewChannel(c.createPublisher(), c.subscribeToConnection(), name, params)
	c.channels[name] = channel

	return channel
}

// OnConnectionChange subscribes to connection state changes
func (c *PondClient) OnConnectionChange(callback ConnectionHandler) func() {
	c.connectionSubsMu.Lock()
	sub := make(chan bool, 1)
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
		for state := range sub {
			callback(state)
		}
	}()

	// Return unsubscribe function
	return func() {
		c.connectionSubsMu.Lock()
		if existing, ok := c.connectionSubs[id]; ok {
			delete(c.connectionSubs, id)
			close(existing)
		}
		c.connectionSubsMu.Unlock()
	}
}

// subscribeToConnection returns a channel that receives connection state changes
func (c *PondClient) subscribeToConnection() <-chan bool {
	c.connectionSubsMu.Lock()
	sub := make(chan bool, 1)
	id := c.nextConnSubID
	c.nextConnSubID++
	c.connectionSubs[id] = sub
	c.connectionSubsMu.Unlock()

	// Send current state immediately
	go func() {
		c.safeSendBool(sub, c.GetState())
	}()

	return sub
}

// subscribeToEvents returns a channel that receives all events
func (c *PondClient) subscribeToEvents() <-chan ChannelEvent {
	c.eventSubsMu.Lock()
	sub := make(chan ChannelEvent, 100)
	id := c.nextEventID
	c.nextEventID++
	c.eventSubs[id] = sub
	c.eventSubsMu.Unlock()

	return sub
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

		conn.SetWriteDeadline(time.Now().Add(c.config.WriteTimeout))
		err = conn.WriteMessage(websocket.TextMessage, data)
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
		select {
		case c.connectionState <- connected:
		default:
			// Channel is full, skip
		}
	}
}

// dispatchConnectionChanges sends connection state changes to all subscribers
func (c *PondClient) dispatchConnectionChanges() {
	for {
		select {
		case state := <-c.connectionState:
			c.connectionSubsMu.RLock()
			subs := make([]chan bool, 0, len(c.connectionSubs))
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
			subs := make([]chan ChannelEvent, 0, len(c.eventSubs))
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

// readMessages handles incoming WebSocket messages
func (c *PondClient) readMessages() {
	defer func() {
		c.setConnectionState(false)
		c.handleDisconnect()
	}()

	c.setConnectionState(true)

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			c.connMu.RLock()
			conn := c.conn
			c.connMu.RUnlock()

			if conn == nil {
				return
			}

			conn.SetReadDeadline(time.Now().Add(c.config.ReadTimeout))
			_, data, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					// Log unexpected close
				}
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

// handleDisconnect handles disconnection and reconnection logic
func (c *PondClient) handleDisconnect() {
	if c.disconnecting {
		return
	}

	c.reconnectCount++

	// Check if we should attempt reconnection
	if c.config.MaxReconnectTries >= 0 && c.reconnectCount > c.config.MaxReconnectTries {
		return
	}

	// Exponential backoff with jitter
	backoff := time.Duration(c.reconnectCount) * c.config.ReconnectInterval
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}

	timer := time.NewTimer(backoff)
	defer timer.Stop()

	select {
	case <-c.ctx.Done():
		return
	case <-timer.C:
		if err := c.Connect(); err != nil {
			// Connection failed, will try again
		}
	}
}

// init initializes the client's event handling
func (c *PondClient) init() {
	eventChan := c.subscribeToEvents()

	go func() {
		for event := range eventChan {
			switch event.Event {
			case string(EventAcknowledge):
				c.handleAcknowledge(event)
			case string(EventConnection):
				if event.Action == Connect {
					c.setConnectionState(true)
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
		channel = NewChannel(c.createPublisher(), c.subscribeToConnection(), event.ChannelName, JoinParams{})
		c.channels[event.ChannelName] = channel
	}

	channel.Acknowledge(c.subscribeToEvents())
}

func (c *PondClient) safeSendBool(ch chan bool, value bool) {
	defer func() {
		if recover() != nil {
			// Channel closed, ignore
		}
	}()

	select {
	case ch <- value:
	default:
	}
}

func (c *PondClient) safeSendEvent(ch chan ChannelEvent, event ChannelEvent) {
	defer func() {
		if recover() != nil {
			// Channel closed, ignore
		}
	}()

	select {
	case ch <- event:
	default:
	}
}
