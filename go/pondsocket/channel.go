// This file contains the Channel struct which represents a communication room where users
// can join, send messages, and track presence. Channels handle message broadcasting,
// user management, presence tracking, and distributed state synchronization through PubSub.
package pondsocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

type syncCoordinator struct {
	requestID       string
	requesterUserID string
	responses       map[string]map[string]interface{}
	timeout         time.Duration
	completeChan    chan map[string]interface{}
	mutex           sync.RWMutex
	completed       bool
}

type Channel struct {
	name                        string
	endpointPath                string
	presence                    *presenceClient
	leave                       *LeaveHandler
	store                       *store[map[string]interface{}]
	channel                     chan internalEvent
	connections                 *store[Transport]
	middleware                  *middleware[*messageEvent, *Channel]
	outgoing                    *middleware[*OutgoingContext, interface{}]
	onDestroy                   func() error
	pubsub                      PubSub
	nodeID                      string
	ctx                         context.Context
	cancel                      context.CancelFunc
	mutex                       sync.RWMutex
	syncCoordinators            map[string]*syncCoordinator
	assignsSyncCoordinators     map[string]*syncCoordinator
	syncCoordinatorMutex        sync.RWMutex
	assignsSyncCoordinatorMutex sync.RWMutex
	internalQueueTimeout        time.Duration
	hooks                       *Hooks
	dispatchSem                 chan struct{}
}

func newChannel(ctx context.Context, options options) *Channel {
	channelCtx, cancel := context.WithCancel(ctx)

	c := Channel{
		name:                    options.Name,
		store:                   newStore[map[string]interface{}](),
		outgoing:                options.Outgoing,
		connections:             newStore[Transport](),
		channel:                 make(chan internalEvent, 128),
		middleware:              options.Middleware,
		leave:                   options.Leave,
		onDestroy:               options.OnDestroy,
		pubsub:                  options.PubSub,
		hooks:                   options.Hooks,
		nodeID:                  uuid.NewString(),
		ctx:                     channelCtx,
		cancel:                  cancel,
		syncCoordinators:        make(map[string]*syncCoordinator),
		assignsSyncCoordinators: make(map[string]*syncCoordinator),
		internalQueueTimeout:    options.InternalQueueTimeout,
		dispatchSem:             make(chan struct{}, 32),
	}
	c.presence = newPresence(&c)

	c.handleMessageEvents()

	return &c
}

func (c *Channel) Name() string {
	return c.name
}

