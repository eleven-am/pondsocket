// This file contains the presenceClient struct which manages presence tracking for users
// in a channel. It handles presence state updates, broadcasts presence changes to tracked
// users, and coordinates distributed presence synchronization across nodes.
package pondsocket

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/google/uuid"
)

type presenceClient struct {
	mutex   sync.RWMutex
	store   *store[interface{}]
	channel *Channel
}

type presencePayload struct {
	Event     presenceEventType `json:"event"`
	UserID    string            `json:"userId"`
	Change    interface{}       `json:"change"`
	Presence  []interface{}     `json:"presence,omitempty"`
	NodeID    string            `json:"nodeId,omitempty"`
	RequestID string            `json:"requestId,omitempty"`
}

type assignsPayload struct {
	Event     assignsEventType `json:"event"`
	UserID    string           `json:"userId"`
	Key       string           `json:"key"`
	Value     interface{}      `json:"value"`
	Assigns   []interface{}    `json:"assigns,omitempty"`
	NodeID    string           `json:"nodeId,omitempty"`
	RequestID string           `json:"requestId,omitempty"`
}

func newPresence(channel *Channel) *presenceClient {
	return &presenceClient{
		store:   newStore[interface{}](),
		channel: channel,
	}
}

// Track starts tracking presence for a user with the given initial presence data.
// This broadcasts a join event to all other tracked users and initiates
// a presence sync request in distributed environments.
// Returns an error if the user is already being tracked.
func (p *presenceClient) Track(userID string, presenceData interface{}) error {

	var currentPresenceList []interface{}

	var trackErr error
	p.mutex.Lock()

	trackErr = p.store.Create(userID, presenceData)

	if trackErr == nil {
		currentPresenceList = p.store.Values().items
	}
	p.mutex.Unlock()

	if trackErr != nil {
		return trackErr
	}

	p.requestPresenceSync(userID)
	return p.publishPresenceEvent(join, userID, presenceData, currentPresenceList)
}

// Update changes the presence data for a tracked user.
// This broadcasts an update event to all other tracked users.
// Returns an error if the user is not currently being tracked.
func (p *presenceClient) Update(userID string, presenceData interface{}) error {

	var currentPresenceList []interface{}

	var updateErr error
	p.mutex.Lock()

	updateErr = p.store.Update(userID, presenceData)

	if updateErr == nil {
		currentPresenceList = p.store.Values().items
	}
	p.mutex.Unlock()

	if updateErr != nil {
		return updateErr
	}

	return p.publishPresenceEvent(update, userID, presenceData, currentPresenceList)
}

// UnTrack stops tracking presence for a user.
// This broadcasts a leave event to all other tracked users with the user's
// last known presence data. If the user is not being tracked, returns nil.
func (p *presenceClient) UnTrack(userID string) error {

	var oldPresenceData interface{}

	var currentPresenceList []interface{}

	var err error
	p.mutex.Lock()

	oldPresenceData, err = p.store.Read(userID)

	if err != nil {
		p.mutex.Unlock()

		var pondErr *Error
		if errors.As(err, &pondErr) && pondErr.Code == StatusNotFound {
			return nil
		}
		return err
	}
	err = p.store.Delete(userID)

	if err == nil {
		currentPresenceList = p.store.Values().items
	}
	p.mutex.Unlock()

	if err != nil {
		return wrapF(err, "failed to delete presence data for user %s", userID)
	}

	return p.publishPresenceEvent(leave, userID, oldPresenceData, currentPresenceList)
}

// GetAll returns a map of all tracked users' presence data.
// The map is keyed by user ID with presence data as values.
// This returns a snapshot of the current presence state.
func (p *presenceClient) GetAll() map[string]interface{} {
	p.mutex.RLock()

	defer p.mutex.RUnlock()

	allData := p.store.List()

	return allData
}

// Get returns the presence data for a specific tracked user.
// Returns an error if the user is not being tracked.
func (p *presenceClient) Get(userID string) (interface{}, error) {
	p.mutex.RLock()

	defer p.mutex.RUnlock()

	presenceData, err := p.store.Read(userID)

	if err != nil {
		return nil, err
	}
	return presenceData, nil
}

func (p *presenceClient) publishPresenceEvent(eventType presenceEventType, userID string, change interface{}, currentPresenceList []interface{}) error {
	payload := presencePayload{
		Event:  eventType,
		UserID: userID,
		Change: change,
	}
	if eventType == join {
		payload.Presence = currentPresenceList
	}
	evt := Event{
		Action:      presence,
		ChannelName: p.channel.name,
		RequestId:   uuid.NewString(),
		Payload:     payload,
		Event:       string(eventType),
		NodeID:      p.channel.nodeID,
	}
	if p.channel.pubsub != nil && p.channel.endpointPath != "" {
		cleanEndpoint := p.channel.endpointPath
		if len(cleanEndpoint) > 0 && cleanEndpoint[0] == '/' {
			cleanEndpoint = cleanEndpoint[1:]
		}
		topic := formatTopic(cleanEndpoint, p.channel.name, string(eventType))

		data, err := json.Marshal(evt)

		if err != nil {
			return err
		}
		return p.channel.pubsub.Publish(topic, data)
	} else {
		p.channel.presence.mutex.RLock()

		trackedUsers := p.channel.presence.store.Keys()

		p.channel.presence.mutex.RUnlock()

		connectedUsers := p.channel.connections.Keys()

		recipientIDs := trackedUsers.filter(func(userID string) bool {
			return connectedUsers.some(func(connID string) bool {
				return connID == userID
			})
		})

		if recipientIDs.length() > 0 {
			internalEv := internalEvent{
				Event:      evt,
				Recipients: recipientIDs,
			}
			select {
			case p.channel.channel <- internalEv:
			case <-p.channel.ctx.Done():
			default:
			}
		}
	}
	return nil
}

func (p *presenceClient) requestPresenceSync(requesterUserID string) {
	if p.channel.pubsub == nil || p.channel.endpointPath == "" {
		return
	}
	cleanEndpoint := p.channel.endpointPath
	if len(cleanEndpoint) > 0 && cleanEndpoint[0] == '/' {
		cleanEndpoint = cleanEndpoint[1:]
	}
	requestID := uuid.NewString()

	p.channel.addSyncCoordinator(requestID, requesterUserID)

	payload := presencePayload{
		Event:     syncRequest,
		RequestID: requestID,
	}
	evt := Event{
		Action:      presence,
		ChannelName: p.channel.name,
		RequestId:   requestID,
		Payload:     payload,
		Event:       string(syncRequest),
		NodeID:      p.channel.nodeID,
	}
	topic := formatTopic(cleanEndpoint, p.channel.name, string(syncRequest))

	data, err := json.Marshal(evt)

	if err != nil {
		return
	}
	go func() {
		if err := p.channel.pubsub.Publish(topic, data); err != nil {
			p.channel.reportError("pubsub_publish", err)
		}
	}()
}
