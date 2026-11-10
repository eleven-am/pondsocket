package pondsocket

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

type SSEConn struct {
	id            string
	writer        http.ResponseWriter
	flusher       http.Flusher
	incoming      chan []byte
	assigns       map[string]interface{}
	closeChan     chan struct{}
	closeOnce     sync.Once
	mutex         sync.RWMutex
	isClosing     bool
	handlersRan   bool
	closeHandlers *array[func(Transport) error]
	handler       *transportEventHandler
	options       *Options
	ctx           context.Context
	cancel        context.CancelFunc
}

type sseOptions struct {
	writer    http.ResponseWriter
	assigns   map[string]interface{}
	id        string
	options   *Options
	parentCtx context.Context
}

func newSSEConn(opts sseOptions) (*SSEConn, error) {
	flusher, ok := opts.writer.(http.Flusher)
	if !ok {
		return nil, internal(string(gatewayEntity), "ResponseWriter does not support flushing")
	}
	if opts.options != nil && opts.options.CORSAllowCredentials && opts.options.CORSAllowOrigin == "*" {
		return nil, badRequest(string(gatewayEntity), "credentialed SSE CORS requires an explicit origin")
	}

	ctx, cancel := context.WithCancel(opts.parentCtx)

	assigns := opts.assigns
	if assigns == nil {
		assigns = make(map[string]interface{})
	}

	bufferSize := DefaultOptions().ReceiveChannelBuffer
	if opts.options != nil && opts.options.ReceiveChannelBuffer > 0 {
		bufferSize = opts.options.ReceiveChannelBuffer
	}

	conn := &SSEConn{
		id:            opts.id,
		writer:        opts.writer,
		flusher:       flusher,
		incoming:      make(chan []byte, bufferSize),
		assigns:       assigns,
		closeChan:     make(chan struct{}),
		closeHandlers: newArray[func(Transport) error](),
		options:       opts.options,
		ctx:           ctx,
		cancel:        cancel,
	}

	opts.writer.Header().Set("Content-Type", "text/event-stream")
	opts.writer.Header().Set("Cache-Control", "no-cache")
	opts.writer.Header().Set("Connection", "keep-alive")
	opts.writer.Header().Set("X-Accel-Buffering", "no")
	opts.writer.Header().Set("X-Connection-ID", opts.id)

	if opts.options != nil && opts.options.CORSAllowOrigin != "" {
		opts.writer.Header().Set("Access-Control-Allow-Origin", opts.options.CORSAllowOrigin)
		opts.writer.Header().Set("Access-Control-Expose-Headers", "X-Connection-ID")
		if opts.options.CORSAllowCredentials {
			opts.writer.Header().Set("Access-Control-Allow-Credentials", "true")
		}
	}

	go conn.keepAlive()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	return conn, nil
}

func (s *SSEConn) keepAlive() {
	interval := 30 * time.Second
	if s.options != nil && s.options.PingInterval > 0 {
		interval = s.options.PingInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !s.IsActive() {
				return
			}
			s.mutex.Lock()
			_, err := s.writer.Write([]byte(": keepalive\n\n"))
			if err != nil {
				s.mutex.Unlock()
				s.Close()
				return
			}
			s.flusher.Flush()
			s.mutex.Unlock()
		case <-s.ctx.Done():
			s.Close()
			return
		case <-s.closeChan:
			return
		}
	}
}

func (s *SSEConn) GetID() string {
	return s.id
}

func (s *SSEConn) SendJSON(v interface{}) error {
	if !s.IsActive() {
		return internal(string(gatewayEntity), "SSE connection "+s.id+" is closing")
	}

	data, err := json.Marshal(v)
	if err != nil {
		return wrapF(err, "failed to marshal JSON for SSE connection %s", s.id)
	}

	s.mutex.Lock()
	defer s.mutex.Unlock()

	select {
	case <-s.closeChan:
		return internal(string(gatewayEntity), "SSE connection "+s.id+" is closing")
	case <-s.ctx.Done():
		return internal(string(gatewayEntity), "SSE connection "+s.id+" context cancelled")
	default:
	}

	var eventName string
	switch event := v.(type) {
	case Event:
		eventName = event.Event
	case *Event:
		if event != nil {
			eventName = event.Event
		}
	}
	if eventName != "" {
		if strings.ContainsAny(eventName, "\r\n") {
			return badRequest(string(gatewayEntity), "invalid SSE event name")
		}
		_, err = s.writer.Write([]byte("event: " + eventName + "\n"))
		if err != nil {
			go s.Close()
			return wrapF(err, "failed to write SSE event field for connection %s", s.id)
		}
	}

	_, err = s.writer.Write([]byte("data: "))
	if err != nil {
		go s.Close()
		return wrapF(err, "failed to write SSE data prefix for connection %s", s.id)
	}

	_, err = s.writer.Write(data)
	if err != nil {
		go s.Close()
		return wrapF(err, "failed to write SSE data for connection %s", s.id)
	}

	_, err = s.writer.Write([]byte("\n\n"))
	if err != nil {
		go s.Close()
		return wrapF(err, "failed to write SSE terminator for connection %s", s.id)
	}

	s.flusher.Flush()
	return nil
}

func (s *SSEConn) GetAssign(key string) interface{} {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if s.assigns == nil {
		return nil
	}
	return s.assigns[key]
}