// RemoveUser removes a user from the channel, cleaning up their presence and assigns data.
// This method is automatically called when a user disconnects or leaves the channel.
// The reason parameter describes why the user is leaving (e.g., "disconnect", "evicted", "explicit_leave").
// If a leave handler is configured, it will be called asynchronously after removal.
// If this was the last user in the channel and an onDestroy handler is set, the channel will be destroyed.
func (c *Channel) RemoveUser(userID string, reason string) error {
	if err := c.checkState(); err != nil {
		return err
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()
	if err := c.checkState(); err != nil {
		return err
	}

	user, userErr := c.getUserUnsafe(userID)
	if userErr != nil {
		return userErr
	}

	storeErr := c.store.Delete(userID)
	presenceErr := c.presence.UnTrack(userID)
	connectionErr := c.connections.Delete(userID)
	combinedErr := combine(storeErr, presenceErr, connectionErr)
	if combinedErr != nil {
		return wrapF(combinedErr, "failed to fully remove user %s resources", userID)
	}

	if c.leave != nil && user != nil {
		go func(u User, channelName string, leaveReason string) {
			leaveCtx := newLeaveContext(c.ctx, c, &u, leaveReason)
			(*c.leave)(leaveCtx)
		}(*user, c.name, reason)
	}

	remainingUsers := c.store.Len()
	if c.onDestroy != nil && remainingUsers == 0 {
		go func() {
			if err := c.Close(); err != nil {
				c.reportError("channel_close", err)
			}
			if c.onDestroy != nil {
				if err := c.onDestroy(); err != nil {
					c.reportError("channel_on_destroy", err)
				}
			}
		}()
	}

	return nil
}

// GetUser retrieves a user's information including their assigns and presence data.
// Returns a User struct containing the user's ID, assigns (server-side metadata),
// and presence data if the user is being tracked.
// Returns an error if the user is not found in the channel.
func (c *Channel) GetUser(userID string) (*User, error) {
	if err := c.checkState(); err != nil {
		return nil, err
	}
	c.mutex.RLock()

	defer c.mutex.RUnlock()

	if err := c.checkState(); err != nil {
		return nil, err
	}
	return c.getUserUnsafe(userID)
}

// EvictUser forcefully removes a user from the channel and notifies all other users.
// The evicted user receives an "evicted" system event with the reason,
// and all other users receive a "user_evicted" broadcast event.
// After eviction, the user is removed from the channel.
func (c *Channel) EvictUser(userID, reason string) error {
	if err := c.checkState(); err != nil {
		return err
	}

	_, err := c.GetUser(userID)
	if err != nil {
		return err
	}

	evictionPayload := map[string]interface{}{
		"reason": reason,
		"userId": userID,
	}

	broadcastEvent := Event{
		Action:      system,
		ChannelName: c.name,
		RequestId:   uuid.NewString(),
		Event:       "user_evicted",
		Payload:     evictionPayload,
	}

	systemEvent := Event{
		Action:      system,
		ChannelName: c.name,
		RequestId:   uuid.NewString(),
		Event:       "evicted",
		Payload:     evictionPayload,
	}

	if err = c.sendMessage(string(channelEntity), recipients{userIds: []string{userID}}, systemEvent); err != nil {
		return wrapF(err, "failed to send eviction system message to user %s", userID)
	}

	if err = c.RemoveUser(userID, "evicted:"+reason); err != nil {
		return wrapF(err, "failed to remove user %s during eviction", userID)
	}

	if err = c.checkState(); err != nil {
		return nil
	}

	recp := all
	return c.sendMessage(string(channelEntity), recipients{recipient: &recp}, broadcastEvent)
}

// GetAssigns returns a map of all users' assigns data in the channel.
// In single-node deployments, returns local assigns data.
// In distributed deployments with PubSub configured, triggers a sync request to gather
// assigns data from all nodes and waits up to 500ms for responses.
// The returned map is keyed by user ID, with each value being that user's assigns map.
// Assigns are server-side metadata and are never sent to clients automatically.
// Returns nil if the channel is shutting down.
func (c *Channel) GetAssigns() map[string]map[string]interface{} {
	if err := c.checkState(); err != nil {
		return nil
	}

	if c.pubsub == nil || c.endpointPath == "" {
		return c.getLocalAssigns()
	}

	requestID := c.requestAssignsSync("system")
	if requestID == "" {
		return c.getLocalAssigns()
	}

	coordinator := c.getAssignsSyncCoordinator(requestID)
	if coordinator == nil {
		return c.getLocalAssigns()
	}

	select {
	case aggregated := <-coordinator.completeChan:
		c.removeAssignsSyncCoordinator(requestID)
		result := make(map[string]map[string]interface{})
		for userID, assigns := range aggregated {
			if assignsMap, ok := assigns.(map[string]interface{}); ok {
				result[userID] = assignsMap
			}
		}
		return result
	case <-time.After(500 * time.Millisecond):
		c.removeAssignsSyncCoordinator(requestID)
		return c.getLocalAssigns()
	case <-c.ctx.Done():
		c.removeAssignsSyncCoordinator(requestID)
		return nil
	}
}

func (c *Channel) getLocalAssigns() map[string]map[string]interface{} {
	assigns := c.store.List()

	result := make(map[string]map[string]interface{})
	for userID, userAssigns := range assigns {
		userAssignsCopy := make(map[string]interface{})
		for k, v := range userAssigns {
			userAssignsCopy[k] = v
		}
		result[userID] = userAssignsCopy
	}
	return result
}

// GetPresence returns a map of all tracked users' presence data in the channel.
// In single-node deployments, returns local presence data.
// In distributed deployments with PubSub configured, triggers a sync request to gather
// presence data from all nodes and waits up to 500ms for responses.
// The returned map is keyed by user ID, with each value being that user's presence data.
// Only users who have been explicitly tracked will appear in this map.
// Returns nil if the channel is shutting down.
func (c *Channel) GetPresence() map[string]interface{} {
	if err := c.checkState(); err != nil {
		return nil
	}

	if c.pubsub == nil || c.endpointPath == "" {
		return c.presence.GetAll()
	}

	requestID := uuid.NewString()
	coordinator := c.addSyncCoordinator(requestID, "system")

	cleanEndpoint := c.endpointPath
	if len(cleanEndpoint) > 0 && cleanEndpoint[0] == '/' {
		cleanEndpoint = cleanEndpoint[1:]
	}

	payload := presencePayload{
		Event:     syncRequest,
		RequestID: requestID,
	}
	evt := Event{
		Action:      presence,
		ChannelName: c.name,
		RequestId:   requestID,
		Payload:     payload,
		Event:       string(syncRequest),
		NodeID:      c.nodeID,
	}
	topic := formatTopic(cleanEndpoint, c.name, string(syncRequest))

	data, err := json.Marshal(evt)
	if err != nil {
		c.removeSyncCoordinator(requestID)
		return c.presence.GetAll()
	}

	go func() {
		if err := c.pubsub.Publish(topic, data); err != nil {
			c.reportError("pubsub_publish", err)
		}
	}()

	select {
	case aggregated := <-coordinator.completeChan:
		c.removeSyncCoordinator(requestID)
		return aggregated
	case <-time.After(500 * time.Millisecond):
		c.removeSyncCoordinator(requestID)
		return c.presence.GetAll()
	case <-c.ctx.Done():
		c.removeSyncCoordinator(requestID)
		return nil
	}
}

// Track starts tracking presence for a user with the provided presence data.
// Once tracked, the user will receive presence updates from other tracked users.
// The presence data is broadcast to all other tracked users in the channel.
// In distributed setups, a presence sync is initiated to get data from other nodes.
func (c *Channel) Track(userID string, presenceData interface{}) error {
	if err := c.checkState(); err != nil {
		return err
	}
	return c.presence.Track(userID, presenceData)
}

// UnTrack stops tracking presence for a user.
// The user will no longer receive presence updates and their presence data
// is removed and broadcast as a leave event to other tracked users.
// If the user is not being tracked, this method returns nil without error.
func (c *Channel) UnTrack(userID string) error {
	if err := c.checkState(); err != nil {
		return err
	}
	return c.presence.UnTrack(userID)
}

// UpdatePresence updates the presence data for a tracked user.
// The updated presence data is broadcast to all other tracked users in the channel.
// Returns an error if the user is not currently being tracked.
func (c *Channel) UpdatePresence(userID string, presenceData interface{}) error {
	if err := c.checkState(); err != nil {
		return err
	}
	return c.presence.Update(userID, presenceData)
}

// UpdateAssigns updates a specific assign key-value pair for a user.
// Assigns are server-side metadata that are never automatically sent to clients.
// In distributed setups, the update is synchronized across nodes via PubSub.
// Returns an error if the user does not exist in the channel.
func (c *Channel) UpdateAssigns(userID string, key string, value interface{}) error {
	if err := c.checkState(); err != nil {
		return err
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	if err := c.checkState(); err != nil {
		return err
	}
	assigns, err := c.store.Read(userID)

	if err != nil {
		return notFound(c.name, "User does not exist in channel to update assigns")
	}

	assignsCopy := make(map[string]interface{})
	if assigns != nil {
		for k, v := range assigns {
			assignsCopy[k] = v
		}
	}

	assignsCopy[key] = value
	err = c.store.Update(userID, assignsCopy)

	if err != nil {
		return wrapF(err, "failed to update assigns store for user %s", userID)
	}
	if c.pubsub != nil && c.endpointPath != "" {
		go c.broadcastAssignsUpdate(userID, key, value)
	}

	return nil
}

// Broadcast sends an event with payload to all users in the channel.
// The event is delivered as a broadcast action to every connected user.
// This is the primary method for sending messages to all channel participants.
func (c *Channel) Broadcast(e string, payload interface{}) error {
	if err := c.checkState(); err != nil {
		return err
	}
	recp := all
	recipients := recipients{
		recipient: &recp,
	}
	response := Event{
		Action:      broadcast,
		ChannelName: c.name,
		RequestId:   uuid.NewString(),
		Event:       e,
		Payload:     payload,
	}
	return c.sendMessage(string(channelEntity), recipients, response)
}

// BroadcastTo sends an event with payload to specific users in the channel.
// Only the users whose IDs are provided will receive the message.
// If no user IDs are provided, no message is sent and nil is returned.
// This is useful for targeted messaging within a channel.
func (c *Channel) BroadcastTo(e string, payload interface{}, userIDs ...string) error {
	if err := c.checkState(); err != nil {
		return err
	}
	if len(userIDs) == 0 {
		return nil
	}
	recps := recipients{
		userIds: userIDs,
	}
	response := Event{
		Action:      broadcast,
		ChannelName: c.name,
		RequestId:   uuid.NewString(),
		Event:       e,
		Payload:     payload,
	}
	return c.sendMessage(string(channelEntity), recps, response)
}

// BroadcastFrom sends an event with payload to all users except the specified sender.
// This is commonly used when a user triggers an action that should notify
// all other users but not themselves (e.g., "user is typing" notifications).
func (c *Channel) BroadcastFrom(e string, payload interface{}, senderId string) error {
	if err := c.checkState(); err != nil {
		return err
	}
	recp := allExceptSender
	recps := recipients{
		recipient: &recp,
	}
	response := Event{
		Action:      broadcast,
		ChannelName: c.name,
		RequestId:   uuid.NewString(),
		Event:       e,
		Payload:     payload,
	}
	return c.sendMessage(senderId, recps, response)
}

// Close gracefully shuts down the channel, removing all users and cleaning up resources.
// This method closes all connections, clears presence and assigns data,
// cancels sync coordinators, unsubscribes from PubSub topics, and closes internal channels.
// After calling Close, the channel should not be used.
func (c *Channel) Close() error {
	c.mutex.Lock()

	select {
	case <-c.ctx.Done():
		c.mutex.Unlock()

		return nil
	default:
	}
	c.cancel()

	c.mutex.Unlock()

	userIds := c.connections.Keys()

	var errs []error
	for _, userId := range userIds.items {
		if conn, _ := c.connections.Read(userId); conn != nil {
			conn.Close()
		}
		if err := c.store.Delete(userId); err != nil {
			errs = append(errs, err)
		}
		if err := c.presence.UnTrack(userId); err != nil {
			errs = append(errs, err)
		}
		if err := c.connections.Delete(userId); err != nil {
			errs = append(errs, err)
		}
	}
	c.syncCoordinatorMutex.Lock()

	for requestID := range c.syncCoordinators {
		delete(c.syncCoordinators, requestID)
	}
	c.syncCoordinatorMutex.Unlock()

	c.assignsSyncCoordinatorMutex.Lock()

	for requestID := range c.assignsSyncCoordinators {
		delete(c.assignsSyncCoordinators, requestID)
	}
	c.assignsSyncCoordinatorMutex.Unlock()

	time.Sleep(10 * time.Millisecond)

	close(c.channel)

	if c.pubsub != nil && c.endpointPath != "" {
		cleanEndpoint := c.endpointPath
		if len(cleanEndpoint) > 0 && cleanEndpoint[0] == '/' {
			cleanEndpoint = cleanEndpoint[1:]
		}
		pattern := fmt.Sprintf("pondsocket:%s:%s:.*", cleanEndpoint, c.name)

		if err := c.pubsub.Unsubscribe(pattern); err != nil {
			errs = append(errs, err)
		}
	}

	return combine(errs...)
}

func (c *Channel) addUser(user Transport) error {
	c.mutex.Lock()

	defer c.mutex.Unlock()

	if err := c.checkState(); err != nil {
		return err
	}
	assignsToAdd := user.CloneAssigns()

	if assignsToAdd == nil {
		assignsToAdd = make(map[string]interface{})
	}
	errAssigns := c.store.Create(user.GetID(), assignsToAdd)

	errConn := c.connections.Create(user.GetID(), user)

	combinedErr := combine(errAssigns, errConn)

	if combinedErr != nil {
		_ = c.store.Delete(user.GetID())

		_ = c.connections.Delete(user.GetID())

		return wrapF(combinedErr, "failed to add user %s, already exists or other error", user.GetID())
	}
	user.OnClose(c.onConnectionClose)

	return nil
}

func (c *Channel) broadcast(senderUserID string, event *Event) error {
	if err := c.checkState(); err != nil {
		return err
	}
	user, err := c.GetUser(senderUserID)

	if err != nil {
		return wrapF(err, "failed to process broadcast from unknown user %s", senderUserID)
	}
	ctx, cancel := context.WithTimeout(c.ctx, c.internalQueueTimeout)

	defer cancel()

	req := &messageEvent{
		Event: event,
		User:  user,
	}
	finalHandler := c.notFoundHandler(ctx, senderUserID, event)

	return c.middleware.Handle(ctx, req, c, finalHandler)
}

func (c *Channel) sendMessage(sender string, recipients recipients, event Event) error {
	if err := c.checkState(); err != nil {
		return err
	}
	if sender != string(channelEntity) {
		if _, err := c.GetUser(sender); err != nil {
			return forbidden(c.name, "Sender not in channel").withDetails(map[string]string{"sender": sender})
		}
	}

	var targetUserIDs *array[string]
	allUserIDs := c.connections.Keys()

	if recipients.recipient != nil {
		if *recipients.recipient == all {
			targetUserIDs = allUserIDs
		} else if *recipients.recipient == allExceptSender && sender != string(channelEntity) {
			targetUserIDs = allUserIDs.filter(func(item string) bool { return item != sender })
		} else {
			return badRequest(c.name, "Invalid symbolic recipient type").withDetails(map[string]string{"receivedType": string(*recipients.recipient)})
		}
	} else if len(recipients.userIds) > 0 {
		specifiedIDs := fromSlice(recipients.userIds)

		targetUserIDs = allUserIDs.filter(func(connId string) bool {
			return specifiedIDs.some(func(targetId string) bool { return targetId == connId })
		})

		if targetUserIDs.length() < specifiedIDs.length() {
			return notFound(c.name, "Some specified recipients not found").withDetails(map[string]interface{}{
				"specifiedIds": specifiedIDs.items,
				"foundIds":     targetUserIDs.items,
			})
		}
	} else {
		return nil
	}

	if targetUserIDs.length() == 0 {
		return nil
	}

	internalEv := internalEvent{
		Event:      event,
		Recipients: targetUserIDs,
	}

	if c.pubsub != nil && c.endpointPath != "" {
		cleanEndpoint := c.endpointPath
		if len(cleanEndpoint) > 0 && cleanEndpoint[0] == '/' {
			cleanEndpoint = cleanEndpoint[1:]
		}

		topic := formatTopic(cleanEndpoint, c.name, event.Event)

		eventForPubSub := event
		eventForPubSub.NodeID = c.nodeID
		data, err := json.Marshal(eventForPubSub)

		if err == nil {
			go func() {
				if err := c.pubsub.Publish(topic, data); err != nil {
					c.reportError("pubsub_publish", err)
				}
			}()
		}
	}
	select {
	case c.channel <- internalEv:
		return nil
	case <-c.ctx.Done():
		return wrapF(c.ctx.Err(), "channel %s context cancelled while queueing message", c.name)

	case <-time.After(c.internalQueueTimeout):
		return timeout(c.name, "timeout queueing internal message; channel processor might be stuck or overloaded")
	}
}

func (c *Channel) notFoundHandler(ctx context.Context, sender string, e *Event) FinalHandlerFunc[*messageEvent, *Channel] {
	return func(request *messageEvent, response *Channel) error {
		select {
		case <-ctx.Done():
			return ctx.Err()

		default:
		}
		if err := c.checkState(); err != nil {
			return err
		}
		message := Event{
			Action:      system,
			ChannelName: c.name,
			RequestId:   e.RequestId,
			Event:       string(notFoundEvent),
			Payload:     notFound(c.name, fmt.Sprintf("No handler registered for this event: %s", e.Event)),
		}
		return c.sendMessage(string(channelEntity), recipients{userIds: []string{sender}}, message)
	}
}

func (c *Channel) onMessage(event *internalEvent) error {
	if err := c.checkState(); err != nil {
		return err
	}
	newEvent := event.Event
	recps := event.Recipients
	connectionsToSend := c.connections.GetByKeys(recps.items...)

	if connectionsToSend.length() == 0 {
		return nil
	}

	var wg sync.WaitGroup
	var errMutex sync.Mutex
	var deliveryErrors error

	for _, conn := range connectionsToSend.items {
		select {
		case <-c.ctx.Done():
			return c.ctx.Err()
		default:
		}

		wg.Add(1)
		go func(transport Transport, ev Event) {
			defer wg.Done()
			eventCopy := ev
			if err := c.processOutgoing(&eventCopy, transport); err != nil {
				errMutex.Lock()
				deliveryErrors = addError(deliveryErrors, err)
				errMutex.Unlock()
			}
		}(conn, newEvent)
	}

	wg.Wait()
	return deliveryErrors
}

func (c *Channel) getUserUnsafe(userID string) (*User, error) {
	assignsData, assignErr := c.store.Read(userID)

	if assignErr != nil {
		return nil, assignErr
	}
	presenceData, presenceErr := c.presence.Get(userID)

	if presenceErr != nil {
		var pondErr *Error
		isNotFoundError := false
		if errors.As(presenceErr, &pondErr) {
			if pondErr.Code == StatusNotFound {
				isNotFoundError = true
			}
		}
		if isNotFoundError {
			presenceData = nil
		} else {
			return nil, wrapF(combine(assignErr, presenceErr), "failed to retrieve presence data for user %s", userID)
		}
	}
	return &User{
		UserID:   userID,
		Assigns:  assignsData,
		Presence: presenceData,
	}, nil
}

func (c *Channel) onConnectionClose(user Transport) error {
	if err := c.checkState(); err != nil {
		return err
	}
	return c.RemoveUser(user.GetID(), "connection_closed")
}

func (c *Channel) getUserAssigns(userID, key string) (interface{}, error) {
	if err := c.checkState(); err != nil {
		return nil, err
	}
	c.mutex.RLock()

	defer c.mutex.RUnlock()

	if err := c.checkState(); err != nil {
		return nil, err
	}
	assigns, err := c.store.Read(userID)

	if err != nil {
		return nil, notFound(c.name, "User does not exist in channel")
	}
	if assigns == nil {
		return nil, notFound(c.name, "Assign key not found for user").withDetails(map[string]interface{}{"userId": userID, "key": key})
	}
	value, exists := assigns[key]
	if !exists {
		return nil, notFound(c.name, "Assign key not found for user").withDetails(map[string]interface{}{"userId": userID, "key": key})
	}
	return value, nil
}

func (c *Channel) handleMessageEvents() {
	go func() {
		for {
			select {
			case ev, ok := <-c.channel:
				if !ok {
					return
				}

				select {
				case c.dispatchSem <- struct{}{}:
				case <-c.ctx.Done():
					return
				}

				go func(event internalEvent) {
					defer func() {
						<-c.dispatchSem
						if r := recover(); r != nil {
							c.reportError("channel_dispatch_panic", fmt.Errorf("panic recovered: %v", r))
						}
					}()

					if err := c.onMessage(&event); err != nil {
						if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
							return
						}
						c.reportError("channel_dispatch", err)
					}
				}(ev)

			case <-c.ctx.Done():
				return
			}
		}
	}()
}

func (c *Channel) checkState() error {
	select {
	case <-c.ctx.Done():
		err := c.ctx.Err()
		return wrapF(err, "channel %s is shutting down", c.name)
	default:
		return nil
	}
}

func (c *Channel) reportError(component string, err error) {
	if err == nil || c == nil || c.hooks == nil || c.hooks.Metrics == nil {
		return
	}
	c.hooks.Metrics.Error(component, err)
}

func (c *Channel) processOutgoing(event *Event, conn Transport) error {
	if err := c.checkState(); err != nil {
		return err
	}
	user, err := c.GetUser(conn.GetID())

	if err != nil {
		return wrapF(err, "failed to get user data for connection %s", conn.GetID())
	}
	ctx, cancel := context.WithTimeout(c.ctx, c.internalQueueTimeout)

	defer cancel()

	outgoingCtx := newOutgoingContext(ctx, c, event, user, conn)

	err = c.outgoing.Handle(ctx, outgoingCtx, nil, c.finalOutgoingHandler())

	if err != nil {
		return wrapF(err, "failed to process outgoing message for user %s", user.UserID)
	}
	return outgoingCtx.sendMessage()
}

func (c *Channel) finalOutgoingHandler() FinalHandlerFunc[*OutgoingContext, interface{}] {
	return func(ctx *OutgoingContext, _ interface{}) error {
		return nil
	}
}

func (c *Channel) subscribeToPubSub() {
	if c.pubsub == nil || c.endpointPath == "" {
		return
	}
	cleanEndpoint := c.endpointPath
	if len(cleanEndpoint) > 0 && cleanEndpoint[0] == '/' {
		cleanEndpoint = cleanEndpoint[1:]
	}
	pattern := fmt.Sprintf("pondsocket:%s:%s:.*", cleanEndpoint, c.name)

	err := c.pubsub.Subscribe(pattern, func(topic string, data []byte) {
		if err := c.checkState(); err != nil {
			return
		}

		var event Event
		if err := json.Unmarshal(data, &event); err != nil {
			return
		}

		if event.NodeID == c.nodeID {
			return
		}
		if event.Action == assigns {
			c.handleRemoteAssignsEvent(&event)

			return
		}

		var recipientIDs *array[string]
		if event.Action == presence {
			if event.Event == string(syncResponse) {
				c.handleSyncResponse(&event)
				return
			}

			c.handleRemotePresenceEvent(&event)
			c.presence.mutex.RLock()
			trackedUsers := c.presence.store.Keys()
			c.presence.mutex.RUnlock()
			connectedUsers := c.connections.Keys()
			recipientIDs = trackedUsers.filter(func(userID string) bool {
				return connectedUsers.some(func(connID string) bool {
					return connID == userID
				})
			})
		} else {
			recipientIDs = c.connections.Keys()
		}
		if recipientIDs.length() == 0 {
			return
		}
		internalEv := internalEvent{
			Event:      event,
			Recipients: recipientIDs,
		}
		select {
		case c.channel <- internalEv:
		case <-c.ctx.Done():
		default:
		}
	})

	if err != nil {
		c.reportError("pubsub_subscribe", err)
	}
}

func (c *Channel) handleRemotePresenceEvent(event *Event) {
	payload, ok := event.Payload.(map[string]interface{})

	if !ok {
		return
	}
	eventType, _ := payload["event"].(string)
	userID, _ := payload["userId"].(string)
	change := payload["change"]
	if eventType != string(syncRequest) && userID == "" {
		return
	}
	switch presenceEventType(eventType) {
	case join:
		if change != nil {
			c.presence.mutex.Lock()
			_ = c.presence.store.Create(userID, change)
			c.presence.mutex.Unlock()
		}
	case update:
		if change != nil {
			c.presence.mutex.Lock()
			_ = c.presence.store.Update(userID, change)
			c.presence.mutex.Unlock()
		}
	case leave:
		c.presence.mutex.Lock()

		_ = c.presence.store.Delete(userID)

		c.presence.mutex.Unlock()

	case syncRequest:
		go c.handlePresenceSyncRequest(event)
	}
}

func (c *Channel) broadcastAssignsUpdate(userID string, key string, value interface{}) {
	if err := c.checkState(); err != nil {
		return
	}

	cleanEndpoint := c.endpointPath
	if len(cleanEndpoint) > 0 && cleanEndpoint[0] == '/' {
		cleanEndpoint = cleanEndpoint[1:]
	}

	assignsPayload := map[string]interface{}{
		"UserID": userID,
		"Key":    key,
		"Value":  value,
	}

	event := Event{
		Action:      assigns,
		ChannelName: c.name,
		RequestId:   uuid.NewString(),
		Event:       "assigns:update",
		Payload:     assignsPayload,
		NodeID:      c.nodeID,
	}
	topic := formatTopic(cleanEndpoint, c.name, "assigns:update")

	data, err := json.Marshal(event)

	if err != nil {
		return
	}
	if err := c.pubsub.Publish(topic, data); err != nil {
		c.reportError("pubsub_publish", err)
	}
}

func (c *Channel) handleRemoteAssignsEvent(event *Event) {
	if event.Event == string(assignsSyncRequest) {
		go c.handleAssignsSyncRequest(event)
		return
	}
	if event.Event == string(assignsSyncResponse) {
		c.handleAssignsSyncResponse(event)
		return
	}
	payload, ok := event.Payload.(map[string]interface{})
	if !ok {
		return
	}
	userID, _ := payload["UserID"].(string)

	key, _ := payload["Key"].(string)

	value := payload["Value"]
	if userID == "" || key == "" {
		return
	}
	assigns, err := c.store.Read(userID)
	if err != nil {
		assigns = make(map[string]interface{})
		assigns[key] = value
		_ = c.store.Create(userID, assigns)
		return
	}

	assignsCopy := make(map[string]interface{})
	for k, v := range assigns {
		assignsCopy[k] = v
	}
	assignsCopy[key] = value
	_ = c.store.Update(userID, assignsCopy)
}

func newSyncCoordinator(requestID, requesterUserID string, timeout time.Duration) *syncCoordinator {
	return &syncCoordinator{
		requestID:       requestID,
		requesterUserID: requesterUserID,
		responses:       make(map[string]map[string]interface{}),
		timeout:         timeout,
		completeChan:    make(chan map[string]interface{}, 1),
		completed:       false,
	}
}

func (c *Channel) addSyncCoordinator(requestID, requesterUserID string) *syncCoordinator {
	c.syncCoordinatorMutex.Lock()
	defer c.syncCoordinatorMutex.Unlock()
	coordinator := newSyncCoordinator(requestID, requesterUserID, 500*time.Millisecond)

	c.syncCoordinators[requestID] = coordinator
	go c.handleSyncTimeout(coordinator)

	return coordinator
}

func (c *Channel) getSyncCoordinator(requestID string) *syncCoordinator {
	c.syncCoordinatorMutex.RLock()
	defer c.syncCoordinatorMutex.RUnlock()
	return c.syncCoordinators[requestID]
}

func (c *Channel) removeSyncCoordinator(requestID string) {
	c.syncCoordinatorMutex.Lock()
	defer c.syncCoordinatorMutex.Unlock()
	delete(c.syncCoordinators, requestID)
}

func (c *Channel) addAssignsSyncCoordinator(requestID, requesterUserID string) *syncCoordinator {
	c.assignsSyncCoordinatorMutex.Lock()
	defer c.assignsSyncCoordinatorMutex.Unlock()
	coordinator := newSyncCoordinator(requestID, requesterUserID, 500*time.Millisecond)

	c.assignsSyncCoordinators[requestID] = coordinator
	go c.handleAssignsSyncTimeout(coordinator)

	return coordinator
}

func (c *Channel) getAssignsSyncCoordinator(requestID string) *syncCoordinator {
	c.assignsSyncCoordinatorMutex.RLock()
	defer c.assignsSyncCoordinatorMutex.RUnlock()
	return c.assignsSyncCoordinators[requestID]
}

func (c *Channel) removeAssignsSyncCoordinator(requestID string) {
	c.assignsSyncCoordinatorMutex.Lock()
	defer c.assignsSyncCoordinatorMutex.Unlock()
	delete(c.assignsSyncCoordinators, requestID)
}

func (c *syncCoordinator) addResponse(nodeID string, presenceData map[string]interface{}) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.completed {
		return
	}
	c.responses[nodeID] = presenceData
}

