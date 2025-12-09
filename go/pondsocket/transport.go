package pondsocket

type TransportType string

const (
	TransportWebSocket TransportType = "websocket"
	TransportSSE       TransportType = "sse"
)

type Transport interface {
	GetID() string
	SendJSON(v interface{}) error
	GetAssign(key string) interface{}
	SetAssign(key string, value interface{})
	GetAssigns() map[string]interface{}
	CloneAssigns() map[string]interface{}
	IsActive() bool
	Close()
	OnClose(callback func(Transport) error)
	OnMessage(handler func(Event, Transport) error)
	HandleMessages()
	Type() TransportType
	PushMessage(data []byte) error
}

type transportEventHandler func(event Event, transport Transport) error
