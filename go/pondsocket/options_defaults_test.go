package pondsocket

import (
	"context"
	"testing"
)

func TestNewManagerFillsZeroOptionsFromDefaults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewManager(ctx, Options{})
	defaults := DefaultOptions()

	checks := []struct {
		name      string
		got, want any
	}{
		{"ReadBufferSize", m.Options.ReadBufferSize, defaults.ReadBufferSize},
		{"WriteBufferSize", m.Options.WriteBufferSize, defaults.WriteBufferSize},
		{"MaxMessageSize", m.Options.MaxMessageSize, defaults.MaxMessageSize},
		{"PingInterval", m.Options.PingInterval, defaults.PingInterval},
		{"PongWait", m.Options.PongWait, defaults.PongWait},
		{"WriteWait", m.Options.WriteWait, defaults.WriteWait},
		{"SendTimeout", m.Options.SendTimeout, defaults.SendTimeout},
		{"SendChannelBuffer", m.Options.SendChannelBuffer, defaults.SendChannelBuffer},
		{"ReceiveChannelBuffer", m.Options.ReceiveChannelBuffer, defaults.ReceiveChannelBuffer},
		{"InternalQueueTimeout", m.Options.InternalQueueTimeout, defaults.InternalQueueTimeout},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want default %v", c.name, c.got, c.want)
		}
	}
}

func TestNewManagerKeepsExplicitOptions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := NewManager(ctx, Options{SendChannelBuffer: 7, MaxConnections: 3, CheckOrigin: true})

	if m.Options.SendChannelBuffer != 7 {
		t.Errorf("SendChannelBuffer overridden: %d", m.Options.SendChannelBuffer)
	}
	if m.Options.MaxConnections != 3 {
		t.Errorf("MaxConnections overridden: %d", m.Options.MaxConnections)
	}
	if !m.Options.CheckOrigin {
		t.Error("CheckOrigin overridden")
	}
}

func TestPartialOptionsServeConnectionsWithoutPanicking(t *testing.T) {
	r := newWireRig(t, func(o *Options) {
		*o = Options{Hooks: o.Hooks}
	}, nil)

	c := r.dialJoined("j1")
	defer c.Close()

	if r.manager.Options.PingInterval <= 0 {
		t.Fatalf("PingInterval not defaulted: %v", r.manager.Options.PingInterval)
	}
}
