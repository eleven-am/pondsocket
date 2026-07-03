package pondsocket

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"time"
)

func decodeWire(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to unmarshal wire message: %v", err)
	}
	return m
}

func assertEnvelope(t *testing.T, m map[string]interface{}, msgType string) {
	t.Helper()
	if m["protocol"] != distributedProtocol {
		t.Errorf("protocol = %v, want %s", m["protocol"], distributedProtocol)
	}
	if v, ok := m["version"].(float64); !ok || int(v) != distributedVersion {
		t.Errorf("version = %v, want %d", m["version"], distributedVersion)
	}
	if m["type"] != msgType {
		t.Errorf("type = %v, want %s", m["type"], msgType)
	}
	for _, key := range []string{"messageId", "sourceNodeId", "endpointName", "channelName", "timestamp"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing envelope key %q", key)
		}
	}
}

func mustHave(t *testing.T, m map[string]interface{}, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Errorf("expected wire key %q to be present", k)
		}
	}
}

func mustNotHave(t *testing.T, m map[string]interface{}, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; ok {
			t.Errorf("expected wire key %q to be absent", k)
		}
	}
}

func TestDistributedRoundTripAllTypes(t *testing.T) {
	const endpoint = "socket"
	const channel = "room:1"
	const node = "node-A"

	t.Run("USER_MESSAGE", func(t *testing.T) {
		ev := Event{
			Action:      broadcast,
			ChannelName: channel,
			RequestId:   "req-1",
			Event:       "chat",
			Payload:     map[string]interface{}{"text": "hi"},
			NodeID:      node,
		}
		data, err := distributedBytesFromEvent(endpoint, ev, "user-1", "ALL_USERS")
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgUserMessage)
		mustHave(t, m, "fromUserId", "event", "payload", "requestId", "recipientDescriptor")
		if m["fromUserId"] != "user-1" {
			t.Errorf("fromUserId = %v", m["fromUserId"])
		}
		if m["event"] != "chat" {
			t.Errorf("event = %v", m["event"])
		}
		if m["recipientDescriptor"] != "ALL_USERS" {
			t.Errorf("recipientDescriptor = %v", m["recipientDescriptor"])
		}
		decoded, ok := eventFromDistributedBytes(data)
		if !ok || decoded.Action != broadcast || decoded.Event != "chat" || decoded.FromUserID != "user-1" {
			t.Errorf("decode mismatch: %+v", decoded)
		}
	})

	t.Run("PRESENCE_UPDATE", func(t *testing.T) {
		ev := Event{
			Action:      presence,
			ChannelName: channel,
			RequestId:   "req-presence-1",
			Event:       string(join),
			NodeID:      node,
			Payload:     presencePayload{Event: join, UserID: "user-1", Change: map[string]interface{}{"status": "online"}},
		}
		data, err := distributedBytesFromEvent(endpoint, ev, "CHANNEL", "ALL_USERS")
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgPresenceUpdate)
		mustHave(t, m, "userId", "presence")
		mustNotHave(t, m, "event", "requestId")
		if m["userId"] != "user-1" {
			t.Errorf("userId = %v", m["userId"])
		}
		presenceMap, _ := m["presence"].(map[string]interface{})
		if presenceMap["status"] != "online" {
			t.Errorf("presence = %v", m["presence"])
		}
		decoded, ok := eventFromDistributedBytes(data)
		if !ok || decoded.Action != presence {
			t.Errorf("decode mismatch: %+v", decoded)
		}
	})

	t.Run("PRESENCE_REMOVED", func(t *testing.T) {
		ev := Event{
			Action:      presence,
			ChannelName: channel,
			RequestId:   "req-presence-2",
			Event:       string(leave),
			NodeID:      node,
			Payload:     presencePayload{Event: leave, UserID: "user-1"},
		}
		data, err := distributedBytesFromEvent(endpoint, ev, "CHANNEL", "ALL_USERS")
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgPresenceRemoved)
		mustHave(t, m, "userId")
		mustNotHave(t, m, "event", "presence", "requestId")
		if m["userId"] != "user-1" {
			t.Errorf("userId = %v", m["userId"])
		}
	})

	t.Run("ASSIGNS_UPDATE", func(t *testing.T) {
		ev := Event{
			Action:      assigns,
			ChannelName: channel,
			RequestId:   "req-assigns-1",
			Event:       "assigns:update",
			NodeID:      node,
			Payload: map[string]interface{}{
				"userId":  "user-1",
				"key":     "role",
				"value":   "admin",
				"assigns": map[string]interface{}{"role": "admin"},
			},
		}
		data, err := distributedBytesFromEvent(endpoint, ev, "CHANNEL", "ALL_USERS")
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgAssignsUpdate)
		mustHave(t, m, "userId", "assigns")
		mustNotHave(t, m, "payload", "requestId", "key", "value")
		if m["userId"] != "user-1" {
			t.Errorf("userId = %v", m["userId"])
		}
		topAssigns, _ := m["assigns"].(map[string]interface{})
		if topAssigns["role"] != "admin" {
			t.Errorf("top-level assigns = %v", m["assigns"])
		}
		decoded, ok := eventFromDistributedBytes(data)
		if !ok || decoded.Action != assigns {
			t.Errorf("decode mismatch: %+v", decoded)
		}
		decodedPayload, _ := decoded.Payload.(map[string]interface{})
		decodedAssigns, _ := decodedPayload["assigns"].(map[string]interface{})
		if decodedAssigns["role"] != "admin" {
			t.Errorf("decoded assigns = %v", decodedPayload["assigns"])
		}
	})

	t.Run("EVICT_USER", func(t *testing.T) {
		ev := Event{
			Action:      userCommand,
			ChannelName: channel,
			RequestId:   "req-evict-1",
			Event:       string(userEvictCommand),
			NodeID:      node,
			Payload:     map[string]interface{}{"userID": "user-1", "reason": "spam"},
		}
		data, err := distributedBytesFromEvent(endpoint, ev, "CHANNEL", "ALL_USERS")
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgEvictUser)
		mustHave(t, m, "userId", "reason")
		mustNotHave(t, m, "requestId")
		if m["userId"] != "user-1" || m["reason"] != "spam" {
			t.Errorf("evict wire = %v", m)
		}
		decoded, ok := eventFromDistributedBytes(data)
		if !ok || decoded.Event != string(userEvictCommand) {
			t.Errorf("decode mismatch: %+v", decoded)
		}
	})

	t.Run("USER_REMOVE", func(t *testing.T) {
		ev := Event{
			Action:      userCommand,
			ChannelName: channel,
			RequestId:   "req-remove-1",
			Event:       string(userRemoveCommand),
			NodeID:      node,
			Payload:     map[string]interface{}{"userID": "user-1", "reason": "gone"},
		}
		data, err := distributedBytesFromEvent(endpoint, ev, "CHANNEL", "ALL_USERS")
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgUserRemove)
		mustHave(t, m, "userId")
		mustNotHave(t, m, "reason", "requestId")
		if m["userId"] != "user-1" {
			t.Errorf("userId = %v", m["userId"])
		}
		decoded, ok := eventFromDistributedBytes(data)
		if !ok || decoded.Event != string(userRemoveCommand) {
			t.Errorf("decode mismatch: %+v", decoded)
		}
	})

	t.Run("USER_GET_REQUEST", func(t *testing.T) {
		ev := Event{
			Action:      userCommand,
			ChannelName: channel,
			RequestId:   "lookup-1",
			Event:       string(userGetRequest),
			NodeID:      node,
			Payload:     map[string]interface{}{"userID": "user-1", "requestID": "lookup-1"},
		}
		data, err := distributedBytesFromEvent(endpoint, ev, "CHANNEL", "ALL_USERS")
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgUserGetRequest)
		mustHave(t, m, "userId", "requestId", "fromNode")
		mustNotHave(t, m, "payload")
		if m["userId"] != "user-1" {
			t.Errorf("userId = %v", m["userId"])
		}
		if m["requestId"] != "lookup-1" {
			t.Errorf("requestId = %v", m["requestId"])
		}
		if m["fromNode"] != node {
			t.Errorf("fromNode = %v, want %s", m["fromNode"], node)
		}
		decoded, ok := eventFromDistributedBytes(data)
		if !ok || decoded.Event != string(userGetRequest) {
			t.Errorf("decode mismatch: %+v", decoded)
		}
		decodedPayload, _ := decoded.Payload.(map[string]interface{})
		if decodedPayload["userID"] != "user-1" || decodedPayload["requestID"] != "lookup-1" {
			t.Errorf("decoded payload = %v", decoded.Payload)
		}
	})

	t.Run("USER_GET_RESPONSE", func(t *testing.T) {
		ev := Event{
			Action:      userCommand,
			ChannelName: channel,
			RequestId:   "lookup-1",
			Event:       string(userGetResponse),
			NodeID:      node,
			Payload: map[string]interface{}{
				"requestID": "lookup-1",
				"userID":    "user-1",
				"assigns":   map[string]interface{}{"role": "admin"},
				"presence":  map[string]interface{}{"status": "online"},
				"found":     true,
			},
		}
		data, err := distributedBytesFromEvent(endpoint, ev, "CHANNEL", "ALL_USERS")
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgUserGetResponse)
		mustHave(t, m, "userId", "requestId", "assigns", "presence")
		mustNotHave(t, m, "payload", "found")
		if m["userId"] != "user-1" {
			t.Errorf("userId = %v", m["userId"])
		}
		if m["requestId"] != "lookup-1" {
			t.Errorf("requestId = %v", m["requestId"])
		}
		respAssigns, _ := m["assigns"].(map[string]interface{})
		if respAssigns["role"] != "admin" {
			t.Errorf("assigns = %v", m["assigns"])
		}
		respPresence, _ := m["presence"].(map[string]interface{})
		if respPresence["status"] != "online" {
			t.Errorf("presence = %v", m["presence"])
		}
		decoded, ok := eventFromDistributedBytes(data)
		if !ok || decoded.Event != string(userGetResponse) {
			t.Errorf("decode mismatch: %+v", decoded)
		}
		decodedPayload, _ := decoded.Payload.(map[string]interface{})
		if decodedPayload["requestID"] != "lookup-1" || decodedPayload["userID"] != "user-1" || decodedPayload["found"] != true {
			t.Errorf("decoded payload = %v", decoded.Payload)
		}
	})

	t.Run("USER_JOINED", func(t *testing.T) {
		data, err := encodeUserJoined(endpoint, channel, node, "user-1",
			map[string]interface{}{"status": "online"},
			map[string]interface{}{"role": "member"})
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgUserJoined)
		mustHave(t, m, "userId", "presence", "assigns")
		if m["userId"] != "user-1" {
			t.Errorf("userId = %v", m["userId"])
		}
		env, ok := decodeEnvelope(data)
		if !ok || env.UserID != "user-1" {
			t.Errorf("decode mismatch: %+v", env)
		}
		a, _ := env.Assigns.(map[string]interface{})
		if a["role"] != "member" {
			t.Errorf("assigns = %v", env.Assigns)
		}
	})

	t.Run("USER_LEFT", func(t *testing.T) {
		data, err := encodeUserLeft(endpoint, channel, node, "user-1")
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgUserLeft)
		mustHave(t, m, "userId")
		mustNotHave(t, m, "presence", "assigns")
		env, ok := decodeEnvelope(data)
		if !ok || env.UserID != "user-1" {
			t.Errorf("decode mismatch: %+v", env)
		}
	})

	t.Run("STATE_REQUEST", func(t *testing.T) {
		data, err := encodeStateRequest(endpoint, channel, node)
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgStateRequest)
		mustHave(t, m, "fromNode")
		if m["fromNode"] != node {
			t.Errorf("fromNode = %v, want %s", m["fromNode"], node)
		}
		env, ok := decodeEnvelope(data)
		if !ok || env.FromNode != node {
			t.Errorf("decode mismatch: %+v", env)
		}
	})

	t.Run("STATE_RESPONSE", func(t *testing.T) {
		users := []distributedUser{
			{ID: "user-1", Assigns: map[string]interface{}{"role": "admin"}, Presence: map[string]interface{}{"status": "online"}},
			{ID: "user-2", Assigns: map[string]interface{}{}, Presence: map[string]interface{}{}},
		}
		data, err := encodeStateResponse(endpoint, channel, node, users)
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgStateResponse)
		mustHave(t, m, "users")
		arr, _ := m["users"].([]interface{})
		if len(arr) != 2 {
			t.Fatalf("expected 2 users, got %d", len(arr))
		}
		first, _ := arr[0].(map[string]interface{})
		mustHave(t, first, "id", "assigns", "presence")
		mustNotHave(t, first, "userId")
		if first["id"] != "user-1" {
			t.Errorf("users[0].id = %v", first["id"])
		}
		env, ok := decodeEnvelope(data)
		if !ok || len(env.Users) != 2 || env.Users[0].ID != "user-1" {
			t.Errorf("decode mismatch: %+v", env)
		}
	})

	t.Run("NODE_HEARTBEAT", func(t *testing.T) {
		data, err := encodeHeartbeat(endpoint, channel, node)
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		assertEnvelope(t, m, msgNodeHeartbeat)
		mustHave(t, m, "nodeId")
		if m["nodeId"] != node {
			t.Errorf("nodeId = %v, want %s", m["nodeId"], node)
		}
		env, ok := decodeEnvelope(data)
		if !ok || env.NodeID != node {
			t.Errorf("decode mismatch: %+v", env)
		}
	})
}

