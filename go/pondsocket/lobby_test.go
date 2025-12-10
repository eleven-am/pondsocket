package pondsocket

import (
	"context"
	"sync"
	"testing"
	"time"
)

func createTestEndpoint(ctx context.Context) *Endpoint {
	return &Endpoint{
		connections: newStore[Transport](),
		middleware:  newMiddleWare[joinEvent, interface{}](),
		channels:    newStore[*Channel](),
		options:     DefaultOptions(),
		ctx:         ctx,
	}
}

func TestLobbyCreation(t *testing.T) {
	ctx := context.Background()

	endpoint := createTestEndpoint(ctx)

	lobby := newLobby(endpoint)

	if lobby.endpoint != endpoint {
		t.Error("expected lobby to reference the endpoint")
	}
	if lobby.middleware == nil {
		t.Error("expected middleware to be initialized")
	}
	if lobby.outgoing == nil {
		t.Error("expected outgoing middleware to be initialized")
	}
	if lobby.channels == nil {
		t.Error("expected channels store to be initialized")
	}
}

func TestLobbyOnLeave(t *testing.T) {
	ctx := context.Background()

	endpoint := createTestEndpoint(ctx)

	lobby := newLobby(endpoint)

	leaveCalled := false
	var leaveUser User
	lobby.OnLeave(func(ctx *LeaveContext) {
		leaveCalled = true
		leaveUser = *ctx.GetUser()
	})

	if lobby.leaveHandler == nil {
		t.Error("expected leave handler to be set")
	}
	testUser := User{UserID: "test", Assigns: map[string]interface{}{"role": "member"}}
	testChannel := &Channel{name: "test-channel"}
	leaveCtx := newLeaveContext(ctx, testChannel, &testUser, "test")
	(*lobby.leaveHandler)(leaveCtx)

	if !leaveCalled {
		t.Error("expected leave handler to be called")
	}
	if leaveUser.UserID != "test" {
		t.Errorf("expected user ID test, got %s", leaveUser.UserID)
	}
}

func TestLobbyOnMessage(t *testing.T) {
	ctx := context.Background()

	endpoint := createTestEndpoint(ctx)

	lobby := newLobby(endpoint)

	messageCalled := false
	lobby.OnMessage("test:*", func(ctx *EventContext) error {
		messageCalled = true
		return nil
	})

	testCtx, cancel := context.WithTimeout(ctx, 1*time.Second)

	defer cancel()

	event := &Event{
		Event: "test:message",
	}
	msgEvent := &messageEvent{
		Event: event,
		User:  &User{UserID: "test"},
	}
	err := lobby.middleware.Handle(testCtx, msgEvent, &Channel{}, func(req *messageEvent, res *Channel) error {
		return nil
	})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !messageCalled {
		t.Error("expected message handler to be called")
	}
}

func TestLobbyOnOutgoing(t *testing.T) {
	ctx := context.Background()

	endpoint := createTestEndpoint(ctx)

	lobby := newLobby(endpoint)

	outgoingCalled := false
	lobby.OnOutgoing("broadcast:*", func(ctx *OutgoingContext) error {
		outgoingCalled = true
		return nil
	})

	testCtx, cancel := context.WithTimeout(ctx, 1*time.Second)

	defer cancel()

	event := &Event{
		Event: "broadcast:message",
	}
	mockChannel := &Channel{name: "test"}
	mockUser := &User{UserID: "test"}
	mockConn := &Conn{ID: "test"}
	outgoingCtx := newOutgoingContext(testCtx, mockChannel, event, mockUser, mockConn)

	err := lobby.outgoing.Handle(testCtx, outgoingCtx, nil, func(req *OutgoingContext, res interface{}) error {
		return nil
	})

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if !outgoingCalled {
		t.Error("expected outgoing handler to be called")
	}
}

func TestLobbyCreateChannel(t *testing.T) {
	ctx := context.Background()

	endpoint := createTestEndpoint(ctx)

	lobby := newLobby(endpoint)

	t.Run("creates channel successfully", func(t *testing.T) {
		channel, err := lobby.createChannel("test-channel")

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if channel.name != "test-channel" {
			t.Errorf("expected channel name test-channel, got %s", channel.name)
		}
		storedChannel, err := lobby.channels.Read("test-channel")

		if err != nil {
			t.Errorf("expected channel to be stored, got error: %v", err)
		}
		if storedChannel != channel {
			t.Error("expected stored channel to match created channel")
		}
		endpointChannel, err := endpoint.channels.Read("test-channel")

		if err != nil {
			t.Errorf("expected channel to be stored in endpoint, got error: %v", err)
		}
		if endpointChannel != channel {
			t.Error("expected endpoint channel to match created channel")
		}
	})

	t.Run("returns existing channel if already created", func(t *testing.T) {
		channel1, err := lobby.createChannel("duplicate-channel")

		if err != nil {
			t.Fatalf("expected no error on first create, got %v", err)
		}
		channel2, err := lobby.createChannel("duplicate-channel")

		if err != nil {
			t.Fatalf("expected no error on second create, got %v", err)
		}
		if channel1 != channel2 {
			t.Error("expected same channel instance to be returned")
		}
	})
}

