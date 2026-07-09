package pondsocket

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type wireFrame struct {
	Action      string          `json:"action"`
	Event       string          `json:"event"`
	ChannelName string          `json:"channelName"`
	RequestId   string          `json:"requestId"`
	Payload     json.RawMessage `json:"payload"`
}

func dialAdversarialServer(t *testing.T, configure func(ep *Endpoint)) (*websocket.Conn, func()) {
	t.Helper()
	opts := &ServerOptions{Options: DefaultOptions(), ServerAddr: ":0"}
	server := NewServer(opts)
	ep := server.CreateEndpoint("/ws", func(ctx *ConnectionContext) error {
		return ctx.Accept()
	})
	configure(ep)
	handler := server.manager.HTTPHandler()
	ts := httptest.NewServer(handler)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, nil)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		ts.Close()
		t.Fatalf("dial failed: %v", err)
	}
	cleanup := func() {
		conn.Close()
		ts.Close()
	}
	return conn, cleanup
}

func collectFrames(t *testing.T, conn *websocket.Conn, window time.Duration) []wireFrame {
	t.Helper()
	var frames []wireFrame
	conn.SetReadDeadline(time.Now().Add(window))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var f wireFrame
			if err := json.Unmarshal([]byte(line), &f); err == nil {
				frames = append(frames, f)
			}
		}
	}
	return frames
}

func drainConnect(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("expected CONNECT frame, got %v", err)
	}
}

func countEvents(frames []wireFrame, event string) int {
	n := 0
	for _, f := range frames {
		if f.Event == event {
			n++
		}
	}
	return n
}

func TestAdversarialBroadcastMissingChannelFrames(t *testing.T) {
	conn, cleanup := dialAdversarialServer(t, func(ep *Endpoint) {
		lobby := ep.CreateChannel("room:*", func(jc *JoinContext) error {
			jc.Accept()
			return nil
		})
		_ = lobby
	})
	defer cleanup()

	drainConnect(t, conn)

	send := Event{Action: broadcast, ChannelName: "ghost", Event: "hello", RequestId: "req-b1", Payload: map[string]interface{}{}}
	if err := conn.WriteJSON(send); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	frames := collectFrames(t, conn, 400*time.Millisecond)
	notFound := countEvents(frames, string(notFoundEvent))
	internalErr := countEvents(frames, string(internalErrorEvent))

	t.Logf("broadcast-to-missing-channel frames: NOT_FOUND=%d INTERNAL_ERROR=%d total=%d", notFound, internalErr, len(frames))
	for i, f := range frames {
		t.Logf("  frame[%d] action=%s event=%s reqId=%s", i, f.Action, f.Event, f.RequestId)
	}

	if notFound != 1 {
		t.Errorf("expected exactly one NOT_FOUND frame, got %d", notFound)
	}
	if internalErr != 0 {
		t.Errorf("expected no duplicate INTERNAL_ERROR frame, got %d", internalErr)
	}
	if len(frames) != 1 {
		t.Errorf("expected exactly one frame for a bad broadcast, got %d", len(frames))
	}
}

func TestAdversarialLeaveMissingChannelFrames(t *testing.T) {
	conn, cleanup := dialAdversarialServer(t, func(ep *Endpoint) {
		ep.CreateChannel("room:*", func(jc *JoinContext) error {
			jc.Accept()
			return nil
		})
	})
	defer cleanup()

	drainConnect(t, conn)

	send := Event{Action: leaveChannelEvent, ChannelName: "ghost", Event: "LEAVE_CHANNEL", RequestId: "req-l1", Payload: map[string]interface{}{}}
	if err := conn.WriteJSON(send); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	frames := collectFrames(t, conn, 400*time.Millisecond)
	notFound := countEvents(frames, string(notFoundEvent))
	internalErr := countEvents(frames, string(internalErrorEvent))
	t.Logf("leave-missing-channel frames: NOT_FOUND=%d INTERNAL_ERROR=%d total=%d", notFound, internalErr, len(frames))

	if notFound != 1 {
		t.Errorf("expected exactly one NOT_FOUND frame, got %d", notFound)
	}
	if internalErr != 0 {
		t.Errorf("expected no duplicate INTERNAL_ERROR frame, got %d", internalErr)
	}
	if len(frames) != 1 {
		t.Errorf("expected exactly one frame for a leave of a missing channel, got %d", len(frames))
	}
}