func (c *syncCoordinator) aggregateResponses() map[string]interface{} {
	c.mutex.RLock()
	defer c.mutex.RUnlock()
	merged := make(map[string]interface{})

	for _, presenceData := range c.responses {
		for userID, userData := range presenceData {
			merged[userID] = userData
		}
	}
	return merged
}

func (c *syncCoordinator) complete() map[string]interface{} {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.completed {
		return nil
	}
	c.completed = true

	merged := make(map[string]interface{})
	for _, presenceData := range c.responses {
		for userID, userData := range presenceData {
			merged[userID] = userData
		}
	}

	select {
	case c.completeChan <- merged:
	default:
	}
	return merged
}

func (c *Channel) handleSyncTimeout(coordinator *syncCoordinator) {
	select {
	case <-time.After(coordinator.timeout):
		aggregated := coordinator.complete()

		if aggregated != nil {
			c.sendSyncComplete(coordinator.requestID, coordinator.requesterUserID, aggregated)
		}
		c.removeSyncCoordinator(coordinator.requestID)

	case <-coordinator.completeChan:
		c.removeSyncCoordinator(coordinator.requestID)

	case <-c.ctx.Done():
		c.removeSyncCoordinator(coordinator.requestID)
	}
}

func (c *Channel) handleAssignsSyncTimeout(coordinator *syncCoordinator) {
	select {
	case <-time.After(coordinator.timeout):
		aggregated := coordinator.complete()

		if aggregated != nil {
			c.sendAssignsSyncComplete(coordinator.requestID, coordinator.requesterUserID, aggregated)
		}
		c.removeAssignsSyncCoordinator(coordinator.requestID)

	case <-coordinator.completeChan:
		c.removeAssignsSyncCoordinator(coordinator.requestID)

	case <-c.ctx.Done():
		c.removeAssignsSyncCoordinator(coordinator.requestID)
	}
}

