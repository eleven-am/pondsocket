// This file contains the Manager struct which handles WebSocket upgrades, HTTP routing,
// origin checking, and endpoint management. The Manager acts as the central coordinator
// between the HTTP server and WebSocket endpoints.
package pondsocket

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"net/http"
	"regexp"
	"time"
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
	return m.endpoints.items
}

func createOriginChecker(opts *Options) func(*http.Request) bool {
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
			return false
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
// - No origin checking (accepts all origins)
// - 1KB read/write buffers
// - 512KB max message size
// - 30s ping interval, 60s pong wait
// - Compression disabled
// - 256 buffer size for send/receive channels
func DefaultOptions() *Options {
	return &Options{
		CheckOrigin:          false,
		ReadBufferSize:       1024,
		WriteBufferSize:      1024,
		MaxMessageSize:       512 * 1024,
		PingInterval:         30 * time.Second,
		PongWait:             60 * time.Second,
		WriteWait:            10 * time.Second,
		EnableCompression:    false,
		SendChannelBuffer:    256,
		ReceiveChannelBuffer: 256,
		InternalQueueTimeout: 1 * time.Second,
	}
}

// NewManager creates a new Manager instance with the provided context and options.
// The manager handles WebSocket upgrades, origin checking, and endpoint routing.
// If no options are provided, default options will be used.
// If no PubSub implementation is provided in options, a local in-memory PubSub will be created.
func NewManager(ctx context.Context, options ...Options) *Manager {
	opts := DefaultOptions()

	if len(options) > 0 {
		opts = &options[0]
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize:    opts.ReadBufferSize,
		WriteBufferSize:   opts.WriteBufferSize,
		CheckOrigin:       createOriginChecker(opts),
		EnableCompression: opts.EnableCompression,
	}
	httpMiddleware := newMiddleWare[*http.Request, *http.ResponseWriter]()

	if opts.PubSub == nil {
		opts.PubSub = NewLocalPubSub(ctx, 100)
	}
	return &Manager{
		middleware: httpMiddleware,
		Options:    opts,
		upgrader:   upgrader,
		ctx:        ctx,
		endpoints:  newArray[*Endpoint](),
	}
}

// CreateEndpoint registers a new WebSocket endpoint at the specified path pattern.
// The path can include parameters (e.g., "/room/:id") and wildcards.
// The handlerFunc is called for each new connection attempt, allowing custom
// authentication, validation, and connection setup logic.
// Returns the created Endpoint which can be used to configure channels and message handling.
func (m *Manager) CreateEndpoint(path string, handlerFunc ConnectionEventHandler) *Endpoint {
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

		return handlerFunc(connCtx)
	})

	return endpoint
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
				return
			}
			if w.Header().Get("Content-Type") == "" {
				http.Error(w, errMsg, statusCode)
			}
		}
	}
}
