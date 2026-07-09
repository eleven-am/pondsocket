package pondsocket

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func readConnFrame(t *testing.T, conn *Conn, timeout time.Duration) map[string]interface{} {
	t.Helper()
	select {
	case data := <-conn.send:
		var m map[string]interface{}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("failed to unmarshal frame: %v", err)
		}
		return m
	case <-time.After(timeout):
		t.Fatal("expected a frame to be sent")
		return nil
	}
}

func TestAdversarialDeclineCodeVariants(t *testing.T) {
	cases := []int{0, 403, 999, StatusUnauthorized}
	for _, code := range cases {
		ctx := context.Background()
		channel := createContextTestChannel(ctx, "test-channel")
		conn := createTestConn("user1", nil)
		event := createTestEvent("req-decline", "join", nil)
		joinCtx := newJoinContext(ctx, channel, nil, conn, event)

		if err := joinCtx.Decline(code, "nope"); err != nil {
			t.Fatalf("Decline(%d) returned error: %v", code, err)
		}
		frame := readConnFrame(t, conn, time.Second)
		if frame["event"] != string(unauthorizedEvent) {
			t.Errorf("Decline(%d): expected event %s, got %v", code, unauthorizedEvent, frame["event"])
		}
		payload, ok := frame["payload"].(map[string]interface{})
		if !ok {
			t.Fatalf("Decline(%d): payload not an object: %T", code, frame["payload"])
		}
		if int(payload["code"].(float64)) != code {
			t.Errorf("Decline(%d): payload.code = %v, want %d", code, payload["code"], code)
		}
		details, ok := payload["details"].(map[string]interface{})
		if !ok {
			t.Fatalf("Decline(%d): details not an object: %T", code, payload["details"])
		}
		if int(details["statusCode"].(float64)) != code {
			t.Errorf("Decline(%d): details.statusCode = %v, want %d", code, details["statusCode"], code)
		}
		channel.Close()
	}
}

func TestAdversarialErrorEventRequestIdEcho(t *testing.T) {
	ev := errorEventWithRequestId(internal(string(gatewayEntity), "boom"), "client-req-42")
	if ev.RequestId != "client-req-42" {
		t.Errorf("expected echoed requestId client-req-42, got %q", ev.RequestId)
	}

	ev2 := errorEventWithRequestId(internal(string(gatewayEntity), "boom"), "")
	if ev2.RequestId == "" {
		t.Error("empty inbound requestId must fall back to a non-empty uuid, got empty string")
	}
	if ev2.RequestId == "client-req-42" {
		t.Error("fallback uuid unexpectedly equals prior id")
	}

	ev3 := errorEvent(internal(string(gatewayEntity), "boom"))
	if ev3.RequestId == "" {
		t.Error("errorEvent must not emit an empty requestId")
	}
}

func TestAdversarialAcceptThenDeclineSuppressed(t *testing.T) {
	ctx := context.Background()
	channel := createContextTestChannel(ctx, "test-channel")
	defer channel.Close()

	conn := createTestConn("user1", nil)
	event := createTestEvent("req-1", "join", nil)
	joinCtx := newJoinContext(ctx, channel, nil, conn, event)

	joinCtx.Accept()
	if joinCtx.err != nil {
		t.Fatalf("Accept set err: %v", joinCtx.err)
	}

	err := joinCtx.Decline(403, "too late")
	if err == nil {
		t.Fatal("expected Decline after Accept to return an error, got nil")
	}
	var pe *Error
	if !asPondError(err, &pe) || pe.Code != StatusBadRequest {
		t.Errorf("expected badRequest(400) from double response, got %v", err)
	}
}

func asPondError(err error, target **Error) bool {
	for err != nil {
		if e, ok := err.(*Error); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestAdversarialExplicitDeclineSingleFrame(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()
	endpoint := newEndpoint(ctx, "/test", opts)

	endpoint.CreateChannel("room:*", func(jc *JoinContext) error {
		return jc.Decline(403, "forbidden")
	})

	conn := createTestConn("user1", nil)
	endpoint.connections.Create(conn.ID, conn)

	ev := &Event{
		Action:      joinChannelEvent,
		ChannelName: "room:1",
		RequestId:   "req-x",
		Payload:     map[string]interface{}{},
	}
	_ = endpoint.joinChannel(ev, conn)

	frame := readConnFrame(t, conn, time.Second)
	payload := frame["payload"].(map[string]interface{})
	if int(payload["code"].(float64)) != 403 {
		t.Errorf("expected code 403, got %v", payload["code"])
	}

	select {
	case extra := <-conn.send:
		t.Fatalf("expected exactly one frame after explicit decline, got a second: %s", string(extra))
	case <-time.After(150 * time.Millisecond):
	}
}

func TestAdversarialPresenceFrameShape(t *testing.T) {
	ctx := context.Background()
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

	drainConn(connA)
	drainConn(connB)

	if err := channel.Track("userA", map[string]interface{}{"name": "alice"}); err != nil {
		t.Fatalf("Track A: %v", err)
	}
	if err := channel.Track("userB", map[string]interface{}{"name": "bob"}); err != nil {
		t.Fatalf("Track B: %v", err)
	}

	frame := waitForPresenceFrame(t, connA, "JOIN", time.Second)

	if frame["action"] != string(presence) {
		t.Errorf("expected action PRESENCE, got %v", frame["action"])
	}
	payload, ok := frame["payload"].(map[string]interface{})
	if !ok {
		t.Fatalf("payload not an object: %T", frame["payload"])
	}
	if _, has := payload["changed"]; !has {
		t.Errorf("presence payload must carry 'changed' key, got keys %v", keysOf(payload))
	}
	if _, has := payload["change"]; has {
		t.Errorf("presence payload must NOT carry legacy 'change' key")
	}
	if _, has := payload["presence"]; !has {
		t.Errorf("JOIN presence payload must carry 'presence' list")
	}
}

func drainConn(conn *Conn) {
	for {
		select {
		case <-conn.send:
		default:
			return
		}
	}
}

func keysOf(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func waitForPresenceFrame(t *testing.T, conn *Conn, event string, timeout time.Duration) map[string]interface{} {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case data := <-conn.send:
			var m map[string]interface{}
			if err := json.Unmarshal(data, &m); err != nil {
				continue
			}
			if m["event"] == event {
				return m
			}
		case <-deadline:
			t.Fatalf("timed out waiting for presence %s frame", event)
			return nil
		}
	}
}
