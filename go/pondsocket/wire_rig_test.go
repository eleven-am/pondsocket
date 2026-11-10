package pondsocket

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type wireRig struct {
	t       *testing.T
	cancel  context.CancelFunc
	srv     *httptest.Server
	manager *Manager
	ep      *Endpoint
	lobby   *Lobby

	mutex  sync.Mutex
	conns  map[string]Transport
	order  []string
	errors []string
	leaves chan *leaveRecord
	discs  chan string
	closes chan string
}

type wireMetrics struct {
	connTestMetricsCollector
	r *wireRig
}

func (m *wireMetrics) Error(component string, err error) {
	if component == "read_pump" {
		return
	}
	m.r.mutex.Lock()
	m.r.errors = append(m.r.errors, component+": "+err.Error())
	m.r.mutex.Unlock()
}

func (r *wireRig) reported() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]string(nil), r.errors...)
}

type leaveRecord struct {
	userID       string
	connectionID string
	reason       string
}

type wireMessage struct {
	Event   string          `json:"event"`
	Action  string          `json:"action"`
	Channel string          `json:"channelName"`
	Payload json.RawMessage `json:"payload"`
}

func newWireRig(t *testing.T, mutate func(*Options), join func(*JoinContext) error) *wireRig {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	r := &wireRig{
		t:      t,
		cancel: cancel,
		conns:  make(map[string]Transport),
		leaves: make(chan *leaveRecord, 1024),
		discs:  make(chan string, 1024),
		closes: make(chan string, 1024),
	}

	opts := DefaultOptions()
	opts.Hooks = &Hooks{
		OnConnect: func(c Transport) error {
			r.mutex.Lock()
			r.conns[c.GetID()] = c
			r.order = append(r.order, c.GetID())
			r.mutex.Unlock()
			c.OnClose(func(tr Transport) error {
				r.closes <- tr.GetID()
				return nil
			})
			return nil
		},
		OnDisconnect: func(c Transport) { r.discs <- c.GetID() },
		Metrics:      &wireMetrics{r: r},
	}
	if mutate != nil {
		mutate(opts)
	}

	r.manager = NewManager(ctx, *opts)
	r.ep = r.manager.CreateEndpoint("/socket", func(c *ConnectionContext) error { return c.Accept() })
	if join == nil {
		join = func(c *JoinContext) error {
			c.Accept()
			return nil
		}
	}
	r.lobby = r.ep.CreateChannel("/room/:id", join)
	r.lobby.OnLeave(func(c *LeaveContext) error {
		r.leaves <- &leaveRecord{userID: c.GetUser().UserID, connectionID: c.ConnectionID(), reason: c.GetReason()}
		return nil
	})
	r.srv = httptest.NewServer(r.manager.HTTPHandler())

	t.Cleanup(r.shutdown)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("reported errors: %v", r.reported())
		}
	})
	return r
}

func (r *wireRig) shutdown() {
	r.srv.CloseClientConnections()
	r.srv.Close()
	r.cancel()
}

func (r *wireRig) channel() *Channel {
	r.t.Helper()
	ch, err := r.ep.GetChannelByName("/room/1")
	if err != nil {
		r.t.Fatalf("channel lookup: %v", err)
	}
	return ch
}

func (r *wireRig) connID(i int) string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.order[i]
}

func (r *wireRig) dial() *websocket.Conn {
	r.t.Helper()
	url := "ws" + strings.TrimPrefix(r.srv.URL, "http") + "/socket"
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		r.t.Fatalf("dial: %v", err)
	}
	return c
}

func readMessages(c *websocket.Conn, timeout time.Duration) ([]wireMessage, error) {
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	_, frame, err := c.ReadMessage()
	if err != nil {
		return nil, err
	}
	var out []wireMessage
	for _, line := range strings.Split(string(frame), "\n") {
		if line == "" {
			continue
		}
		var m wireMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

func (r *wireRig) join(c *websocket.Conn, requestID string) []wireMessage {
	r.t.Helper()
	if err := c.WriteJSON(map[string]any{"action": "JOIN_CHANNEL", "channelName": "/room/1", "requestId": requestID, "event": "JOIN_CHANNEL", "payload": map[string]any{}}); err != nil {
		r.t.Fatalf("write join: %v", err)
	}
	var seen []wireMessage
	for {
		msgs, err := readMessages(c, 5*time.Second)
		if err != nil {
			r.t.Fatalf("waiting for join acknowledgement: %v", err)
		}
		for i, m := range msgs {
			seen = append(seen, m)
			if m.Event == string(acknowledgeEvent) {
				return append(seen, msgs[i+1:]...)
			}
		}
	}
}

func (r *wireRig) dialJoined(requestID string) *websocket.Conn {
	r.t.Helper()
	c := r.dial()
	r.join(c, requestID)
	return c
}