func (s *SSEConn) SetAssign(key string, value interface{}) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.assigns == nil {
		s.assigns = make(map[string]interface{})
	}
	s.assigns[key] = value
}

func (s *SSEConn) GetAssigns() map[string]interface{} {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	cloned := make(map[string]interface{}, len(s.assigns))
	for k, v := range s.assigns {
		cloned[k] = v
	}
	return cloned
}

func (s *SSEConn) CloneAssigns() map[string]interface{} {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	cloned := make(map[string]interface{}, len(s.assigns))
	for key, value := range s.assigns {
		cloned[key] = value
	}
	return cloned
}

func (s *SSEConn) IsActive() bool {
	select {
	case <-s.ctx.Done():
		return false
	default:
	}

	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return !s.isClosing
}

func (s *SSEConn) Close() {
	var handlersToRun []func(Transport) error

	s.closeOnce.Do(func() {
		s.mutex.Lock()
		s.isClosing = true
		handlersToRun = make([]func(Transport) error, len(s.closeHandlers.items))
		copy(handlersToRun, s.closeHandlers.items)
		s.handlersRan = true
		s.mutex.Unlock()

		if s.cancel != nil {
			s.cancel()
		}

		close(s.closeChan)
	})

	var closeHandlerErrors error
	for _, handler := range handlersToRun {
		if err := handler(s); err != nil {
			closeHandlerErrors = addError(closeHandlerErrors, err)
		}
	}

	if closeHandlerErrors != nil {
		s.reportError("sse_close_handlers", closeHandlerErrors)
	}
}

func (s *SSEConn) Wait() {
	select {
	case <-s.closeChan:
	case <-s.ctx.Done():
	}
}

func (s *SSEConn) OnClose(callback func(Transport) error) {
	s.mutex.Lock()
	if s.handlersRan {
		s.mutex.Unlock()
		if err := callback(s); err != nil {
			s.reportError("sse_close_handlers", err)
		}
		return
	}
	s.closeHandlers.push(callback)
	s.mutex.Unlock()
}

func (s *SSEConn) OnMessage(handler func(Event, Transport) error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	wrapped := transportEventHandler(func(event Event, transport Transport) error {
		return handler(event, transport)
	})
	s.handler = &wrapped
}

func (s *SSEConn) HandleMessages() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.Close()
			}
		}()

		for {
			select {
			case message, ok := <-s.incoming:
				if !ok {
					return
				}

				var event Event
				if err := json.Unmarshal(message, &event); err != nil {
					_ = s.SendJSON(errorEvent(wrapF(err, "failed to unmarshal event from SSE connection %s", s.id)))
					continue
				}

				s.mutex.RLock()
				handler := s.handler
				s.mutex.RUnlock()

				if handler == nil {
					_ = s.SendJSON(errorEventWithRequestId(internal(string(gatewayEntity), "no handler registered for SSE connection "+s.id), event.RequestId))
					continue
				}

				if !event.Validate() {
					_ = s.SendJSON(errorEventWithRequestId(internal(string(gatewayEntity), "invalid event received from SSE connection "+s.id), event.RequestId))
					continue
				}

				s.dispatch(event, handler)

			case <-s.ctx.Done():
				return
			case <-s.closeChan:
				return
			}
		}
	}()
}

func (s *SSEConn) dispatch(ev Event, h *transportEventHandler) {
	defer func() {
		if r := recover(); r != nil {
			s.reportError("sse_handler_panic", internal(string(gatewayEntity), "handler panic recovered"))
		}
	}()

	if err := (*h)(ev, s); err != nil {
		s.reportError("sse_handler", err)
		if errEv := errorEventWithRequestId(err, ev.RequestId); errEv != nil {
			_ = s.SendJSON(errEv)
		}
	}
}

func (s *SSEConn) Type() TransportType {
	return TransportSSE
}

func (s *SSEConn) Context() context.Context {
	if s.ctx == nil {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	return s.ctx
}

func (s *SSEConn) PushMessage(data []byte) error {
	if !s.IsActive() {
		return internal(string(gatewayEntity), "SSE connection "+s.id+" is not active")
	}
	if s.options != nil && s.options.MaxMessageSize > 0 && int64(len(data)) > s.options.MaxMessageSize {
		return badRequest(string(gatewayEntity), "SSE message exceeds maximum size")
	}

	select {
	case s.incoming <- data:
		return nil
	default:
	}
	timer := time.NewTimer(s.getSendTimeout())
	defer timer.Stop()
	select {
	case s.incoming <- data:
		return nil
	case <-s.closeChan:
		return internal(string(gatewayEntity), "SSE connection "+s.id+" is closing")
	case <-s.ctx.Done():
		return internal(string(gatewayEntity), "SSE connection "+s.id+" context cancelled")
	case <-timer.C:
		return timeout(string(gatewayEntity), "timeout pushing message to SSE connection "+s.id)
	}
}

func (s *SSEConn) reportError(component string, err error) {
	if err == nil || s == nil || s.options == nil || s.options.Hooks == nil || s.options.Hooks.Metrics == nil {
		return
	}
	s.options.Hooks.Metrics.Error(component, err)
}

func (s *SSEConn) getSendTimeout() time.Duration {
	if s.options != nil && s.options.SendTimeout > 0 {
		return s.options.SendTimeout
	}
	return 5 * time.Second
}
