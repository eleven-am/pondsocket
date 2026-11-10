package pondsocket

import (
	"strings"
	"testing"
	"time"
)

func TestStalledClientDoesNotDelayDeliveryToOthers(t *testing.T) {
	const stalledMessages = 400
	const probes = 20
	const callBound = 100 * time.Millisecond
	const deliveryBound = time.Second

	r := newWireRig(t, func(o *Options) {
		o.SendChannelBuffer = 16
		o.SendTimeout = 5 * time.Second
		o.WriteWait = 5 * time.Second
	}, nil)

	stalled := r.dialJoined("stalled")
	defer stalled.Close()
	healthy := r.dialJoined("healthy")
	defer healthy.Close()

	stalledID := r.connID(0)
	healthyID := r.connID(1)
	ch := r.channel()

	pad := strings.Repeat("x", 64*1024)
	go func() {
		for i := 0; i < stalledMessages; i++ {
			if err := ch.BroadcastTo("bulk", map[string]any{"n": i, "pad": pad}, stalledID); err != nil {
				return
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	var slowest time.Duration
	for i := 0; i < probes; i++ {
		start := time.Now()
		if err := ch.BroadcastTo("probe", sequenced{N: i}, healthyID); err != nil {
			t.Fatalf("BroadcastTo healthy: %v", err)
		}
		if d := time.Since(start); d > slowest {
			slowest = d
		}
	}
	if slowest > callBound {
		t.Fatalf("BroadcastTo to a healthy user took %v while another client stalled, bound %v", slowest, callBound)
	}

	got := make(chan []int, 1)
	go func() { got <- collectSequence(t, healthy, "probe", probes) }()

	select {
	case seq := <-got:
		assertAscending(t, "healthy", seq)
	case <-time.After(deliveryBound):
		t.Fatalf("healthy client did not receive %d probes within %v while another client stalled", probes, deliveryBound)
	}

	select {
	case id := <-r.discs:
		if id != stalledID {
			t.Fatalf("disconnected %q, want the stalled client %q", id, stalledID)
		}
	case <-time.After(deliveryBound):
		t.Fatal("stalled client was not disconnected")
	}
}