func (c *Channel) requestAssignsSync(requesterUserID string) string {
	if c.pubsub == nil || c.endpointPath == "" {
		return ""
	}
	cleanEndpoint := c.endpointPath
	if len(cleanEndpoint) > 0 && cleanEndpoint[0] == '/' {
		cleanEndpoint = cleanEndpoint[1:]
	}
	requestID := uuid.NewString()

	c.addAssignsSyncCoordinator(requestID, requesterUserID)

	payload := assignsPayload{
		Event:     assignsSyncRequest,
		RequestID: requestID,
	}
	evt := Event{
		Action:      assigns,
		ChannelName: c.name,
		RequestId:   requestID,
		Payload:     payload,
		Event:       string(assignsSyncRequest),
		NodeID:      c.nodeID,
	}
	topic := formatTopic(cleanEndpoint, c.name, string(assignsSyncRequest))

	data, err := json.Marshal(evt)
	if err != nil {
		c.removeAssignsSyncCoordinator(requestID)
		return ""
	}

	go func() {
		if err := c.pubsub.Publish(topic, data); err != nil {
			c.reportError("pubsub_publish", err)
		}
	}()

	return requestID
}

func (c *Channel) handlePresenceSyncRequest(requestEvent *Event) {
	if c.pubsub == nil || c.endpointPath == "" {
		return
	}
	payload, ok := requestEvent.Payload.(map[string]interface{})

	if !ok {
		return
	}
	requestID, _ := payload["requestId"].(string)

	if requestID == "" {
		return
	}
	presenceData := c.presence.GetAll()

	presenceSlice := make([]interface{}, 0, len(presenceData))

	for userID, userData := range presenceData {
		presenceSlice = append(presenceSlice, map[string]interface{}{
			"userId": userID,
			"data":   userData,
		})
	}
	cleanEndpoint := c.endpointPath
	if len(cleanEndpoint) > 0 && cleanEndpoint[0] == '/' {
		cleanEndpoint = cleanEndpoint[1:]
	}
	responsePayload := presencePayload{
		Event:     syncResponse,
		NodeID:    c.nodeID,
		RequestID: requestID,
		Presence:  presenceSlice,
	}
	evt := Event{
		Action:      presence,
		ChannelName: c.name,
		RequestId:   uuid.NewString(),
		Payload:     responsePayload,
		Event:       string(syncResponse),
		NodeID:      c.nodeID,
	}
	topic := formatTopic(cleanEndpoint, c.name, string(syncResponse))

	data, err := json.Marshal(evt)

	if err != nil {
		return
	}
	if err := c.pubsub.Publish(topic, data); err != nil {
		c.reportError("pubsub_publish", err)
	}
}

