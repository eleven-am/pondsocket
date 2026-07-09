package pondsocket

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestJoinContextDeclineCarriesStatusCode(t *testing.T) {
	ctx := context.Background()
	channel := createContextTestChannel(ctx, "test-channel")

	defer channel.Close()

	conn := createTestConn("user1", nil)
	event := createTestEvent("req-decline", "join", nil)

	joinCtx := newJoinContext(ctx, channel, nil, conn, event)
	if err := joinCtx.Decline(403, "Forbidden"); err != nil {
		t.Fatalf("Decline returned error: %v", err)
	}

	select {
	case data := <-conn.send:
		var sent struct {
			Action  string `json:"action"`
			Event   string `json:"event"`
			Payload struct {
				Code    int `json:"code"`
				Details struct {
					StatusCode int `json:"statusCode"`
				} `json:"details"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(data, &sent); err != nil {
			t.Fatalf("failed to unmarshal decline event: %v", err)
		}
		if sent.Event != string(unauthorizedEvent) {
			t.Errorf("expected event %s, got %s", unauthorizedEvent, sent.Event)
		}
		if sent.Payload.Code != 403 {
			t.Errorf("expected payload.code 403, got %d", sent.Payload.Code)
		}
		if sent.Payload.Details.StatusCode != 403 {
			t.Errorf("expected payload.details.statusCode 403, got %d", sent.Payload.Details.StatusCode)
		}
	case <-time.After(time.Second):
		t.Fatal("expected decline message to be sent")
	}
}

func TestEndpointAutoDeclineOnUnresponsiveHandler(t *testing.T) {
	ctx := context.Background()
	opts := DefaultOptions()

	endpoint := newEndpoint(ctx, "/test", opts)

	handlerCalled := false
	endpoint.CreateChannel("room:*", func(ctx *JoinContext) error {
		handlerCalled = true
		return nil
	})

	conn := createTestConn("user1", nil)
	endpoint.connections.Create(conn.ID, conn)

	ev := &Event{
		Action:      joinChannelEvent,
		ChannelName: "room:123",
		RequestId:   "req-auto",
		Payload:     map[string]interface{}{},
	}

	_ = endpoint.joinChannel(ev, conn)

	if !handlerCalled {
		t.Fatal("expected join handler to be called")
	}

	select {
	case data := <-conn.send:
		var sent struct {
			Event   string `json:"event"`
			Payload struct {
				Code int `json:"code"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(data, &sent); err != nil {
			t.Fatalf("failed to unmarshal auto-decline event: %v", err)
		}
		if sent.Event != string(unauthorizedEvent) {
			t.Errorf("expected event %s, got %s", unauthorizedEvent, sent.Event)
		}
		if sent.Payload.Code != StatusUnauthorized {
			t.Errorf("expected payload.code %d, got %d", StatusUnauthorized, sent.Payload.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("expected auto-decline message to be sent")
	}

	if _, err := channelUserForTest(endpoint, "room:123", "user1"); err == nil {
		t.Error("expected user NOT to be added after auto-decline")
	}
}

func channelUserForTest(e *Endpoint, channelName, userID string) (*User, error) {
	ch, err := e.channels.Read(channelName)
	if err != nil {
		return nil, err
	}
	return ch.GetUser(userID)
}
