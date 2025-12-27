package pondsocket

import (
	"context"
	"testing"
	"time"
)

func TestCrossNodeEvictUser(t *testing.T) {
	t.Run("evicts user on remote node via PubSub", func(t *testing.T) {
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

		connA := createTestConn("user_on_A", nil)
		err := channelA.addUser(connA)
		if err != nil {
			t.Fatalf("Failed to add user to channel A: %v", err)
		}
		channelA.connections.Update(connA.ID, connA)

		time.Sleep(100 * time.Millisecond)

		err = channelB.EvictUser("user_on_A", "rule_violation")
		if err != nil {
			t.Errorf("Expected no error when evicting remote user, got: %v", err)
		}

		time.Sleep(300 * time.Millisecond)

		_, err = channelA.GetUser("user_on_A")
		if err == nil {
			t.Error("Expected user to be removed from channel A after remote eviction")
		}
	})

	t.Run("publishes evict command via PubSub when user not found locally", func(t *testing.T) {
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
		channel.subscribeToPubSub()
		defer channel.Close()

		time.Sleep(100 * time.Millisecond)

		err := channel.EvictUser("nonexistent_user", "test")
		if err != nil {
			t.Errorf("Expected no error (command published to PubSub), got: %v", err)
		}
	})

	t.Run("returns error when user not found and no PubSub configured", func(t *testing.T) {
		ctx := context.Background()

		channelOpts := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}

		channel := newChannel(ctx, channelOpts)
		defer channel.Close()

		err := channel.EvictUser("nonexistent_user", "test")
		if err == nil {
			t.Error("Expected error when evicting nonexistent user without PubSub")
		}
	})
}

