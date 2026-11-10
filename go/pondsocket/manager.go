// This file contains the Manager struct which handles WebSocket upgrades, HTTP routing,
// origin checking, and endpoint management. The Manager acts as the central coordinator
// between the HTTP server and WebSocket endpoints.
package pondsocket

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type Manager struct {
	middleware *middleware[*http.Request, *http.ResponseWriter]
	Options    *Options
	endpoints  *array[*Endpoint]
	upgrader   websocket.Upgrader
	ctx        context.Context
}

// GetEndpoints returns a slice of all registered endpoints in the manager.
// This can be used to inspect or iterate over all active endpoints.
func (m *Manager) GetEndpoints() []*Endpoint {
	return m.endpoints.snapshot()
}

func createOriginChecker(opts *Options) func(*http.Request) bool {
	if opts.AllowAnyOrigin {
		return func(*http.Request) bool { return true }
	}
	if opts.CheckOriginFunc != nil {
		return opts.CheckOriginFunc
	}

	var compiledRegexps []*regexp.Regexp
	if opts.CheckOrigin && len(opts.AllowedOriginRegexps) > 0 {
		compiledRegexps = make([]*regexp.Regexp, 0, len(opts.AllowedOriginRegexps))

		for _, pattern := range opts.AllowedOriginRegexps {
			compiledRegexps = append(compiledRegexps, pattern)
		}
	}

	return func(r *http.Request) bool {
		if !opts.CheckOrigin {
			return true
		}
		origin := r.Header.Get("Origin")

		if origin == "" {
			return true
		}
		if len(opts.AllowedOrigins) == 0 && len(compiledRegexps) == 0 {
			parsed, err := url.Parse(origin)
			return err == nil && strings.EqualFold(parsed.Host, r.Host)
		}
		for _, allowed := range opts.AllowedOrigins {
			if allowed == "*" {
				return true
			}
			if allowed == origin {
				return true
			}
		}
		for _, pattern := range compiledRegexps {
			if pattern.MatchString(origin) {
				return true
			}
		}
		return false
	}
}

// DefaultOptions returns a new Options struct with sensible default values.
// These defaults provide a good starting point for most applications:
// - Same-origin browser connections (set AllowAnyOrigin only for deliberate public endpoints)
// - 1KB read/write buffers
// - 512KB max message size
// - 30s ping interval, 60s pong wait
// - Compression disabled
// - 64-message bounded send/receive queues
// - Messages from one connection are handled serially in arrival order
func DefaultOptions() *Options {
	return &Options{
		CheckOrigin:          true,
		ReadBufferSize:       1024,
		WriteBufferSize:      1024,
		MaxMessageSize:       512 * 1024,
		PingInterval:         30 * time.Second,
		PongWait:             60 * time.Second,
		WriteWait:            10 * time.Second,
		SendTimeout:          5 * time.Second,
		EnableCompression:    false,
		SendChannelBuffer:    64,
		ReceiveChannelBuffer: 64,
		InternalQueueTimeout: 1 * time.Second,
	}
}

func (o *Options) fillDefaults() {
	defaults := DefaultOptions()
	if !o.AllowAnyOrigin {
		o.CheckOrigin = defaults.CheckOrigin
	}

	if o.ReadBufferSize <= 0 {
		o.ReadBufferSize = defaults.ReadBufferSize
	}
	if o.WriteBufferSize <= 0 {
		o.WriteBufferSize = defaults.WriteBufferSize
	}
	if o.MaxMessageSize <= 0 {
		o.MaxMessageSize = defaults.MaxMessageSize
	}
	if o.PingInterval <= 0 {
		o.PingInterval = defaults.PingInterval
	}
	if o.PongWait <= 0 {
		o.PongWait = defaults.PongWait
	}
	if o.WriteWait <= 0 {
		o.WriteWait = defaults.WriteWait
	}
	if o.SendTimeout <= 0 {
		o.SendTimeout = defaults.SendTimeout
	}
	if o.SendChannelBuffer <= 0 {
		o.SendChannelBuffer = defaults.SendChannelBuffer
	}
	if o.ReceiveChannelBuffer <= 0 {
		o.ReceiveChannelBuffer = defaults.ReceiveChannelBuffer
	}
	if o.InternalQueueTimeout <= 0 {
		o.InternalQueueTimeout = defaults.InternalQueueTimeout
	}
}

// NewManager creates a new Manager instance with the provided context and options.
// The manager handles WebSocket upgrades, origin checking, and endpoint routing.
// If no options are provided, default options will be used.
// PubSub is opt-in. Single-node servers deliver directly without paying the
// serialization, goroutine, and heartbeat cost of a broker loopback.
func NewManager(ctx context.Context, options ...Options) *Manager {
	optionCopy := *DefaultOptions()

	if len(options) > 0 {
		optionCopy = options[0]
		optionCopy.fillDefaults()
	}

	opts := optionCopy

	upgrader := websocket.Upgrader{
		ReadBufferSize:    opts.ReadBufferSize,
		WriteBufferSize:   opts.WriteBufferSize,
		CheckOrigin:       createOriginChecker(&opts),
		EnableCompression: opts.EnableCompression,
	}
	httpMiddleware := newMiddleWare[*http.Request, *http.ResponseWriter]()

	return &Manager{
		middleware: httpMiddleware,
		Options:    &opts,
		upgrader:   upgrader,
		ctx:        ctx,
		endpoints:  newArray[*Endpoint](),
	}
}

