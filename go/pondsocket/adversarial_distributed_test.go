package pondsocket

import (
	"testing"
)

func encodeDecodePresence(t *testing.T, evName presenceEventType) *Event {
	t.Helper()
	evt := Event{
		Action:      presence,
		ChannelName: "room:1",
		Event:       string(evName),
		NodeID:      "node-A",
		Payload: presencePayload{
			Event:    evName,
			UserID:   "u1",
			Change:   map[string]interface{}{"name": "alice"},
			Presence: []interface{}{map[string]interface{}{"name": "alice"}},
		},
	}
	data, err := distributedBytesFromEvent("socket", evt, "CHANNEL", "ALL_USERS")
	if err != nil {
		t.Fatalf("encode %s: %v", evName, err)
	}
	ev, ok := eventFromDistributedBytes(data)
	if !ok {
		t.Fatalf("decode %s: not ok", evName)
	}
	return ev
}

func TestAdversarialCrossNodeJoinDowngradedToUpdate(t *testing.T) {
	ev := encodeDecodePresence(t, join)

	if ev.Event != string(join) {
		t.Errorf("cross-node JOIN must stay JOIN, got %q", ev.Event)
	}

	payload, ok := ev.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("payload type %T", ev.Payload)
	}
	if _, has := payload["changed"]; !has {
		t.Errorf("cross-node presence payload missing 'changed'")
	}
	if _, has := payload["presence"]; !has {
		t.Errorf("cross-node JOIN must carry the full 'presence' snapshot list")
	}
}

func TestAdversarialCrossNodeLeaveUpdatePreserved(t *testing.T) {
	lv := encodeDecodePresence(t, leave)
	if lv.Event != string(leave) {
		t.Errorf("cross-node LEAVE must stay LEAVE, got %q", lv.Event)
	}

	up := encodeDecodePresence(t, update)
	if up.Event != string(update) {
		t.Errorf("cross-node UPDATE must stay UPDATE, got %q", up.Event)
	}
	payload, ok := up.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("payload type %T", up.Payload)
	}
	if _, has := payload["changed"]; !has {
		t.Errorf("cross-node UPDATE payload missing 'changed'")
	}
}

func TestAdversarialSelfMessageDedup(t *testing.T) {
	ctx := t.Context()
	channel := createContextTestChannel(ctx, "room:1")
	defer channel.Close()
	channel.endpointPath = "/socket"

	conn := createTestConn("u1", nil)
	if err := channel.addUser(conn); err != nil {
		t.Fatalf("addUser: %v", err)
	}
	drainConn(conn)

	evt := Event{
		Action:      presence,
		ChannelName: "room:1",
		Event:       string(update),
		NodeID:      channel.nodeID,
		Payload: presencePayload{
			Event:  update,
			UserID: "u1",
			Change: map[string]interface{}{"name": "self"},
		},
	}
	data, err := distributedBytesFromEvent("socket", evt, "CHANNEL", "ALL_USERS")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	channel.handleDistributedMessage(data)

	select {
	case frame := <-conn.send:
		t.Errorf("self-originated distributed message must be dropped, but a frame was delivered: %s", string(frame))
	default:
	}
}
