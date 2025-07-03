// This file contains the Conn struct which represents a WebSocket connection to a client.
// It handles the low-level WebSocket communication, including reading and writing messages,
// ping/pong keepalive, graceful shutdown, and connection lifecycle management.
package main

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"sync"
	"time"
)

type eventHandler func(event Event, user *Conn) error

type Conn struct {
	ID            string
	conn          *websocket.Conn
	send          chan []byte
	receive       chan []byte
	assigns       map[string]interface{}
	closeChan     chan struct{}
	closeOnce     sync.Once
	mutex         sync.RWMutex
	isClosing     bool
	closeHandlers *array[func(*Conn) error]
	handler       *eventHandler
	options       *Options
	ctx           context.Context
	cancel        context.CancelFunc
}

func newConn(mCtx context.Context, wsConn *websocket.Conn, assigns map[string]interface{}, id string, options *Options) (*Conn, error) {
	ctx, cancel := context.WithCancel(mCtx)

	c := &Conn{
		ID:            id,
		conn:          wsConn,
		assigns:       assigns,
		ctx:           ctx,
		cancel:        cancel,
		closeChan:     make(chan struct{}),
		send:          make(chan []byte, options.SendChannelBuffer),
		receive:       make(chan []byte, options.ReceiveChannelBuffer),
		closeHandlers: newArray[func(*Conn) error](),
		options:       options,
	}
	wsConn.SetReadLimit(options.MaxMessageSize)

	if err := wsConn.SetReadDeadline(time.Now().Add(options.PongWait)); err != nil {
		cancel()

		return nil, wrapF(err, "failed to set initial read deadline for connection %s", id)
	}
	wsConn.SetPongHandler(func(string) error {
		err := wsConn.SetReadDeadline(time.Now().Add(options.PongWait))

		if err != nil {
			return err
		}
		return nil
	})

	c.conn.SetCloseHandler(func(code int, text string) error {
		c.Close()

		return nil
	})

	go c.readPump()

	go c.writePump()

	return c, nil
}

func (c *Conn) readPump() {
	defer func() {
		c.cancel()

		c.Close()
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			if err := c.conn.SetReadDeadline(time.Now().Add(c.options.PongWait)); err != nil {
				return
			}
			_, message, err := c.conn.ReadMessage()

			if err != nil {
				return
			}
			select {
			case c.receive <- message:
			case <-c.ctx.Done():
				return
			case <-time.After(c.options.WriteWait):
				return
			}
		}
	}
}

func (c *Conn) writePump() {
	ticker := time.NewTicker(c.options.PingInterval)

	defer func() {
		ticker.Stop()

		c.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !c.IsActive() {
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "connection closed"))

				return
			}
			if err := c.conn.SetWriteDeadline(time.Now().Add(c.options.WriteWait)); err != nil {
				return
			}
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})

				return
			}
			w, err := c.conn.NextWriter(websocket.TextMessage)

			if err != nil {
				return
			}
			if _, err = w.Write(message); err != nil {
				_ = w.Close()

				return
			}
			n := len(c.send)

			for i := 0; i < n; i++ {
				select {
				case msg, ok := <-c.send:
					if !ok {
						break
					}
					if _, err = w.Write([]byte{'\n'}); err != nil {
						_ = w.Close()

						return
					}
					if _, err = w.Write(msg); err != nil {
						_ = w.Close()

						return
					}
				default:
					break
				}
			}
			if err = w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			if !c.IsActive() {
				return
			}
			if err := c.conn.SetWriteDeadline(time.Now().Add(c.options.WriteWait)); err != nil {
				return
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.ctx.Done():
			return
		case <-c.closeChan:
			return
		}
	}
}

