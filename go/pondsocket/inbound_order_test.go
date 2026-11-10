package pondsocket

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type inboundProbe struct {
	mutex   sync.Mutex
	byConn  map[string][]int
	arrived map[string]chan struct{}
	counts  map[string]int
	delay   map[string]time.Duration
}

func newInboundProbe() *inboundProbe {
	return &inboundProbe{
		byConn:  make(map[string][]int),
		arrived: make(map[string]chan struct{}),
		counts:  make(map[string]int),
		delay:   make(map[string]time.Duration),
	}
}

func (p *inboundProbe) expect(connID string, n int, perMessage time.Duration) <-chan struct{} {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	done := make(chan struct{})
	p.arrived[connID] = done
	p.counts[connID] = n
	p.delay[connID] = perMessage
	return done
}

func (p *inboundProbe) record(connID string, n int) {
	p.mutex.Lock()
	delay := p.delay[connID]
	p.mutex.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}

	p.mutex.Lock()
	p.byConn[connID] = append(p.byConn[connID], n)
	finished := len(p.byConn[connID]) == p.counts[connID]
	done := p.arrived[connID]
	p.mutex.Unlock()

	if finished {
		close(done)
	}
}

func (p *inboundProbe) sequence(connID string) []int {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return append([]int(nil), p.byConn[connID]...)
}

func registerSeqHandler(r *wireRig, probe *inboundProbe) {
	r.lobby.OnMessage("seq", func(c *EventContext) error {
		var p sequenced
		raw, err := json.Marshal(c.GetPayload())
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		probe.record(c.GetUser().UserID, p.N)
		return nil
	})
}

func sendSeq(t *testing.T, c *websocket.Conn, from, to int) {
	t.Helper()
	for i := from; i < to; i++ {
		if err := c.WriteJSON(map[string]any{"action": "BROADCAST", "channelName": "/room/1", "requestId": fmt.Sprint("r", i), "event": "seq", "payload": map[string]any{"n": i}}); err != nil {
			t.Errorf("write #%d: %v", i, err)
			return
		}
	}
}

func TestInboundMessagesFromOneConnectionAreHandledInArrivalOrder(t *testing.T) {
	const total = 10000

	r := newWireRig(t, nil, nil)
	probe := newInboundProbe()
	registerSeqHandler(r, probe)

	c := r.dialJoined("j1")
	defer c.Close()
	id := r.connID(0)
	done := probe.expect(id, total, 0)

	go sendSeq(t, c, 0, total)

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("only %d of %d messages handled", len(probe.sequence(id)), total)
	}
	assertAscending(t, "inbound", probe.sequence(id))
}

func TestSlowHandlerOnOneConnectionDoesNotDelayAnother(t *testing.T) {
	const slowMessages = 5
	const slowDelay = 300 * time.Millisecond
	const fastMessages = 200
	const bound = 500 * time.Millisecond

	r := newWireRig(t, nil, nil)
	probe := newInboundProbe()
	registerSeqHandler(r, probe)

	slow := r.dialJoined("slow")
	defer slow.Close()
	fast := r.dialJoined("fast")
	defer fast.Close()
	slowID := r.connID(0)
	fastID := r.connID(1)

	slowDone := probe.expect(slowID, slowMessages, slowDelay)
	fastDone := probe.expect(fastID, fastMessages, 0)

	start := time.Now()
	go sendSeq(t, slow, 0, slowMessages)
	time.Sleep(50 * time.Millisecond)
	go sendSeq(t, fast, 0, fastMessages)

	select {
	case <-fastDone:
	case <-time.After(10 * time.Second):
		t.Fatalf("fast connection handled only %d of %d", len(probe.sequence(fastID)), fastMessages)
	}
	if elapsed := time.Since(start); elapsed > bound {
		t.Fatalf("fast connection took %v while another connection's handler was slow, bound %v", elapsed, bound)
	}
	assertAscending(t, "fast", probe.sequence(fastID))

	select {
	case <-slowDone:
	case <-time.After(10 * time.Second):
		t.Fatal("slow connection never finished")
	}
	assertAscending(t, "slow", probe.sequence(slowID))
}
