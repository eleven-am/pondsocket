package pondsocket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestPondClient_HeartbeatKeepsIdleConnectionAlive(t *testing.T) {
	var connections atomic.Int32
	var pings atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		conn.SetPingHandler(func(payload string) error {
			pings.Add(1)
			return conn.WriteControl(websocket.PongMessage, []byte(payload), time.Now().Add(time.Second))
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	config := DefaultClientConfig()
	config.PingInterval = 20 * time.Millisecond
	config.PongTimeout = 30 * time.Millisecond
	config.ReadTimeout = 35 * time.Millisecond
	config.ReconnectInterval = 5 * time.Millisecond
	client, err := NewPondClientWithConfig("ws"+strings.TrimPrefix(server.URL, "http"), nil, config)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Disconnect()

	waitFor(t, time.Second, func() bool { return pings.Load() >= 4 }, "four heartbeat pings")
	time.Sleep(2 * config.ReadTimeout)
	if got := connections.Load(); got != 1 {
		t.Fatalf("idle connection reconnected despite pong responses: got %d connections", got)
	}
	if !client.GetState() {
		t.Fatal("client reported disconnected while heartbeat pongs were arriving")
	}
}

func TestPondClient_MissingPongReconnects(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		conn.SetPingHandler(func(string) error { return nil })
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	config := DefaultClientConfig()
	config.PingInterval = 20 * time.Millisecond
	config.PongTimeout = 20 * time.Millisecond
	config.ReconnectInterval = 5 * time.Millisecond
	client, err := NewPondClientWithConfig("ws"+strings.TrimPrefix(server.URL, "http"), nil, config)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer client.Disconnect()

	waitFor(t, time.Second, func() bool { return connections.Load() >= 2 }, "reconnect after missing pong")
}

func TestPondClient_DisconnectStopsHeartbeatAndReconnect(t *testing.T) {
	var connections atomic.Int32
	var pings atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		conn.SetPingHandler(func(payload string) error {
			pings.Add(1)
			return conn.WriteControl(websocket.PongMessage, []byte(payload), time.Now().Add(time.Second))
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	config := DefaultClientConfig()
	config.PingInterval = 15 * time.Millisecond
	config.PongTimeout = 20 * time.Millisecond
	config.ReconnectInterval = 5 * time.Millisecond
	client, err := NewPondClientWithConfig("ws"+strings.TrimPrefix(server.URL, "http"), nil, config)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := client.Connect(); err != nil {
		t.Fatalf("connect: %v", err)
	}
	waitFor(t, time.Second, func() bool { return pings.Load() >= 2 }, "heartbeat before disconnect")

	if err := client.Disconnect(); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	time.Sleep(3 * config.PingInterval)
	connectionsAfterDisconnect := connections.Load()
	pingsAfterDisconnect := pings.Load()
	time.Sleep(5 * config.PingInterval)

	if got := connections.Load(); got != connectionsAfterDisconnect {
		t.Fatalf("client reconnected after explicit disconnect: before=%d after=%d", connectionsAfterDisconnect, got)
	}
	if got := pings.Load(); got != pingsAfterDisconnect {
		t.Fatalf("heartbeat continued after explicit disconnect: before=%d after=%d", pingsAfterDisconnect, got)
	}
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}
