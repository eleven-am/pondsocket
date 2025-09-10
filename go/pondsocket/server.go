// package pondsocket provides a WebSocket server implementation with channel-based messaging.
// This file contains the main Server struct which manages the HTTP server lifecycle,
// WebSocket endpoints, and graceful shutdown handling.
package pondsocket

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type Server struct {
	server    *http.Server
	manager   *Manager
	mutex     sync.RWMutex
	isRunning bool
	ctx       context.Context
	cancel    context.CancelFunc
}

// NewServer creates a new PondSocket server instance with the provided options.
// It initializes the server with a manager for handling WebSocket connections and
// configures the HTTP server with the specified address, timeouts, and TLS settings.
// If no options are provided, default values will be used.
func NewServer(options *ServerOptions) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	opts := options.Options
	if opts == nil {
		opts = DefaultOptions()
	}
	manager := NewManager(ctx, *opts)

	addr := options.ServerAddr
	if addr == "" {
		addr = ":8080"
	}
	return &Server{
		ctx:     ctx,
		cancel:  cancel,
		manager: manager,
		server: &http.Server{
			Addr:         addr,
			Handler:      manager.HTTPHandler(),
			ReadTimeout:  options.ServerReadTimeout,
			WriteTimeout: options.ServerWriteTimeout,
			IdleTimeout:  options.ServerIdleTimeout,
			TLSConfig:    options.ServerTLSConfig,
		},
	}
}

// CreateEndpoint creates a new WebSocket endpoint at the specified path.
// The handlerFunc will be called for each new WebSocket connection attempt,
// allowing you to accept or decline connections based on custom logic.
// Returns an Endpoint instance that can be used to create channels and configure message handling.
func (s *Server) CreateEndpoint(path string, handlerFunc ConnectionEventHandler) *Endpoint {
	return s.manager.CreateEndpoint(path, handlerFunc)
}

// Start begins listening for HTTP/WebSocket connections on the configured address.
// This method starts the server in a background goroutine and returns immediately.
// If the server is already running, it returns an error.
// For TLS connections, ensure TLSConfig is set in ServerOptions.
func (s *Server) Start() error {
	s.mutex.Lock()

	if s.isRunning {
		s.mutex.Unlock()

		return internal("SERVER", "Server is already running")
	}
	s.isRunning = true
	s.mutex.Unlock()

	go func() {
		if s.server.TLSConfig != nil {
			s.server.ListenAndServeTLS("", "")
		} else {
			s.server.ListenAndServe()
		}

		s.mutex.Lock()

		s.isRunning = false
		s.mutex.Unlock()
	}()

	return nil
}

// Listen starts the server and blocks until a shutdown signal is received (SIGINT or SIGTERM).
// It provides a convenient way to run the server with automatic graceful shutdown handling.
// The server will wait up to 30 seconds for active connections to close during shutdown.
func (s *Server) Listen() error {
	if err := s.Start(); err != nil {
		return err
	}
	quit := make(chan os.Signal, 1)

	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	<-quit
	shutdownTimeout := 30 * time.Second
	if err := s.Stop(shutdownTimeout); err != nil {
		return wrapF(err, "error during server shutdown")
	}
	return nil
}

// IsRunning returns true if the server is currently accepting connections.
// This method is thread-safe and can be called concurrently.
func (s *Server) IsRunning() bool {
	s.mutex.RLock()

	defer s.mutex.RUnlock()

	return s.isRunning
}

// Stop gracefully shuts down the server with the given timeout.
// It stops accepting new connections and waits for existing connections to close.
// If the timeout is exceeded, the server is forcefully closed.
// Returns nil if the server was not running or shutdown completed successfully.
func (s *Server) Stop(timeout time.Duration) error {
	s.mutex.Lock()

	if !s.isRunning {
		s.mutex.Unlock()

		return nil
	}
	s.mutex.Unlock()

	s.cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), timeout)

	defer shutdownCancel()

	err := s.server.Shutdown(shutdownCtx)

	if err != nil {
		return wrapF(err, "http server shutdown failed")
	}
	return nil
}
