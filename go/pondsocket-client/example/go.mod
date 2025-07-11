module pondsocket-example

go 1.24.4

replace pondsocket => ../

require pondsocket v0.0.0-00010101000000-000000000000

require (
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
)
