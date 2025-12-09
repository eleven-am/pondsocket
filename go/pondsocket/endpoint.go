// This file contains the Endpoint struct which manages WebSocket connections for a specific path.
// Endpoints handle connection lifecycle, message routing between clients and channels,
// and coordinate join/leave operations for channel-based communication.
package pondsocket

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
)

type Endpoint struct {
	path        string
	connections *store[Transport]
	middleware  *middleware[joinEvent, interface{}]
	channels    *store[*Channel]
	options     *Options
	ctx         context.Context
	mutex       sync.RWMutex
}

type joinEvent struct {
	user      Transport
	channel   string
	requestId string
	payload   interface{}
}

func newEndpoint(ctx context.Context, path string, options *Options) *Endpoint {
	return &Endpoint{
		path:        path,
		options:     options,
		connections: newStore[Transport](),
		channels:    newStore[*Channel](),
		middleware:  newMiddleWare[joinEvent, interface{}](),
		ctx:         ctx,
	}
}

// CreateChannel creates a new channel pattern for this endpoint.
// The path parameter defines the channel pattern (e.g., "room/:id" or "user/:userId/notifications").
// The handlerFunc is called when a client attempts to join a matching channel,
// allowing for custom authorization and setup logic.
// Returns a Lobby instance for further configuration of message and presence handling.
func (e *Endpoint) CreateChannel(path string, handlerFunc JoinEventHandler) *Lobby {
	if err := e.checkState(); err != nil {
		return newLobby(e)
	}
	lobby := newLobby(e)

	e.middleware.Use(func(ctx context.Context, request joinEvent, _ interface{}, next nextFunc) error {
		if err := e.checkState(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()

		default:
		}
		route, err := parse(path, request.channel)

		if err != nil {
			return next()
		}
		ch, err := lobby.getOrCreateChannel(request.channel)

		if err != nil {
			return wrapF(err, "failed to get or create channel instance for %s", request.channel)
		}
		ctx, cancel := context.WithTimeout(e.ctx, e.options.InternalQueueTimeout)

		defer cancel()

		ev := &Event{
			Action:      system,
			ChannelName: request.channel,
			RequestId:   request.requestId,
			Event:       string(joinChannelEvent),
			Payload:     request.payload,
		}
		joinCtx := newJoinContext(ctx, ch, route, request.user, ev)

		if err := handlerFunc(joinCtx); err != nil {
			return err
		}
		if joinCtx.err != nil {
			return joinCtx
		}
		return nil
	})

	return lobby
}

// CloseConnection forcefully closes one or more connections by their IDs.
// This can be used to disconnect specific clients from the server.
// Returns an error if any of the specified connections cannot be found or closed.
// If no IDs are provided, the method returns nil without performing any action.
func (e *Endpoint) CloseConnection(ids ...string) error {
	if err := e.checkState(); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	var errs []error
	for _, id := range ids {
		if _, err := e.connections.Read(id); err != nil {
			errs = append(errs, wrapF(err, "connection %s not found", id))
		}
	}

	connsToClose := e.connections.GetByKeys(ids...)

	closeErr := mapToError(connsToClose, func(conn Transport) error {
		conn.Close()

		return e.connections.Delete(conn.GetID())
	})

	errs = append(errs, closeErr)

	return combine(errs...)
}

// GetClients returns a slice of active connections for this endpoint.
// If no IDs are provided, it returns all active connections.
// If specific IDs are provided, it returns only the connections matching those IDs.
// Non-existent IDs are silently ignored.
func (e *Endpoint) GetClients(ids ...string) []Transport {
	if err := e.checkState(); err != nil {
		return []Transport{}
	}
	if len(ids) == 0 {
		clients := e.connections.Values()

		return clients.items
	}
	clients := e.connections.GetByKeys(ids...)

	return clients.items
}

