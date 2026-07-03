package pondsocket

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const distributedProtocol = "pondsocket.distributed"
const distributedVersion = 1

const (
	msgStateRequest    = "STATE_REQUEST"
	msgStateResponse   = "STATE_RESPONSE"
	msgUserJoined      = "USER_JOINED"
	msgUserLeft        = "USER_LEFT"
	msgUserMessage     = "USER_MESSAGE"
	msgPresenceUpdate  = "PRESENCE_UPDATE"
	msgPresenceRemoved = "PRESENCE_REMOVED"
	msgAssignsUpdate   = "ASSIGNS_UPDATE"
	msgEvictUser       = "EVICT_USER"
	msgUserRemove      = "USER_REMOVE"
	msgUserGetRequest  = "USER_GET_REQUEST"
	msgUserGetResponse = "USER_GET_RESPONSE"
	msgNodeHeartbeat   = "NODE_HEARTBEAT"
)

type distributedUser struct {
	ID       string      `json:"id"`
	Assigns  interface{} `json:"assigns"`
	Presence interface{} `json:"presence"`
}

type distributedEnvelope struct {
	Protocol            string            `json:"protocol"`
	Version             int               `json:"version"`
	Type                string            `json:"type"`
	MessageID           string            `json:"messageId"`
	SourceNodeID        string            `json:"sourceNodeId"`
	EndpointName        string            `json:"endpointName"`
	ChannelName         string            `json:"channelName"`
	Timestamp           int64             `json:"timestamp"`
	FromUserID          string            `json:"fromUserId,omitempty"`
	Event               string            `json:"event,omitempty"`
	Payload             interface{}       `json:"payload,omitempty"`
	RequestID           string            `json:"requestId,omitempty"`
	RecipientDescriptor interface{}       `json:"recipientDescriptor,omitempty"`
	UserID              string            `json:"userId,omitempty"`
	Presence            interface{}       `json:"presence,omitempty"`
	Assigns             interface{}       `json:"assigns,omitempty"`
	Reason              string            `json:"reason,omitempty"`
	FromNode            string            `json:"fromNode,omitempty"`
	Users               []distributedUser `json:"users,omitempty"`
	NodeID              string            `json:"nodeId,omitempty"`
}

func newEnvelope(msgType, endpoint, channel, nodeID string) distributedEnvelope {
	return distributedEnvelope{
		Protocol:     distributedProtocol,
		Version:      distributedVersion,
		Type:         msgType,
		MessageID:    uuid.NewString(),
		SourceNodeID: nodeID,
		EndpointName: endpoint,
		ChannelName:  channel,
		Timestamp:    time.Now().UnixMilli(),
	}
}

func distributedBytesFromEvent(endpoint string, event Event, sender string, recipientDescriptor interface{}) ([]byte, error) {
	env := newEnvelope(distributedTypeFromEvent(event), endpoint, event.ChannelName, event.NodeID)
	switch env.Type {
	case msgUserMessage:
		env.RequestID = event.RequestId
		env.FromUserID = sender
		env.Event = event.Event
		env.Payload = event.Payload
		if recipientDescriptor != nil {
			env.RecipientDescriptor = recipientDescriptor
		} else if len(event.Recipients) > 0 {
			env.RecipientDescriptor = event.Recipients
		} else {
			env.RecipientDescriptor = "ALL_USERS"
		}
	case msgPresenceUpdate:
		if payload, ok := event.Payload.(presencePayload); ok {
			env.UserID = payload.UserID
			env.Presence = payload.Change
		} else if payload, ok := event.Payload.(map[string]interface{}); ok {
			env.UserID, _ = payload["userId"].(string)
			env.Presence = payload["change"]
		}
	case msgPresenceRemoved:
		if payload, ok := event.Payload.(presencePayload); ok {
			env.UserID = payload.UserID
		} else if payload, ok := event.Payload.(map[string]interface{}); ok {
			env.UserID, _ = payload["userId"].(string)
		}
	case msgAssignsUpdate:
		if payload, ok := event.Payload.(map[string]interface{}); ok {
			env.UserID, _ = payload["userId"].(string)
			if env.UserID == "" {
				env.UserID, _ = payload["UserID"].(string)
			}
			env.Assigns = payload["assigns"]
			if env.Assigns == nil {
				env.Assigns = payload["Assigns"]
			}
		}
	case msgEvictUser:
		if payload, ok := event.Payload.(map[string]interface{}); ok {
			env.UserID, _ = payload["userID"].(string)
			env.Reason, _ = payload["reason"].(string)
		}
	case msgUserRemove:
		if payload, ok := event.Payload.(map[string]interface{}); ok {
			env.UserID, _ = payload["userID"].(string)
		}
	case msgUserGetRequest:
		env.RequestID = event.RequestId
		env.FromNode = event.NodeID
		if payload, ok := event.Payload.(map[string]interface{}); ok {
			env.UserID, _ = payload["userID"].(string)
			if env.UserID == "" {
				env.UserID, _ = payload["userId"].(string)
			}
		}
	case msgUserGetResponse:
		env.RequestID = event.RequestId
		if payload, ok := event.Payload.(map[string]interface{}); ok {
			env.UserID, _ = payload["userID"].(string)
			if env.UserID == "" {
				env.UserID, _ = payload["userId"].(string)
			}
			env.Assigns = payload["assigns"]
			env.Presence = payload["presence"]
		}
	default:
		env.Payload = event.Payload
		env.Event = event.Event
	}
	return json.Marshal(env)
}

