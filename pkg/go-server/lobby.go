// This file contains the Lobby struct which provides a high-level interface for managing
// channels within an endpoint. It allows configuration of message handlers, presence tracking,
// and outgoing message transformations for channels matching specific patterns.
package pondsocket

import (
	"context"
	"errors"
	"sync"
)

type Lobby struct {
	leaveHandler *LeaveHandler
	middleware   *middleware[*messageEvent, *Channel]
	outgoing     *middleware[*OutgoingContext, interface{}]
	channels     *store[*Channel]
	endpoint     *Endpoint
	channelMutex sync.RWMutex
}

func newLobby(endpoint *Endpoint) *Lobby {
	return &Lobby{
		endpoint:   endpoint,
		middleware: newMiddleWare[*messageEvent, *Channel](),
		outgoing:   newMiddleWare[*OutgoingContext, interface{}](),
		channels:   newStore[*Channel](),
	}
}

// OnLeave registers a handler that is called when a user leaves any channel in this lobby.
// The handler is called asynchronously after the user has been removed from the channel.
// Only one leave handler can be registered; subsequent calls will replace the previous handler.
func (l *Lobby) OnLeave(handler LeaveHandler) {
	if err := l.endpoint.checkState(); err != nil {
		return
	}
	l.leaveHandler = &handler
}

// OnMessage registers a handler for messages matching the specified event pattern.
// The event pattern can include parameters (e.g., "chat:*" or "user.:action").
// Handlers are called in the order they were registered until one handles the message.
// Multiple handlers can be registered for different event patterns.
func (l *Lobby) OnMessage(event Path, handler MessageEventHandler) {
	if err := l.endpoint.checkState(); err != nil {
		return
	}
	l.middleware.Use(func(ctx context.Context, request *messageEvent, ch *Channel, next nextFunc) error {
		if err := l.endpoint.checkState(); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		route, err := parse(string(event), request.Event.Event)
		if err != nil {
			return next()
		}

		ctx, cancel := context.WithTimeout(ctx, l.endpoint.options.InternalQueueTimeout)
		defer cancel()
		eventCtx := newEventContext(ctx, ch, request, route)

		return handler(eventCtx)
	})
}

// OnOutgoing registers a handler for outgoing messages matching the specified event pattern.
// This allows transformation or blocking of messages before they are sent to clients.
// Handlers can modify the payload, block the message, or refresh user data.
// Multiple handlers can be registered and are executed in registration order.
func (l *Lobby) OnOutgoing(event Path, handler OutgoingEventHandler) {
	if err := l.endpoint.checkState(); err != nil {
		return
	}
	l.outgoing.Use(func(ctx context.Context, request *OutgoingContext, response interface{}, next nextFunc) error {
		if err := l.endpoint.checkState(); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		route, err := parse(string(event), request.Event.Event)

		if err != nil {
			return next()
		}
		request.setRoute(route)

		return handler(request)
	})
}

func (l *Lobby) createChannel(name string) (*Channel, error) {
	l.channelMutex.Lock()
	defer l.channelMutex.Unlock()

	if existingCh, _ := l.channels.Read(name); existingCh != nil {
		return existingCh, nil
	}
	return l.createChannelUnsafe(name)
}

func (l *Lobby) createChannelUnsafe(name string) (*Channel, error) {
	if err := l.endpoint.checkState(); err != nil {
		return nil, err
	}
	opts := options{
		Name:                 name,
		Middleware:           l.middleware,
		Leave:                l.leaveHandler,
		Outgoing:             l.outgoing,
		InternalQueueTimeout: l.endpoint.options.InternalQueueTimeout,
		PubSub:               l.endpoint.options.PubSub,
	}
	c := newChannel(l.endpoint.ctx, opts)

	c.endpointPath = l.endpoint.path
	c.subscribeToPubSub()

	c.onDestroy = l.onChannelDestroyed(name)

	channelsErr := l.channels.Create(name, c)

	endpointErr := l.endpoint.channels.Create(name, c)

	if err := combine(channelsErr, endpointErr); err != nil {
		if err = c.Close(); err != nil {
			return nil, err
		}
		return nil, err
	}
	return c, nil
}

func (l *Lobby) getOrCreateChannel(name string) (*Channel, error) {
	if err := l.endpoint.checkState(); err != nil {
		return nil, err
	}
	ch, err := l.channels.Read(name)

	if err == nil {
		return ch, nil
	}

	var pondErr *Error
	if !errors.As(err, &pondErr) || pondErr.Code != StatusNotFound {
		return nil, err
	}
	l.channelMutex.Lock()

	defer l.channelMutex.Unlock()

	if existingCh, _ := l.channels.Read(name); existingCh != nil {
		return existingCh, nil
	}
	return l.createChannelUnsafe(name)
}

func (l *Lobby) onChannelDestroyed(name string) func() error {
	return func() error {
		l.channelMutex.Lock()
		defer l.channelMutex.Unlock()
		errLobby := l.channels.Delete(name)
		errEndpoint := l.endpoint.channels.Delete(name)
		return combine(errLobby, errEndpoint)
	}
}
