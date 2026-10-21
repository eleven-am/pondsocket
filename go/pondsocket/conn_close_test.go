package pondsocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestClientCloseFrameClosesConnectionWithoutDeadlock(t *testing.T) {
	opts := DefaultOptions()
	endpoint := newEndpoint(context.Background(), "/test", opts)
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	accepted := make(chan *Conn, 1)
	var handlerRan atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)

			return
		}
		conn, err := newConn(context.Background(), raw, map[string]interface{}{}, "close-frame-client", opts)
		if err != nil {
			t.Errorf("newConn failed: %v", err)

			return
		}
		conn.OnClose(func(Transport) error {
			handlerRan.Store(true)

			return nil
		})
		if err := endpoint.addConnection(conn); err != nil {
			t.Errorf("addConnection failed: %v", err)

			return
		}
		accepted <- conn
	}))
	defer server.Close()

	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer client.Close()

	var conn *Conn
	select {
	case conn = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not accept the connection")
	}
	if len(endpoint.GetClients()) != 1 {
		t.Fatalf("expected 1 registered client, got %d", len(endpoint.GetClients()))
	}

	deadline := time.Now().Add(time.Second)
	if err := client.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), deadline); err != nil {
		t.Fatalf("write close frame failed: %v", err)
	}

	closed := make(chan struct{})
	go func() {
		conn.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close deadlocked after the client sent a close frame")
	}

	waitForMembershipState(t, func() bool { return len(endpoint.GetClients()) == 0 })
	if !handlerRan.Load() {
		t.Fatal("close handlers did not run")
	}
}