func (c *Conn) handleMessages() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				c.Close()
			}
		}()

		for {
			select {
			case message, ok := <-c.receive:
				if !ok {
					return
				}

				var event Event
				if err := json.Unmarshal(message, &event); err != nil {
					continue
				}
				c.mutex.RLock()

				handler := c.handler
				c.mutex.RUnlock()

				if handler == nil {
					continue
				}
				if !event.Validate() {
					continue
				}
				_ = (*handler)(event, c)

			case <-c.ctx.Done():
				return
			case <-c.closeChan:
				return
			}
		}
	}()
}

func (c *Conn) sendJSON(v interface{}) error {
	if !c.IsActive() {
		return internal(string(gatewayEntity), "Connection with id "+c.ID+" is closing")
	}
	data, err := json.Marshal(v)

	if err != nil {
		return wrapF(err, "failed to marshal JSON for connection %s", c.ID)
	}
	select {
	case <-c.closeChan:
		return internal(string(gatewayEntity), "Connection with id "+c.ID+" is closing")

	case <-c.ctx.Done():
		return internal(string(gatewayEntity), "Connection with id "+c.ID+" is closing due to context cancellation")

	case c.send <- data:
		return nil
	case <-time.After(5 * time.Second):
		go c.Close()

		return internal(string(gatewayEntity), "send timeout, connection with id "+c.ID+" is closing")
	}
}

func (c *Conn) onMessage(handler eventHandler) {
	c.mutex.Lock()

	defer c.mutex.Unlock()

	c.handler = &handler
}

func (c *Conn) setAssign(key string, value interface{}) {
	c.mutex.Lock()

	defer c.mutex.Unlock()

	if c.assigns == nil {
		c.assigns = make(map[string]interface{})
	}
	c.assigns[key] = value
}

// GetAssign retrieves a value from the connection's assigns map by key.
// Assigns are metadata associated with this connection that persist
// across channel joins. Returns nil if the key doesn't exist.
func (c *Conn) GetAssign(key string) interface{} {
	c.mutex.RLock()

	defer c.mutex.RUnlock()

	if c.assigns == nil {
		return nil
	}
	return c.assigns[key]
}

// OnClose registers a callback to be executed when the connection closes.
// Multiple callbacks can be registered and they will be called in the order
// they were added. Callbacks are executed synchronously during connection cleanup.
func (c *Conn) OnClose(callback func(conn *Conn) error) {
	c.mutex.Lock()

	defer c.mutex.Unlock()

	c.closeHandlers.push(callback)
}

// IsActive returns true if the connection is still active and can send/receive messages.
// Returns false if the connection is closing or has been closed.
// This method is thread-safe and can be called concurrently.
func (c *Conn) IsActive() bool {
	select {
	case <-c.ctx.Done():
		return false
	default:
	}
	c.mutex.RLock()

	defer c.mutex.RUnlock()

	return !c.isClosing
}

// Close gracefully shuts down the connection.
// It executes all registered close handlers, cancels the context,
// closes the WebSocket connection, and cleans up all channels.
// This method is idempotent and can be called multiple times safely.
func (c *Conn) Close() {
	c.closeOnce.Do(func() {
		c.mutex.Lock()

		c.isClosing = true
		handlersToRun := make([]func(*Conn) error, len(c.closeHandlers.items))

		copy(handlersToRun, c.closeHandlers.items)

		c.mutex.Unlock()

		if c.cancel != nil {
			c.cancel()
		}
		close(c.closeChan)

		var closeHandlerErrors error
		for _, handler := range handlersToRun {
			if err := handler(c); err != nil {
				closeHandlerErrors = addError(closeHandlerErrors, err)
			}
		}
		if closeHandlerErrors != nil {
		}
		if c.conn != nil {
			c.conn.Close()
		}
		close(c.send)

		close(c.receive)
	})
}

func (c *Conn) cloneAssigns() map[string]interface{} {
	c.mutex.RLock()

	defer c.mutex.RUnlock()

	cloned := make(map[string]interface{}, len(c.assigns))

	for key, value := range c.assigns {
		cloned[key] = value
	}
	return cloned
}