func (m *Manager) CreateEndpoint(path string, handlerFunc ConnectionEventHandler, middlewares ...ConnectionMiddleware) *Endpoint {
	select {
	case <-m.ctx.Done():
		return newEndpoint(m.ctx, path, m.Options)

	default:
	}
	endpoint := newEndpoint(m.ctx, path, m.Options)

	m.endpoints.push(endpoint)

	m.middleware.Use(func(ctx context.Context, request *http.Request, response *http.ResponseWriter, next nextFunc) error {
		select {
		case <-ctx.Done():
			return ctx.Err()

		default:
		}
		route, err := parse(path, request.URL.Path)

		if err != nil {
			return next()
		}
		if err := m.setSSECORSHeaders(*response); err != nil {
			http.Error(*response, err.Error(), http.StatusInternalServerError)
			return nil
		}

		switch request.Method {
		case http.MethodGet:
			return m.handleConnection(ctx, request, response, endpoint, route, handlerFunc, middlewares)

		case http.MethodPost:
			return m.handleSSEMessage(request, response, endpoint)

		case http.MethodDelete:
			return m.handleSSEDisconnect(request, response, endpoint)

		case http.MethodOptions:
			if m.Options.CORSAllowOrigin == "" {
				return next()
			}
			(*response).Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			(*response).Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Connection-ID")
			(*response).WriteHeader(http.StatusNoContent)
			return nil

		default:
			return next()
		}
	})

	return endpoint
}

func (m *Manager) setSSECORSHeaders(response http.ResponseWriter) error {
	if m.Options.CORSAllowOrigin == "" {
		return nil
	}
	if m.Options.CORSAllowCredentials && m.Options.CORSAllowOrigin == "*" {
		return badRequest(string(gatewayEntity), "credentialed SSE CORS requires an explicit origin")
	}
	response.Header().Set("Access-Control-Allow-Origin", m.Options.CORSAllowOrigin)
	response.Header().Set("Access-Control-Expose-Headers", "X-Connection-ID")
	if m.Options.CORSAllowCredentials {
		response.Header().Set("Access-Control-Allow-Credentials", "true")
	}
	return nil
}

func (m *Manager) handleConnection(ctx context.Context, request *http.Request, response *http.ResponseWriter, endpoint *Endpoint, route *Route, handlerFunc ConnectionEventHandler, middlewares []ConnectionMiddleware) error {
	userId := uuid.NewString()

	connOpts := connectionOptions{
		request:  request,
		response: response,
		endpoint: endpoint,
		userId:   userId,
		upgrader: m.upgrader,
		route:    route,
		connCtx:  ctx,
	}

	connCtx := newConnectionContext(connOpts)
	if err := executeWithMiddleware(connCtx, func(ctx *ConnectionContext) error {
		return handlerFunc(ctx)
	}, middlewares); err != nil {
		connCtx.connectionCancel()
		if conn, readErr := endpoint.connections.Read(userId); readErr == nil && conn != nil {
			conn.Close()
		}
		return err
	}

	if connCtx.sseConn != nil {
		connCtx.sseConn.Wait()
		connCtx.sseConn.Close()
	}

	return nil
}

func (m *Manager) handleSSEMessage(request *http.Request, response *http.ResponseWriter, endpoint *Endpoint) error {
	connID := request.Header.Get("X-Connection-ID")
	if connID == "" {
		http.Error(*response, "X-Connection-ID header required", http.StatusBadRequest)
		return nil
	}

	maxMessageSize := m.Options.MaxMessageSize
	if maxMessageSize > 0 {
		request.Body = http.MaxBytesReader(*response, request.Body, maxMessageSize)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(*response, "Request body too large", http.StatusRequestEntityTooLarge)
			return nil
		}
		http.Error(*response, "Failed to read request body", http.StatusBadRequest)
		return nil
	}
	defer request.Body.Close()

	if err := endpoint.pushMessage(connID, body); err != nil {
		http.Error(*response, err.Error(), http.StatusNotFound)
		return nil
	}

	(*response).WriteHeader(http.StatusAccepted)
	return nil
}

func (m *Manager) handleSSEDisconnect(request *http.Request, response *http.ResponseWriter, endpoint *Endpoint) error {
	connID := request.Header.Get("X-Connection-ID")
	if connID == "" {
		http.Error(*response, "X-Connection-ID header required", http.StatusBadRequest)
		return nil
	}

	if err := endpoint.CloseConnection(connID); err != nil {
		http.Error(*response, err.Error(), http.StatusNotFound)
		return nil
	}

	(*response).WriteHeader(http.StatusNoContent)
	return nil
}

// HTTPHandler returns an http.HandlerFunc that processes incoming HTTP requests.
// This handler manages WebSocket upgrades and routes requests to appropriate endpoints.
// It should be used as the handler for the HTTP server.
// The handler automatically manages context merging and error responses.
func (m *Manager) HTTPHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mergedCtx, cancel := mergeContexts(m.ctx, r.Context())

		defer cancel()

		err := m.middleware.Handle(mergedCtx, r, &w, func(req *http.Request, rw *http.ResponseWriter) error {
			http.Error(*rw, "Not Found", http.StatusNotFound)

			return nil
		})

		if err != nil {
			statusCode := http.StatusInternalServerError
			errMsg := "Internal Server Error"
			if errors.Is(err, context.Canceled) {
				statusCode = 499
				errMsg = "Client Closed Request"
			} else if errors.Is(err, context.DeadlineExceeded) {
				statusCode = http.StatusGatewayTimeout
				errMsg = "Gateway Timeout"
			} else {
				m.reportError("http_handler", err)
			}
			if w.Header().Get("Content-Type") == "" {
				http.Error(w, errMsg, statusCode)
			}
		}
	}
}

func (m *Manager) reportError(component string, err error) {
	if err == nil || m == nil || m.Options == nil || m.Options.Hooks == nil || m.Options.Hooks.Metrics == nil {
		return
	}
	m.Options.Hooks.Metrics.Error(component, err)
}