func (c *Channel) handleSyncResponse(event *Event) {
	payload, ok := event.Payload.(map[string]interface{})

	if !ok {
		return
	}
	requestID, _ := payload["requestId"].(string)

	nodeID, _ := payload["nodeId"].(string)

	presenceData, _ := payload["presence"].([]interface{})

	if requestID == "" || nodeID == "" {
		return
	}
	coordinator := c.getSyncCoordinator(requestID)

	if coordinator == nil {
		return
	}
	nodePresence := make(map[string]interface{})

	for _, item := range presenceData {
		if itemMap, ok := item.(map[string]interface{}); ok {
			if userID, exists := itemMap["userId"].(string); exists {
				nodePresence[userID] = itemMap["data"]
			}
		}
	}
	coordinator.addResponse(nodeID, nodePresence)
}

func (c *Channel) sendSyncComplete(requestID, requesterUserID string, aggregatedPresence map[string]interface{}) {
	if c.pubsub == nil || c.endpointPath == "" {
		return
	}
	presenceSlice := make([]interface{}, 0, len(aggregatedPresence))

	for userID, userData := range aggregatedPresence {
		presenceSlice = append(presenceSlice, map[string]interface{}{
			"userId": userID,
			"data":   userData,
		})
	}
	cleanEndpoint := c.endpointPath
	if len(cleanEndpoint) > 0 && cleanEndpoint[0] == '/' {
		cleanEndpoint = cleanEndpoint[1:]
	}
	completePayload := presencePayload{
		Event:     syncComplete,
		RequestID: requestID,
		Presence:  presenceSlice,
	}
	evt := Event{
		Action:      presence,
		ChannelName: c.name,
		RequestId:   uuid.NewString(),
		Payload:     completePayload,
		Event:       string(syncComplete),
		NodeID:      c.nodeID,
	}
	recipientIDs := fromSlice([]string{requesterUserID})

	internalEv := internalEvent{
		Event:      evt,
		Recipients: recipientIDs,
	}
	select {
	case c.channel <- internalEv:
	case <-c.ctx.Done():
	default:
	}
}