// GetChannelByName retrieves a channel instance by its exact name.
// Returns the channel if it exists, or an error if the channel is not found.
// This method looks up active channels that have been created through client joins.
func (e *Endpoint) GetChannelByName(name string) (*Channel, error) {
	if err := e.checkState(); err != nil {
		return nil, err
	}
	channel, err := e.channels.Read(name)

	return channel, err
}

func (e *Endpoint) joinChannel(ev *Event, user Transport) error {
	if err := e.checkState(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(e.ctx, e.options.InternalQueueTimeout)

	defer cancel()

	joinReq := joinEvent{
		user:      user,
		requestId: ev.RequestId,
		channel:   ev.ChannelName,
		payload:   ev.Payload,
	}
	err := e.middleware.Handle(ctx, joinReq, nil, e.notFoundHandler(ctx, ev.RequestId))

	if err != nil {
		errMsg := Event{
			Action:      system,
			ChannelName: ev.ChannelName,
			RequestId:   ev.RequestId,
			Event:       string(internalErrorEvent),
			Payload:     internal(ev.ChannelName, "Failed to process join request"),
		}

		var pondErr *Error
		if errors.As(err, &pondErr) {
			errMsg.Payload = pondErr
		}
		_ = user.SendJSON(errMsg)
	}
	return err
}

func (e *Endpoint) leaveChannel(ev *Event, user Transport) error {
	if err := e.checkState(); err != nil {
		return err
	}
	currentChannel, err := e.channels.Read(ev.ChannelName)

	if err != nil {
		_ = user.SendJSON(Event{
			Action:      system,
			ChannelName: ev.ChannelName,
			RequestId:   ev.RequestId,
			Event:       string(notFoundEvent),
			Payload:     notFound(ev.ChannelName, "Channel not found"),
		})

		return err
	}
	if err = currentChannel.RemoveUser(user.GetID(), "explicit_leave"); err != nil {
		_ = user.SendJSON(Event{
			Action:      system,
			ChannelName: ev.ChannelName,
			RequestId:   ev.RequestId,
			Event:       string(internalErrorEvent),
			Payload:     wrapF(err, "failed to leave channel"),
		})

		return err
	}
	_ = user.SendJSON(Event{
		Action:      system,
		ChannelName: ev.ChannelName,
		RequestId:   ev.RequestId,
		Event:       string(exitAcknowledgeEvent),
		Payload:     make(map[string]interface{}),
	})

	return nil
}

func (e *Endpoint) broadcastMessage(ev *Event, user Transport) error {
	if err := e.checkState(); err != nil {
		return err
	}
	currentChannel, err := e.channels.Read(ev.ChannelName)

	if err != nil {
		_ = user.SendJSON(Event{
			Action:      system,
			ChannelName: ev.ChannelName,
			RequestId:   ev.RequestId,
			Event:       string(notFoundEvent),
			Payload:     notFound(ev.ChannelName, "Channel not found"),
		})

		return err
	}
	if err = currentChannel.broadcast(user.GetID(), ev); err != nil {
		_ = user.SendJSON(Event{
			Action:      system,
			ChannelName: ev.ChannelName,
			RequestId:   ev.RequestId,
			Event:       string(internalErrorEvent),
			Payload:     wrapF(err, "failed to process broadcast"),
		})

		return err
	}
	return nil
}

func (e *Endpoint) notFoundHandler(ctx context.Context, requestID string) FinalHandlerFunc[joinEvent, interface{}] {
	return func(request joinEvent, response interface{}) error {
		select {
		case <-ctx.Done():
			return ctx.Err()

		default:
		}
		if err := e.checkState(); err != nil {
			return err
		}
		message := Event{
			Action:      system,
			ChannelName: request.channel,
			RequestId:   requestID,
			Event:       string(notFoundEvent),
			Payload:     notFound(request.channel, "No channel handler found for this topic"),
		}
		return request.user.SendJSON(message)
	}
}

func (e *Endpoint) handleMessage() transportEventHandler {
	return func(ev Event, user Transport) error {
		if err := e.checkState(); err != nil {
			return err
		}
		switch ev.Action {
		case joinChannelEvent:
			return e.joinChannel(&ev, user)

		case leaveChannelEvent:
			return e.leaveChannel(&ev, user)

		case broadcast:
			return e.broadcastMessage(&ev, user)

		default:
			_ = user.SendJSON(Event{
				Action:      system,
				ChannelName: ev.ChannelName,
				RequestId:   ev.RequestId,
				Event:       string(notFoundEvent),
				Payload:     notFound(ev.ChannelName, "Unknown or unsupported event type").withDetails(map[string]string{"eventType": ev.Event}),
			})

			return badRequest(ev.ChannelName, "Unknown event type").withDetails(map[string]string{"eventType": ev.Event})
		}
	}
}

func (e *Endpoint) handleClose(c Transport) error {
	if e.options.Hooks != nil && e.options.Hooks.OnDisconnect != nil {
		e.options.Hooks.OnDisconnect(c)
	}
	if e.options.Hooks != nil && e.options.Hooks.Metrics != nil {
		e.options.Hooks.Metrics.ConnectionClosed(c.GetID(), 0)
	}
	return e.connections.Delete(c.GetID())
}

func (e *Endpoint) addConnection(conn Transport) error {
	if err := e.checkState(); err != nil {
		conn.Close()

		return err
	}
	if e.options.Hooks != nil && e.options.Hooks.OnConnect != nil {
		if err := e.options.Hooks.OnConnect(conn); err != nil {
			conn.Close()

			return wrapF(err, "OnConnect hook failed")
		}
	}
	if e.options.MaxConnections > 0 {
		e.mutex.RLock()

		currentCount := e.connections.Len()

		e.mutex.RUnlock()

		if currentCount >= e.options.MaxConnections {
			conn.Close()

			return unavailable(string(gatewayEntity), "Maximum connections reached")
		}
	}
	if err := e.connections.Create(conn.GetID(), conn); err != nil {
		conn.Close()

		return wrapF(err, "failed to store connection %s", conn.GetID())
	}
	if e.options.Hooks != nil && e.options.Hooks.Metrics != nil {
		e.options.Hooks.Metrics.ConnectionOpened(conn.GetID(), "endpoint")
	}
	conn.OnMessage(e.handleMessage())

	conn.OnClose(e.handleClose)

	conn.HandleMessages()

	err := conn.SendJSON(Event{
		Action:      connect,
		ChannelName: string(gatewayEntity),
		RequestId:   uuid.NewString(),
		Event:       string(connectionEvent),
		Payload: map[string]interface{}{
			"connectionId": conn.GetID(),
		},
	})

	if err != nil {
		conn.Close()

		_ = e.connections.Delete(conn.GetID())

		if e.options.Hooks != nil && e.options.Hooks.Metrics != nil {
			e.options.Hooks.Metrics.ConnectionError(conn.GetID(), err)
		}
		return wrapF(err, "failed to send connection confirmation to %s", conn.GetID())
	}
	return nil
}

func (e *Endpoint) sendMessage(userId string, event Event) error {
	userConn, err := e.connections.Read(userId)

	if err != nil {
		return notFound(string(gatewayEntity), "User not found").withDetails(map[string]string{"userId": userId})
	}
	return userConn.SendJSON(event)
}

func (e *Endpoint) pushMessage(connID string, data []byte) error {
	if err := e.checkState(); err != nil {
		return err
	}

	conn, err := e.connections.Read(connID)
	if err != nil {
		return notFound(string(gatewayEntity), "Connection not found").withDetails(map[string]string{"connId": connID})
	}

	if conn.Type() != TransportSSE {
		return badRequest(string(gatewayEntity), "PushMessage only supported for SSE connections")
	}

	return conn.PushMessage(data)
}

func (e *Endpoint) checkState() error {
	select {
	case <-e.ctx.Done():
		err := e.ctx.Err()

		return wrapF(err, "endpoint is shutting down")

	default:
		return nil
	}
}
