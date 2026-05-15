package pondsocket

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const distributedProtocol = "pondsocket.distributed"
const distributedVersion = 1

type distributedEnvelope struct {
	Protocol            string      `json:"protocol"`
	Version             int         `json:"version"`
	Type                string      `json:"type"`
	MessageID           string      `json:"messageId"`
	SourceNodeID        string      `json:"sourceNodeId"`
	EndpointName        string      `json:"endpointName"`
	ChannelName         string      `json:"channelName"`
	Timestamp           int64       `json:"timestamp"`
	FromUserID          string      `json:"fromUserId,omitempty"`
	Event               string      `json:"event,omitempty"`
	Payload             interface{} `json:"payload,omitempty"`
	RequestID           string      `json:"requestId,omitempty"`
	RecipientDescriptor interface{} `json:"recipientDescriptor,omitempty"`
	UserID              string      `json:"userId,omitempty"`
	Presence            interface{} `json:"presence,omitempty"`
	Assigns             interface{} `json:"assigns,omitempty"`
	Reason              string      `json:"reason,omitempty"`
}

func distributedBytesFromEvent(endpoint string, event Event, sender string, recipientDescriptor interface{}) ([]byte, error) {
	env := distributedEnvelope{
		Protocol:     distributedProtocol,
		Version:      distributedVersion,
		Type:         distributedTypeFromEvent(event),
		MessageID:    uuid.NewString(),
		SourceNodeID: event.NodeID,
		EndpointName: endpoint,
		ChannelName:  event.ChannelName,
		Timestamp:    time.Now().UnixMilli(),
		RequestID:    event.RequestId,
	}
	switch env.Type {
	case "USER_MESSAGE":
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
	case "PRESENCE_UPDATE":
		env.Event = event.Event
		if payload, ok := event.Payload.(presencePayload); ok {
			env.UserID = payload.UserID
			env.Presence = payload.Change
		} else if payload, ok := event.Payload.(map[string]interface{}); ok {
			env.UserID, _ = payload["userId"].(string)
			env.Presence = payload["change"]
		}
	case "PRESENCE_REMOVED":
		if payload, ok := event.Payload.(presencePayload); ok {
			env.UserID = payload.UserID
		} else if payload, ok := event.Payload.(map[string]interface{}); ok {
			env.UserID, _ = payload["userId"].(string)
		}
	case "ASSIGNS_UPDATE":
		if payload, ok := event.Payload.(map[string]interface{}); ok {
			env.UserID, _ = payload["UserID"].(string)
			env.Assigns = payload
		}
	case "EVICT_USER", "USER_REMOVE":
		if payload, ok := event.Payload.(map[string]interface{}); ok {
			env.UserID, _ = payload["userID"].(string)
			env.Reason, _ = payload["reason"].(string)
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
		return "USER_MESSAGE"
	case presence:
		if event.Event == string(leave) {
			return "PRESENCE_REMOVED"
		}
		return "PRESENCE_UPDATE"
	case assigns:
		return "ASSIGNS_UPDATE"
	case userCommand:
		if event.Event == string(userRemoveCommand) {
			return "USER_REMOVE"
		}
		if event.Event == string(userGetRequest) {
			return "USER_GET_REQUEST"
		}
		if event.Event == string(userGetResponse) {
			return "USER_GET_RESPONSE"
		}
		return "EVICT_USER"
	default:
		return "USER_MESSAGE"
	}
}

func eventFromDistributedBytes(data []byte) (*Event, bool) {
	var env distributedEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, false
	}
	if env.Protocol != distributedProtocol || env.Version != distributedVersion {
		return nil, false
	}
	ev := Event{
		ChannelName: env.ChannelName,
		RequestId:   env.RequestID,
		NodeID:      env.SourceNodeID,
	}
	if ev.RequestId == "" {
		ev.RequestId = uuid.NewString()
	}
	switch env.Type {
	case "USER_MESSAGE":
		ev.Action = broadcast
		ev.Event = env.Event
		ev.Payload = env.Payload
		if ids, ok := env.RecipientDescriptor.([]interface{}); ok {
			for _, id := range ids {
				if s, ok := id.(string); ok {
					ev.Recipients = append(ev.Recipients, s)
				}
			}
		}
	case "PRESENCE_UPDATE":
		ev.Action = presence
		ev.Event = env.Event
		if ev.Event == "" {
			ev.Event = string(update)
		}
		ev.Payload = map[string]interface{}{"event": ev.Event, "userId": env.UserID, "change": env.Presence}
	case "PRESENCE_REMOVED":
		ev.Action = presence
		ev.Event = string(leave)
		ev.Payload = map[string]interface{}{"event": string(leave), "userId": env.UserID}
	case "ASSIGNS_UPDATE":
		ev.Action = assigns
		ev.Event = "assigns:update"
		if assignsMap, ok := env.Assigns.(map[string]interface{}); ok {
			ev.Payload = assignsMap
		} else {
			ev.Payload = map[string]interface{}{"UserID": env.UserID, "Assigns": env.Assigns}
		}
	case "EVICT_USER":
		ev.Action = userCommand
		ev.Event = string(userEvictCommand)
		ev.Payload = map[string]interface{}{"userID": env.UserID, "reason": env.Reason}
	case "USER_REMOVE":
		ev.Action = userCommand
		ev.Event = string(userRemoveCommand)
		ev.Payload = map[string]interface{}{"userID": env.UserID, "reason": env.Reason}
	default:
		return nil, false
	}
	return &ev, true
}