func (c *Channel) handleAssignsSyncRequest(requestEvent *Event) {
	if c.pubsub == nil || c.endpointPath == "" {
		return
	}
	payload, ok := requestEvent.Payload.(map[string]interface{})
	if !ok {
		return
	}
	requestID, _ := payload["requestId"].(string)
	if requestID == "" {
		return
	}

	assignsData := c.getLocalAssigns()
	assignsSlice := make([]interface{}, 0, len(assignsData))

	for userID, userAssigns := range assignsData {
		assignsSlice = append(assignsSlice, map[string]interface{}{
			"userId":  userID,
			"assigns": userAssigns,
		})
	}

	cleanEndpoint := c.endpointPath
	if len(cleanEndpoint) > 0 && cleanEndpoint[0] == '/' {
		cleanEndpoint = cleanEndpoint[1:]
	}

	responsePayload := assignsPayload{
		Event:     assignsSyncResponse,
		NodeID:    c.nodeID,
		RequestID: requestID,
		Assigns:   assignsSlice,
	}

	evt := Event{
		Action:      assigns,
		ChannelName: c.name,
		RequestId:   uuid.NewString(),
		Payload:     responsePayload,
		Event:       string(assignsSyncResponse),
		NodeID:      c.nodeID,
	}

	topic := formatTopic(cleanEndpoint, c.name, string(assignsSyncResponse))
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	if err := c.pubsub.Publish(topic, data); err != nil {
		c.reportError("pubsub_publish", err)
	}
}

