package pondsocket

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAdversarialDebugFrames(t *testing.T) {
	opts := &ServerOptions{Options: DefaultOptions(), ServerAddr: ":0"}
	server := NewServer(opts)
	ep := server.CreateEndpoint("/ws", func(ctx *ConnectionContext) error {
		return ctx.Accept()
	})
	ep.CreateChannel("room:*", func(jc *JoinContext) error {
		jc.Accept()
		return nil
	})
	handler := server.manager.HTTPHandler()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, first, err := conn.ReadMessage()
	t.Logf("first frame err=%v data=%s", err, string(first))

	if err := conn.WriteJSON(Event{Action: broadcast, ChannelName: "ghost", Event: "hello", RequestId: "req-b1", Payload: map[string]interface{}{}}); err != nil {
		t.Fatalf("write: %v", err)
	}

	for i := 0; i < 4; i++ {
		conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Logf("read[%d] err=%v", i, err)
			break
		}
		t.Logf("read[%d] data=%s", i, string(data))
	}
}
