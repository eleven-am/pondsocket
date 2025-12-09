package pondsocket

import (
	"context"
	"encoding/json"
	"net/http"
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

	ctx, cancel := context.WithCancel(opts.parentCtx)

	assigns := opts.assigns
	if assigns == nil {
		assigns = make(map[string]interface{})
	}

	bufferSize := 256
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

	if event, ok := v.(Event); ok && event.Event != "" {
		_, err = s.writer.Write([]byte("event: " + event.Event + "\n"))
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
	s.closeOnce.Do(func() {
		s.mutex.Lock()
		s.isClosing = true
		handlersToRun := make([]func(Transport) error, len(s.closeHandlers.items))
		copy(handlersToRun, s.closeHandlers.items)
		s.mutex.Unlock()

		if s.cancel != nil {
			s.cancel()
		}

		close(s.closeChan)

		var closeHandlerErrors error
		for _, handler := range handlersToRun {
			if err := handler(s); err != nil {
				closeHandlerErrors = addError(closeHandlerErrors, err)
			}
		}

		if closeHandlerErrors != nil {
			s.reportError("sse_close_handlers", closeHandlerErrors)
		}
	})
}

func (s *SSEConn) Wait() {
	select {
	case <-s.closeChan:
	case <-s.ctx.Done():
	}
}

func (s *SSEConn) OnClose(callback func(Transport) error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.closeHandlers.push(callback)
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
					_ = s.SendJSON(errorEvent(internal(string(gatewayEntity), "no handler registered for SSE connection "+s.id)))
					continue
				}

				if !event.Validate() {
					_ = s.SendJSON(errorEvent(internal(string(gatewayEntity), "invalid event received from SSE connection "+s.id)))
					continue
				}

				if err := (*handler)(event, s); err != nil {
					s.reportError("sse_handler", err)
					if ev := errorEvent(err); ev != nil {
						_ = s.SendJSON(ev)
					}
				}

			case <-s.ctx.Done():
				return
			case <-s.closeChan:
				return
			}
		}
	}()
}

func (s *SSEConn) Type() TransportType {
	return TransportSSE
}

func (s *SSEConn) PushMessage(data []byte) error {
	if !s.IsActive() {
		return internal(string(gatewayEntity), "SSE connection "+s.id+" is not active")
	}

	select {
	case s.incoming <- data:
		return nil
	case <-s.closeChan:
		return internal(string(gatewayEntity), "SSE connection "+s.id+" is closing")
	case <-s.ctx.Done():
		return internal(string(gatewayEntity), "SSE connection "+s.id+" context cancelled")
	case <-time.After(s.getSendTimeout()):
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