func (c *Channel) handleAssignsSyncResponse(event *Event) {
	payload, ok := event.Payload.(map[string]interface{})
	if !ok {
		return
	}
	requestID, _ := payload["requestId"].(string)
	nodeID, _ := payload["nodeId"].(string)
	assignsData, _ := payload["assigns"].([]interface{})

	if requestID == "" || nodeID == "" {
		return
	}

	coordinator := c.getAssignsSyncCoordinator(requestID)
	if coordinator == nil {
		return
	}

	nodeAssigns := make(map[string]interface{})
	for _, item := range assignsData {
		if itemMap, ok := item.(map[string]interface{}); ok {
			if userID, exists := itemMap["userId"].(string); exists {
				nodeAssigns[userID] = itemMap["assigns"]
			}
		}
	}
	coordinator.addResponse(nodeID, nodeAssigns)
}

func (c *Channel) sendAssignsSyncComplete(requestID, requesterUserID string, aggregatedAssigns map[string]interface{}) {
	if c.pubsub == nil || c.endpointPath == "" {
		return
	}

	assignsSlice := make([]interface{}, 0, len(aggregatedAssigns))
	for userID, userAssigns := range aggregatedAssigns {
		assignsSlice = append(assignsSlice, map[string]interface{}{
			"userId":  userID,
			"assigns": userAssigns,
		})
	}

	cleanEndpoint := c.endpointPath
	if len(cleanEndpoint) > 0 && cleanEndpoint[0] == '/' {
		cleanEndpoint = cleanEndpoint[1:]
	}

	completePayload := assignsPayload{
		Event:     assignsSyncComplete,
		RequestID: requestID,
		Assigns:   assignsSlice,
	}

	evt := Event{
		Action:      assigns,
		ChannelName: c.name,
		RequestId:   uuid.NewString(),
		Payload:     completePayload,
		Event:       string(assignsSyncComplete),
		NodeID:      c.nodeID,
	}

	recipientIDs := fromSlice([]string{requesterUserID})
	internalEv := internalEvent{
		Event:      evt,
		Recipients: recipientIDs,
	}

	select {
	case c.channel <- internalEv:
	case <-c.ctx.Done():
	default:
	}
}