func TestLobbyGetOrCreateChannel(t *testing.T) {
	ctx := context.Background()

	endpoint := createTestEndpoint(ctx)

	lobby := newLobby(endpoint)

	t.Run("creates new channel when doesn't exist", func(t *testing.T) {
		channel, err := lobby.getOrCreateChannel("new-channel")

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if channel.name != "new-channel" {
			t.Errorf("expected channel name new-channel, got %s", channel.name)
		}
	})

	t.Run("returns existing channel when already exists", func(t *testing.T) {
		channel1, err := lobby.getOrCreateChannel("existing-channel")

		if err != nil {
			t.Fatalf("expected no error on first call, got %v", err)
		}
		channel2, err := lobby.getOrCreateChannel("existing-channel")

		if err != nil {
			t.Fatalf("expected no error on second call, got %v", err)
		}
		if channel1 != channel2 {
			t.Error("expected same channel instance")
		}
	})
}

func TestLobbyChannelCreationRaceCondition(t *testing.T) {
	ctx := context.Background()

	endpoint := createTestEndpoint(ctx)

	lobby := newLobby(endpoint)

	var wg sync.WaitGroup
	channels := make([]*Channel, 10)

	errors := make([]error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func(index int) {
			defer wg.Done()

			ch, err := lobby.getOrCreateChannel("concurrent-channel")

			channels[index] = ch
			errors[index] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errors {
		if err != nil {
			t.Errorf("goroutine %d got error: %v", i, err)
		}
	}
	firstChannel := channels[0]
	for i, ch := range channels {
		if ch != firstChannel {
			t.Errorf("goroutine %d got different channel instance", i)
		}
	}
	if lobby.channels.Len() != 1 {
		t.Errorf("expected 1 channel, got %d", lobby.channels.Len())
	}
}

func TestLobbyOnChannelDestroyed(t *testing.T) {
	ctx := context.Background()

	endpoint := createTestEndpoint(ctx)

	lobby := newLobby(endpoint)

	_, err := lobby.createChannel("destroy-test")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	destroyFunc := lobby.onChannelDestroyed("destroy-test")

	err = destroyFunc()

	if err != nil {
		t.Errorf("expected no error from destroy function, got %v", err)
	}
	_, err = lobby.channels.Read("destroy-test")

	if err == nil {
		t.Error("expected channel to be removed from lobby")
	}
	_, err = endpoint.channels.Read("destroy-test")

	if err == nil {
		t.Error("expected channel to be removed from endpoint")
	}
}

func TestLobbyWithCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	endpoint := createTestEndpoint(ctx)

	lobby := newLobby(endpoint)

	cancel()

	t.Run("getOrCreateChannel returns error", func(t *testing.T) {
		_, err := lobby.getOrCreateChannel("test-channel")

		if err == nil {
			t.Error("expected error when context is cancelled")
		}
	})

	t.Run("createChannel returns error", func(t *testing.T) {
		_, err := lobby.createChannel("test-channel")

		if err == nil {
			t.Error("expected error when context is cancelled")
		}
	})
}

func TestLobbyMiddlewareIntegration(t *testing.T) {
	ctx := context.Background()

	endpoint := createTestEndpoint(ctx)

	lobby := newLobby(endpoint)

	leaveCalled := false
	lobby.OnLeave(func(ctx *LeaveContext) {
		leaveCalled = true
	})

	channel, err := lobby.createChannel("middleware-test")

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if channel.leave == nil {
		t.Error("expected channel to have leave handler from lobby")
	}
	testUser := User{UserID: "test"}
	leaveCtx := newLeaveContext(ctx, channel, &testUser, "test")
	(*channel.leave)(leaveCtx)

	if !leaveCalled {
		t.Error("expected leave handler to be called through channel")
	}
}

func TestLobbyGetChannel(t *testing.T) {
	ctx := context.Background()
	endpoint := createTestEndpoint(ctx)
	lobby := newLobby(endpoint)

	t.Run("returns nil for non-existent channel", func(t *testing.T) {
		channel, err := lobby.GetChannel("nonexistent")
		if err == nil {
			t.Error("expected error for non-existent channel")
		}
		if channel != nil {
			t.Error("expected nil channel")
		}
	})

	t.Run("returns channel when exists", func(t *testing.T) {
		created, err := lobby.createChannel("test-channel")
		if err != nil {
			t.Fatalf("failed to create channel: %v", err)
		}

		found, err := lobby.GetChannel("test-channel")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if found == nil {
			t.Error("expected to find channel")
		}
		if found != created {
			t.Error("expected same channel instance")
		}
	})
}