func TestAdversarialUnknownActionFrames(t *testing.T) {
	conn, cleanup := dialAdversarialServer(t, func(ep *Endpoint) {
		ep.CreateChannel("room:*", func(jc *JoinContext) error {
			jc.Accept()
			return nil
		})
	})
	defer cleanup()

	drainConnect(t, conn)

	send := Event{Action: userCommand, ChannelName: "room:1", Event: "user:evict", RequestId: "req-u1", Payload: map[string]interface{}{}}
	if err := conn.WriteJSON(send); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	frames := collectFrames(t, conn, 400*time.Millisecond)
	notFound := countEvents(frames, string(notFoundEvent))
	internalErr := countEvents(frames, string(internalErrorEvent))
	t.Logf("client-sent-USER_COMMAND frames: NOT_FOUND=%d INTERNAL_ERROR=%d total=%d", notFound, internalErr, len(frames))

	if notFound != 1 {
		t.Errorf("expected exactly one NOT_FOUND frame, got %d", notFound)
	}
	if internalErr != 0 {
		t.Errorf("expected no duplicate INTERNAL_ERROR frame, got %d", internalErr)
	}
	if len(frames) != 1 {
		t.Errorf("expected exactly one frame for a client-sent server-only action, got %d", len(frames))
	}
}

func TestAdversarialPresenceLeaveUpdateShape(t *testing.T) {
	ctx := t.Context()
	channel := createContextTestChannel(ctx, "presence-room")
	defer channel.Close()

	connA := createTestConn("userA", nil)
	connB := createTestConn("userB", nil)
	if err := channel.addUser(connA); err != nil {
		t.Fatalf("addUser A: %v", err)
	}
	if err := channel.addUser(connB); err != nil {
		t.Fatalf("addUser B: %v", err)
	}
	if err := channel.Track("userA", map[string]interface{}{"n": "a"}); err != nil {
		t.Fatalf("track A: %v", err)
	}
	if err := channel.Track("userB", map[string]interface{}{"n": "b"}); err != nil {
		t.Fatalf("track B: %v", err)
	}
	drainConn(connA)
	drainConn(connB)

	if err := channel.UpdatePresence("userB", map[string]interface{}{"n": "b2"}); err != nil {
		t.Fatalf("update B: %v", err)
	}
	upd := waitForPresenceFrame(t, connA, "UPDATE", time.Second)
	updPayload := upd["payload"].(map[string]interface{})
	if _, ok := updPayload["changed"]; !ok {
		t.Errorf("UPDATE payload missing 'changed', keys=%v", keysOf(updPayload))
	}
	if _, ok := updPayload["change"]; ok {
		t.Errorf("UPDATE payload must not carry legacy 'change'")
	}

	drainConn(connA)
	if err := channel.UnTrack("userB"); err != nil {
		t.Fatalf("untrack B: %v", err)
	}
	lv := waitForPresenceFrame(t, connA, "LEAVE", time.Second)
	lvPayload := lv["payload"].(map[string]interface{})
	if _, ok := lvPayload["changed"]; !ok {
		t.Errorf("LEAVE payload missing 'changed', keys=%v", keysOf(lvPayload))
	}
}

func TestAdversarialConcurrentPresenceRace(t *testing.T) {
	ctx := t.Context()
	channel := createContextTestChannel(ctx, "race-room")
	defer channel.Close()

	for i := 0; i < 8; i++ {
		conn := createTestConn(userIDForRace(i), nil)
		if err := channel.addUser(conn); err != nil {
			t.Fatalf("addUser %d: %v", i, err)
		}
		go func() {
			for {
				select {
				case <-conn.send:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		id := userIDForRace(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = channel.Track(id, map[string]interface{}{"v": 0})
			for j := 0; j < 50; j++ {
				_ = channel.UpdatePresence(id, map[string]interface{}{"v": j})
			}
			_ = channel.UnTrack(id)
		}()
	}
	wg.Wait()
}

func userIDForRace(i int) string {
	return "u" + string(rune('0'+i))
}
