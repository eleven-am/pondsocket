package pondsocket

import (
	"context"
	"testing"
	"time"
)

func TestTwoNodeStateSync(t *testing.T) {
	t.Run("late-joining node syncs existing users via STATE_REQUEST/STATE_RESPONSE", func(t *testing.T) {
		ctx := context.Background()
		sharedPubSub := NewLocalPubSub(ctx, 100)

		channelOptsA := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               sharedPubSub,
		}
		channelA := newChannel(ctx, channelOptsA)
		channelA.endpointPath = "/socket"
		channelA.subscribeToPubSub()
		defer channelA.Close()

		connA := createTestConn("user_A", map[string]interface{}{"role": "admin"})
		if err := channelA.addUser(connA); err != nil {
			t.Fatalf("Failed to add user to channel A: %v", err)
		}
		if err := channelA.Track("user_A", map[string]interface{}{"status": "online"}); err != nil {
			t.Fatalf("Failed to track user on channel A: %v", err)
		}

		time.Sleep(200 * time.Millisecond)

		channelOptsB := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               sharedPubSub,
		}
		channelB := newChannel(ctx, channelOptsB)
		channelB.endpointPath = "/socket"
		channelB.subscribeToPubSub()
		defer channelB.Close()

		time.Sleep(400 * time.Millisecond)

		presenceB := channelB.GetPresence()
		userPresence, hasUser := presenceB["user_A"]
		if !hasUser {
			t.Fatalf("Channel B should have synced user_A presence via STATE_RESPONSE, got %+v", presenceB)
		}
		presenceMap, _ := userPresence.(map[string]interface{})
		if presenceMap["status"] != "online" {
			t.Errorf("user_A presence status = %v, want online", presenceMap["status"])
		}

		assignsB := channelB.GetAssigns()
		userAssigns, hasAssigns := assignsB["user_A"]
		if !hasAssigns {
			t.Fatalf("Channel B should have synced user_A assigns via STATE_RESPONSE, got %+v", assignsB)
		}
		if userAssigns["role"] != "admin" {
			t.Errorf("user_A assigns role = %v, want admin", userAssigns["role"])
		}
	})
}

func TestTwoNodeUserJoinedLeft(t *testing.T) {
	t.Run("USER_JOINED and USER_LEFT propagate remote membership", func(t *testing.T) {
		ctx := context.Background()
		sharedPubSub := NewLocalPubSub(ctx, 100)

		channelOptsA := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               sharedPubSub,
		}
		channelA := newChannel(ctx, channelOptsA)
		channelA.endpointPath = "/socket"
		channelA.subscribeToPubSub()
		defer channelA.Close()

		channelOptsB := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               sharedPubSub,
		}
		channelB := newChannel(ctx, channelOptsB)
		channelB.endpointPath = "/socket"
		channelB.subscribeToPubSub()
		defer channelB.Close()

		time.Sleep(200 * time.Millisecond)

		connA := createTestConn("user_on_A", map[string]interface{}{"role": "member"})
		if err := channelA.addUser(connA); err != nil {
			t.Fatalf("Failed to add user to channel A: %v", err)
		}

		time.Sleep(200 * time.Millisecond)

		if _, ok := channelB.GetAssigns()["user_on_A"]; !ok {
			t.Fatal("Channel B should track user_on_A after USER_JOINED")
		}
		channelB.distMutex.RLock()
		_, tracked := channelB.nodeUsers[channelA.nodeID]["user_on_A"]
		channelB.distMutex.RUnlock()
		if !tracked {
			t.Error("Channel B should have node->user tracking for user_on_A")
		}

		if err := channelA.RemoveUser("user_on_A", "leaving"); err != nil {
			t.Fatalf("Failed to remove user from channel A: %v", err)
		}

		time.Sleep(200 * time.Millisecond)

		if _, ok := channelB.GetAssigns()["user_on_A"]; ok {
			t.Error("Channel B should drop user_on_A after USER_LEFT")
		}
	})
}

func TestNamespaceConfigurable(t *testing.T) {
	t.Run("default namespace", func(t *testing.T) {
		ctx := context.Background()
		c := newChannel(ctx, options{
			Name:                 "room:1",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		})
		defer c.Close()
		c.endpointPath = "/socket"
		if got := c.channelTopic(); got != "pondsocket:v1:default:socket:room:1" {
			t.Errorf("channelTopic = %s", got)
		}
		if got := c.heartbeatTopic(); got != "pondsocket:v1:default:__heartbeat__" {
			t.Errorf("heartbeatTopic = %s", got)
		}
	})

	t.Run("custom namespace", func(t *testing.T) {
		ctx := context.Background()
		c := newChannel(ctx, options{
			Name:                 "room:1",
			Namespace:            "tenantX",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		})
		defer c.Close()
		c.endpointPath = "/socket"
		if got := c.channelTopic(); got != "pondsocket:v1:tenantX:socket:room:1" {
			t.Errorf("channelTopic = %s", got)
		}
		if got := c.heartbeatTopic(); got != "pondsocket:v1:tenantX:__heartbeat__" {
			t.Errorf("heartbeatTopic = %s", got)
		}
	})
}

func TestHeartbeatStaleNodeCleanup(t *testing.T) {
	t.Run("evicts users of a node that stops heartbeating", func(t *testing.T) {
		ctx := context.Background()
		sharedPubSub := NewLocalPubSub(ctx, 100)

		channelOpts := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               sharedPubSub,
		}
		channel := newChannel(ctx, channelOpts)
		channel.endpointPath = "/socket"
		channel.heartbeatTimeout = 250 * time.Millisecond
		channel.subscribeToPubSub()
		defer channel.Close()

		time.Sleep(100 * time.Millisecond)

		ghostData, err := encodeUserJoined("socket", "test:channel", "ghost-node", "ghost_user",
			map[string]interface{}{"status": "online"},
			map[string]interface{}{"team": "red"})
		if err != nil {
			t.Fatalf("encode ghost join: %v", err)
		}
		if err := sharedPubSub.Publish(channel.channelTopic(), ghostData); err != nil {
			t.Fatalf("publish ghost join: %v", err)
		}

		deadline := time.Now().Add(1 * time.Second)
		for time.Now().Before(deadline) {
			if _, ok := channel.GetPresence()["ghost_user"]; ok {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if _, ok := channel.GetPresence()["ghost_user"]; !ok {
			t.Fatal("ghost_user should be present before heartbeat timeout")
		}
		if _, ok := channel.GetAssigns()["ghost_user"]; !ok {
			t.Fatal("ghost_user assigns should be present before heartbeat timeout")
		}

		evicted := false
		deadline = time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			_, presenceHas := channel.GetPresence()["ghost_user"]
			_, assignsHas := channel.GetAssigns()["ghost_user"]
			if !presenceHas && !assignsHas {
				evicted = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !evicted {
			t.Error("ghost_user should have been evicted after the node stopped heartbeating")
		}
	})
}
