package pondsocket

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/goleak"
)

func TestConnectionTeardownFiresEachHandlerExactlyOnce(t *testing.T) {
	cases := []struct {
		name    string
		trigger func(r *wireRig, c *websocket.Conn)
	}{
		{
			name: "client sends a normal closure frame",
			trigger: func(r *wireRig, c *websocket.Conn) {
				_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"))
			},
		},
		{
			name: "client drops the TCP connection",
			trigger: func(r *wireRig, c *websocket.Conn) {
				_ = c.UnderlyingConn().Close()
			},
		},
		{
			name: "server closes the connection",
			trigger: func(r *wireRig, c *websocket.Conn) {
				r.mutex.Lock()
				conn := r.conns[r.order[0]]
				r.mutex.Unlock()
				conn.Close()
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

			r := newWireRig(t, nil, nil)
			c := r.dialJoined("j1")
			connID := r.connID(0)

			tc.trigger(r, c)

			expectOne := func(label string, ch <-chan string) {
				t.Helper()
				select {
				case id := <-ch:
					if id != connID {
						t.Errorf("%s fired for %q, want %q", label, id, connID)
					}
				case <-time.After(time.Second):
					t.Fatalf("%s did not fire within 1s", label)
				}
			}

			select {
			case l := <-r.leaves:
				if l.connectionID != connID {
					t.Errorf("leave ConnectionID %q, want %q", l.connectionID, connID)
				}
			case <-time.After(time.Second):
				t.Fatal("channel leave did not fire within 1s")
			}
			expectOne("OnClose", r.closes)
			expectOne("OnDisconnect", r.discs)

			time.Sleep(200 * time.Millisecond)

			if n := len(r.leaves); n != 0 {
				t.Errorf("channel leave fired %d extra time(s)", n)
			}
			if n := len(r.closes); n != 0 {
				t.Errorf("OnClose fired %d extra time(s)", n)
			}
			if n := len(r.discs); n != 0 {
				t.Errorf("OnDisconnect fired %d extra time(s)", n)
			}
			if clients := r.ep.GetClients(); len(clients) != 0 {
				t.Errorf("GetClients() has %d entries after close, want 0", len(clients))
			}

			_ = c.Close()
			r.shutdown()
		})
	}
}

func TestCloseCalledFromInsideReadPathCompletes(t *testing.T) {
	r := newWireRig(t, nil, nil)
	c := r.dialJoined("j1")

	done := make(chan struct{})
	r.mutex.Lock()
	conn := r.conns[r.order[0]]
	r.mutex.Unlock()

	go func() {
		conn.Close()
		conn.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("repeated Close did not return")
	}
	_ = c.Close()
}