func TestNamespacePublishTopic(t *testing.T) {
	ctx := context.Background()

	var published []PubSubMessage
	var mu sync.Mutex
	wrapper := &pubsubTestWrapper{
		PubSub: NewLocalPubSub(ctx, 100),
		onPublish: func(topic string, data []byte) {
			mu.Lock()
			published = append(published, PubSubMessage{Topic: topic, Data: data})
			mu.Unlock()
		},
	}

	channelOpts := options{
		Name:                 "test:channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
		PubSub:               wrapper,
		Namespace:            "tenant-x",
	}

	channel := newChannel(ctx, channelOpts)
	channel.endpointPath = "/socket"
	channel.subscribeToPubSub()
	defer channel.Close()

	conn1 := createTestConn("user1", nil)
	if err := channel.addUser(conn1); err != nil {
		t.Fatalf("failed to add user1: %v", err)
	}

	if err := channel.Broadcast("test:event", map[string]interface{}{"message": "hi"}); err != nil {
		t.Fatalf("failed to broadcast: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	messages := make([]PubSubMessage, len(published))
	copy(messages, published)
	mu.Unlock()

	wantTopic := "pondsocket:v1:tenant-x:socket:test:channel"
	defaultTopic := "pondsocket:v1:default:socket:test:channel"
	found := false
	for _, msg := range messages {
		if msg.Topic == defaultTopic {
			t.Errorf("message published to default-namespace topic %s, expected namespaced topic", defaultTopic)
		}
		if msg.Topic != wantTopic {
			continue
		}
		if event, ok := eventFromDistributedBytes(msg.Data); ok && event.Event == "test:event" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected broadcast on namespaced topic %s; got topics %v", wantTopic, topicsOf(messages))
	}
}

func topicsOf(messages []PubSubMessage) []string {
	topics := make([]string, len(messages))
	for i, m := range messages {
		topics[i] = m.Topic
	}
	return topics
}

func TestRecipientDescriptorForms(t *testing.T) {
	const endpoint = "socket"
	const channel = "room:1"

	t.Run("ALL_USERS", func(t *testing.T) {
		ev := Event{Action: broadcast, ChannelName: channel, Event: "e", NodeID: "n"}
		data, err := distributedBytesFromEvent(endpoint, ev, "s", "ALL_USERS")
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		if m["recipientDescriptor"] != "ALL_USERS" {
			t.Errorf("recipientDescriptor = %v", m["recipientDescriptor"])
		}
		decoded, _ := eventFromDistributedBytes(data)
		if decoded.Recipient != "ALL_USERS" {
			t.Errorf("decoded recipient = %v", decoded.Recipient)
		}
	})

	t.Run("ALL_EXCEPT_SENDER", func(t *testing.T) {
		ev := Event{Action: broadcast, ChannelName: channel, Event: "e", NodeID: "n"}
		data, err := distributedBytesFromEvent(endpoint, ev, "sender-1", "ALL_EXCEPT_SENDER")
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		if m["recipientDescriptor"] != "ALL_EXCEPT_SENDER" {
			t.Errorf("recipientDescriptor = %v", m["recipientDescriptor"])
		}
		if m["fromUserId"] != "sender-1" {
			t.Errorf("fromUserId = %v", m["fromUserId"])
		}
		decoded, _ := eventFromDistributedBytes(data)
		if decoded.Recipient != "ALL_EXCEPT_SENDER" {
			t.Errorf("decoded recipient = %v", decoded.Recipient)
		}
	})

	t.Run("explicit id list", func(t *testing.T) {
		ids := []string{"a", "b", "c"}
		ev := Event{Action: broadcast, ChannelName: channel, Event: "e", NodeID: "n", Recipients: ids}
		data, err := distributedBytesFromEvent(endpoint, ev, "s", nil)
		if err != nil {
			t.Fatal(err)
		}
		m := decodeWire(t, data)
		arr, ok := m["recipientDescriptor"].([]interface{})
		if !ok || len(arr) != 3 {
			t.Fatalf("recipientDescriptor = %v", m["recipientDescriptor"])
		}
		decoded, _ := eventFromDistributedBytes(data)
		if !reflect.DeepEqual(decoded.Recipients, ids) {
			t.Errorf("decoded recipients = %v, want %v", decoded.Recipients, ids)
		}
	})
}