func distributedTypeFromEvent(event Event) string {
	switch event.Action {
	case broadcast:
		return msgUserMessage
	case presence:
		if event.Event == string(leave) {
			return msgPresenceRemoved
		}
		return msgPresenceUpdate
	case assigns:
		return msgAssignsUpdate
	case userCommand:
		if event.Event == string(userRemoveCommand) {
			return msgUserRemove
		}
		if event.Event == string(userGetRequest) {
			return msgUserGetRequest
		}
		if event.Event == string(userGetResponse) {
			return msgUserGetResponse
		}
		return msgEvictUser
	default:
		return msgUserMessage
	}
}

func encodeStateRequest(endpoint, channel, nodeID string) ([]byte, error) {
	env := newEnvelope(msgStateRequest, endpoint, channel, nodeID)
	env.FromNode = nodeID
	return json.Marshal(env)
}

func encodeStateResponse(endpoint, channel, nodeID string, users []distributedUser) ([]byte, error) {
	env := newEnvelope(msgStateResponse, endpoint, channel, nodeID)
	env.Users = users
	return json.Marshal(env)
}

func encodeUserJoined(endpoint, channel, nodeID, userID string, presence, assigns interface{}) ([]byte, error) {
	env := newEnvelope(msgUserJoined, endpoint, channel, nodeID)
	env.UserID = userID
	env.Presence = presence
	env.Assigns = assigns
	return json.Marshal(env)
}

func encodeUserLeft(endpoint, channel, nodeID, userID string) ([]byte, error) {
	env := newEnvelope(msgUserLeft, endpoint, channel, nodeID)
	env.UserID = userID
	return json.Marshal(env)
}

func encodeHeartbeat(endpoint, channel, nodeID string) ([]byte, error) {
	env := newEnvelope(msgNodeHeartbeat, endpoint, channel, nodeID)
	env.NodeID = nodeID
	return json.Marshal(env)
}

func decodeEnvelope(data []byte) (*distributedEnvelope, bool) {
	var env distributedEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, false
	}
	if env.Protocol != distributedProtocol || env.Version != distributedVersion {
		return nil, false
	}
	return &env, true
}

func eventFromDistributedBytes(data []byte) (*Event, bool) {
	env, ok := decodeEnvelope(data)
	if !ok {
		return nil, false
	}
	return eventFromEnvelope(env)
}

func eventFromEnvelope(env *distributedEnvelope) (*Event, bool) {
	ev := Event{
		ChannelName: env.ChannelName,
		RequestId:   env.RequestID,
		NodeID:      env.SourceNodeID,
	}
	if ev.RequestId == "" {
		ev.RequestId = uuid.NewString()
	}
	switch env.Type {
	case msgUserMessage:
		ev.Action = broadcast
		ev.Event = env.Event
		ev.Payload = env.Payload
		ev.FromUserID = env.FromUserID
		if ids, ok := env.RecipientDescriptor.([]interface{}); ok {
			for _, id := range ids {
				if s, ok := id.(string); ok {
					ev.Recipients = append(ev.Recipients, s)
				}
			}
		} else if descriptor, ok := env.RecipientDescriptor.(string); ok {
			ev.Recipient = descriptor
		}
	case msgPresenceUpdate:
		ev.Action = presence
		ev.Event = string(update)
		ev.Payload = map[string]interface{}{"userId": env.UserID, "change": env.Presence}
	case msgPresenceRemoved:
		ev.Action = presence
		ev.Event = string(leave)
		ev.Payload = map[string]interface{}{"userId": env.UserID}
	case msgAssignsUpdate:
		ev.Action = assigns
		ev.Event = "assigns:update"
		if env.Payload != nil {
			ev.Payload = env.Payload
		} else if assignsMap, ok := env.Assigns.(map[string]interface{}); ok {
			ev.Payload = map[string]interface{}{"userId": env.UserID, "assigns": assignsMap}
		} else {
			ev.Payload = map[string]interface{}{"userId": env.UserID, "assigns": env.Assigns}
		}
	case msgEvictUser:
		ev.Action = userCommand
		ev.Event = string(userEvictCommand)
		ev.Payload = map[string]interface{}{"userID": env.UserID, "reason": env.Reason}
	case msgUserRemove:
		ev.Action = userCommand
		ev.Event = string(userRemoveCommand)
		ev.Payload = map[string]interface{}{"userID": env.UserID, "reason": env.Reason}
	case msgUserGetRequest:
		ev.Action = userCommand
		ev.Event = string(userGetRequest)
		ev.Payload = map[string]interface{}{
			"userID":    env.UserID,
			"requestID": env.RequestID,
			"fromNode":  env.FromNode,
		}
	case msgUserGetResponse:
		ev.Action = userCommand
		ev.Event = string(userGetResponse)
		ev.Payload = map[string]interface{}{
			"requestID": env.RequestID,
			"userID":    env.UserID,
			"assigns":   env.Assigns,
			"presence":  env.Presence,
			"found":     true,
		}
	default:
		return nil, false
	}
	return &ev, true
}
