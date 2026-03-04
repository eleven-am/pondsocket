package pondsocket

import (
	"context"
	"sync"
)

type middleware[Request any, Response any] struct {
	handlers []handlerFunc[Request, Response]
	mutex    sync.RWMutex
}

func newMiddleWare[Request any, Response any]() *middleware[Request, Response] {
	return &middleware[Request, Response]{
		handlers: make([]handlerFunc[Request, Response], 0),
	}
}

func (m *middleware[Request, Response]) Use(handlers ...handlerFunc[Request, Response]) {
	m.mutex.Lock()

	defer m.mutex.Unlock()

	m.handlers = append(m.handlers, handlers...)
}

func (m *middleware[Request, Response]) Compose(others ...*middleware[Request, Response]) *middleware[Request, Response] {
	result := newMiddleWare[Request, Response]()

	m.mutex.RLock()

	handlersCopy := make([]handlerFunc[Request, Response], len(m.handlers))

	copy(handlersCopy, m.handlers)

	m.mutex.RUnlock()

	result.Use(handlersCopy...)

	for _, other := range others {
		other.mutex.RLock()

		otherHandlersCopy := make([]handlerFunc[Request, Response], len(other.handlers))

		copy(otherHandlersCopy, other.handlers)

		other.mutex.RUnlock()

		result.Use(otherHandlersCopy...)
	}
	return result
}

func executeWithMiddleware[C any](ctx *C, handler func(ctx *C) error, middlewares []MiddlewareFunc[C]) error {
	if len(middlewares) == 0 {
		return handler(ctx)
	}
	var run func(i int) error
	run = func(i int) error {
		if i >= len(middlewares) {
			return handler(ctx)
		}
		return middlewares[i](ctx, func() error {
			return run(i + 1)
		})
	}
	return run(0)
}

func (m *middleware[Request, Response]) Handle(ctx context.Context, request Request, response Response, finalHandler FinalHandlerFunc[Request, Response]) error {
	select {
	case <-ctx.Done():
		return ctx.Err()

	default:
	}
	m.mutex.RLock()

	handlersCopy := make([]handlerFunc[Request, Response], len(m.handlers))

	copy(handlersCopy, m.handlers)

	m.mutex.RUnlock()

	if len(handlersCopy) == 0 {
		return finalHandler(request, response)
	}

	var executeHandler func(index int) error
	executeHandler = func(index int) error {
		select {
		case <-ctx.Done():
			return ctx.Err()

		default:
		}
		if index >= len(handlersCopy) {
			return finalHandler(request, response)
		}
		handler := handlersCopy[index]
		next := func() error {
			return executeHandler(index + 1)
		}
		return handler(ctx, request, response, next)
	}
	return executeHandler(0)
}