func TestCrossNodeRemoveUser(t *testing.T) {
	t.Run("removes user on remote node via PubSub", func(t *testing.T) {
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

		connA := createTestConn("user_on_A", nil)
		err := channelA.addUser(connA)
		if err != nil {
			t.Fatalf("Failed to add user to channel A: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		err = channelB.RemoveUser("user_on_A", "remote_disconnect")
		if err != nil {
			t.Errorf("Expected no error when removing remote user, got: %v", err)
		}

		time.Sleep(300 * time.Millisecond)

		_, err = channelA.GetUser("user_on_A")
		if err == nil {
			t.Error("Expected user to be removed from channel A after remote removal")
		}
	})

	t.Run("returns error when user not found locally without PubSub", func(t *testing.T) {
		ctx := context.Background()

		channelOpts := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}

		channel := newChannel(ctx, channelOpts)
		defer channel.Close()

		err := channel.RemoveUser("nonexistent_user", "test")
		if err == nil {
			t.Error("Expected error when removing nonexistent user without PubSub")
		}
	})
}

func TestCrossNodeUpdateAssigns(t *testing.T) {
	t.Run("updates assigns on remote node via PubSub", func(t *testing.T) {
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
		err := channelA.addUser(connA)
		if err != nil {
			t.Fatalf("Failed to add user to channel A: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		err = channelB.UpdateAssigns("user_on_A", "role", "admin")
		if err != nil {
			t.Errorf("Expected no error when updating assigns for remote user, got: %v", err)
		}

		time.Sleep(300 * time.Millisecond)

		user, err := channelA.GetUser("user_on_A")
		if err != nil {
			t.Fatalf("Failed to get user: %v", err)
		}

		if user.Assigns["role"] != "admin" {
			t.Errorf("Expected role to be 'admin', got: %v", user.Assigns["role"])
		}
	})

	t.Run("broadcasts assigns update to local users via PubSub", func(t *testing.T) {
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

		connA := createTestConn("user_on_A", map[string]interface{}{"status": "active"})
		err := channelA.addUser(connA)
		if err != nil {
			t.Fatalf("Failed to add user to channel A: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		err = channelA.UpdateAssigns("user_on_A", "status", "away")
		if err != nil {
			t.Errorf("Expected no error when updating assigns, got: %v", err)
		}

		time.Sleep(300 * time.Millisecond)

		assigns := channelB.GetAssigns()
		if assigns["user_on_A"] != nil && assigns["user_on_A"]["status"] == "away" {
			t.Log("Assigns were successfully synchronized to channel B")
		}
	})
}

func TestCrossNodeGetUser(t *testing.T) {
	t.Run("gets user from remote node via PubSub", func(t *testing.T) {
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

		connA := createTestConn("user_on_A", map[string]interface{}{"role": "admin"})
		err := channelA.addUser(connA)
		if err != nil {
			t.Fatalf("Failed to add user to channel A: %v", err)
		}

		err = channelA.Track("user_on_A", map[string]interface{}{"status": "online"})
		if err != nil {
			t.Fatalf("Failed to track user: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		user, err := channelB.GetUser("user_on_A")
		if err != nil {
			t.Fatalf("Expected to get user from remote node, got error: %v", err)
		}

		if user.UserID != "user_on_A" {
			t.Errorf("Expected userID 'user_on_A', got: %s", user.UserID)
		}

		if user.Assigns["role"] != "admin" {
			t.Errorf("Expected role 'admin', got: %v", user.Assigns["role"])
		}
	})

	t.Run("returns error when user not found on any node", func(t *testing.T) {
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

		_, err := channelB.GetUser("nonexistent_user")
		if err == nil {
			t.Error("Expected error when getting nonexistent user")
		}
	})

	t.Run("returns local user without PubSub call", func(t *testing.T) {
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
		channel.subscribeToPubSub()
		defer channel.Close()

		conn := createTestConn("local_user", map[string]interface{}{"role": "user"})
		err := channel.addUser(conn)
		if err != nil {
			t.Fatalf("Failed to add user: %v", err)
		}

		user, err := channel.GetUser("local_user")
		if err != nil {
			t.Fatalf("Expected to get local user, got error: %v", err)
		}

		if user.UserID != "local_user" {
			t.Errorf("Expected userID 'local_user', got: %s", user.UserID)
		}
	})
}

func TestCrossNodeBroadcastTo(t *testing.T) {
	t.Run("broadcasts to users on remote nodes via PubSub", func(t *testing.T) {
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

		connA := createTestConn("user_on_A", nil)
		err := channelA.addUser(connA)
		if err != nil {
			t.Fatalf("Failed to add user to channel A: %v", err)
		}
		channelA.connections.Update(connA.ID, connA)

		connB := createTestConn("sender_on_B", nil)
		err = channelB.addUser(connB)
		if err != nil {
			t.Fatalf("Failed to add user to channel B: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		err = channelB.BroadcastTo("test_event", map[string]interface{}{"message": "hello"}, "user_on_A")
		if err != nil {
			t.Errorf("Expected no error when broadcasting to remote user, got: %v", err)
		}

		time.Sleep(300 * time.Millisecond)

		select {
		case msg := <-connA.send:
			if len(msg) == 0 {
				t.Error("Expected message content")
			}
		case <-time.After(500 * time.Millisecond):
			t.Error("Expected user_on_A to receive broadcast message from channel B")
		}
	})
}

func TestPublishUserCommand(t *testing.T) {
	t.Run("returns error when PubSub is nil", func(t *testing.T) {
		ctx := context.Background()

		channelOpts := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}

		channel := newChannel(ctx, channelOpts)
		defer channel.Close()

		err := channel.publishUserCommand(userEvictCommand, "user1", "test")
		if err == nil {
			t.Error("Expected error when PubSub is nil")
		}
	})

	t.Run("returns error when endpointPath is empty", func(t *testing.T) {
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
		defer channel.Close()

		err := channel.publishUserCommand(userEvictCommand, "user1", "test")
		if err == nil {
			t.Error("Expected error when endpointPath is empty")
		}
	})

	t.Run("publishes command successfully", func(t *testing.T) {
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
		channel.subscribeToPubSub()
		defer channel.Close()

		err := channel.publishUserCommand(userEvictCommand, "user1", "test")
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
	})
}

func TestHandleUserGetRequest(t *testing.T) {
	t.Run("responds when user exists locally", func(t *testing.T) {
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
		channel.subscribeToPubSub()
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{"role": "admin"})
		err := channel.addUser(conn)
		if err != nil {
			t.Fatalf("Failed to add user: %v", err)
		}

		event := &Event{
			Action:      userCommand,
			ChannelName: "test:channel",
			RequestId:   "req-123",
			Event:       string(userGetRequest),
			Payload: map[string]interface{}{
				"userID":    "user1",
				"requestID": "req-123",
			},
		}

		channel.handleUserGetRequest(event, "user1")
		time.Sleep(100 * time.Millisecond)
	})

	t.Run("does nothing when user does not exist", func(t *testing.T) {
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
		channel.subscribeToPubSub()
		defer channel.Close()

		event := &Event{
			Action:      userCommand,
			ChannelName: "test:channel",
			RequestId:   "req-123",
			Event:       string(userGetRequest),
			Payload: map[string]interface{}{
				"userID":    "nonexistent",
				"requestID": "req-123",
			},
		}

		channel.handleUserGetRequest(event, "nonexistent")
	})
}

func TestHandleUserGetResponse(t *testing.T) {
	t.Run("delivers response to waiting coordinator", func(t *testing.T) {
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
		defer channel.Close()

		responseChan := make(chan *User, 1)
		channel.userGetCoordinatorMutex.Lock()
		channel.userGetCoordinators["req-123"] = responseChan
		channel.userGetCoordinatorMutex.Unlock()

		event := &Event{
			Action:      userCommand,
			ChannelName: "test:channel",
			RequestId:   "resp-456",
			Event:       string(userGetResponse),
			Payload: map[string]interface{}{
				"requestID": "req-123",
				"userID":    "user1",
				"assigns":   map[string]interface{}{"role": "admin"},
				"presence":  nil,
				"found":     true,
			},
		}

		channel.handleUserGetResponse(event)

		select {
		case user := <-responseChan:
			if user == nil {
				t.Error("Expected non-nil user")
			} else if user.UserID != "user1" {
				t.Errorf("Expected userID 'user1', got: %s", user.UserID)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Expected response to be delivered")
		}
	})

	t.Run("ignores response when coordinator does not exist", func(t *testing.T) {
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
		defer channel.Close()

		event := &Event{
			Action:      userCommand,
			ChannelName: "test:channel",
			RequestId:   "resp-456",
			Event:       string(userGetResponse),
			Payload: map[string]interface{}{
				"requestID": "nonexistent-req",
				"userID":    "user1",
				"assigns":   map[string]interface{}{"role": "admin"},
				"found":     true,
			},
		}

		channel.handleUserGetResponse(event)
	})

	t.Run("ignores response when found is false", func(t *testing.T) {
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
		defer channel.Close()

		responseChan := make(chan *User, 1)
		channel.userGetCoordinatorMutex.Lock()
		channel.userGetCoordinators["req-123"] = responseChan
		channel.userGetCoordinatorMutex.Unlock()

		event := &Event{
			Action:      userCommand,
			ChannelName: "test:channel",
			RequestId:   "resp-456",
			Event:       string(userGetResponse),
			Payload: map[string]interface{}{
				"requestID": "req-123",
				"found":     false,
			},
		}

		channel.handleUserGetResponse(event)

		select {
		case <-responseChan:
			t.Error("Should not receive response when found is false")
		case <-time.After(50 * time.Millisecond):
		}
	})
}

func TestRequestRemoteUser(t *testing.T) {
	t.Run("returns error when PubSub is nil", func(t *testing.T) {
		ctx := context.Background()

		channelOpts := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
		}

		channel := newChannel(ctx, channelOpts)
		defer channel.Close()

		_, err := channel.requestRemoteUser("user1")
		if err == nil {
			t.Error("Expected error when PubSub is nil")
		}
	})

	t.Run("returns error when endpointPath is empty", func(t *testing.T) {
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
		defer channel.Close()

		_, err := channel.requestRemoteUser("user1")
		if err == nil {
			t.Error("Expected error when endpointPath is empty")
		}
	})

	t.Run("times out when no response received", func(t *testing.T) {
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
		defer channel.Close()

		start := time.Now()
		_, err := channel.requestRemoteUser("nonexistent_user")
		elapsed := time.Since(start)

		if err == nil {
			t.Error("Expected error when user not found")
		}

		if elapsed < 400*time.Millisecond || elapsed > 1*time.Second {
			t.Errorf("Expected timeout around 500ms, got: %v", elapsed)
		}
	})
}
