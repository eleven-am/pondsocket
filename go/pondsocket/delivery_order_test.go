package pondsocket

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type sequenced struct {
	N int `json:"n"`
}

func collectSequence(t *testing.T, c *websocket.Conn, event string, want int) []int {
	t.Helper()
	got := make([]int, 0, want)
	deadline := time.Now().Add(20 * time.Second)
	for len(got) < want {
		if time.Now().After(deadline) {
			t.Fatalf("received only %d of %d %q messages", len(got), want, event)
		}
		msgs, err := readMessages(c, 10*time.Second)
		if err != nil {
			t.Fatalf("read after %d of %d %q messages: %v", len(got), want, event, err)
		}
		for _, m := range msgs {
			if m.Event != event {
				continue
			}
			var p sequenced
			if err := json.Unmarshal(m.Payload, &p); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
			got = append(got, p.N)
		}
	}
	return got
}

func assertAscending(t *testing.T, label string, got []int) {
	t.Helper()
	inversions := 0
	first := -1
	for i, n := range got {
		if n != i {
			inversions++
			if first < 0 {
				first = i
			}
		}
	}
	if inversions > 0 {
		lo := first
		hi := first + 10
		if hi > len(got) {
			hi = len(got)
		}
		t.Fatalf("%s: %d of %d messages out of position, first at index %d: %v", label, inversions, len(got), first, got[lo:hi])
	}
}

func TestBroadcastToSingleRecipientPreservesOrder(t *testing.T) {
	const total = 10000

	r := newWireRig(t, func(o *Options) { o.SendChannelBuffer = total }, nil)
	c := r.dialJoined("j1")
	defer c.Close()

	ch := r.channel()
	user := r.connID(0)

	errs := make(chan error, 1)
	go func() {
		for i := 0; i < total; i++ {
			if err := ch.BroadcastTo("seq", sequenced{N: i}, user); err != nil {
				errs <- fmt.Errorf("BroadcastTo #%d: %w", i, err)
				return
			}
		}
		errs <- nil
	}()

	got := collectSequence(t, c, "seq", total)
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	assertAscending(t, "BroadcastTo", got)
}

func TestJoinAcknowledgementPrecedesMessagesSentAfterAccept(t *testing.T) {
	const waves = 50
	const joinsPerWave = 20
	const afterAccept = 5

	r := newWireRig(t, nil, func(c *JoinContext) error {
		c.Accept()
		id := c.GetUser().UserID
		for i := 0; i < afterAccept; i++ {
			if err := c.Channel.BroadcastTo("boot", sequenced{N: i}, id); err != nil {
				return err
			}
		}
		return nil
	})

	c := r.dial()
	defer c.Close()

	type progress struct {
		acked bool
		boots int
	}
	state := make(map[string]*progress)
	overtaken := 0

	for wave := 0; wave < waves; wave++ {
		pending := 0
		for i := 0; i < joinsPerWave; i++ {
			name := fmt.Sprintf("/room/%d", wave*joinsPerWave+i)
			state[name] = &progress{}
			if err := c.WriteJSON(map[string]any{"action": "JOIN_CHANNEL", "channelName": name, "requestId": name, "event": "JOIN_CHANNEL", "payload": map[string]any{}}); err != nil {
				t.Fatalf("write join: %v", err)
			}
			pending++
		}
		for pending > 0 {
			msgs, err := readMessages(c, 10*time.Second)
			if err != nil {
				t.Fatalf("wave %d with %d joins incomplete: %v", wave, pending, err)
			}
			for _, m := range msgs {
				p, ok := state[m.Channel]
				if !ok {
					continue
				}
				switch m.Event {
				case string(acknowledgeEvent):
					p.acked = true
				case "boot":
					if !p.acked {
						overtaken++
					}
					p.boots++
					if p.boots == afterAccept {
						pending--
					}
				}
			}
		}
	}

	if overtaken > 0 {
		t.Fatalf("%d post-Accept messages (across %d joins) arrived before their join acknowledgement", overtaken, waves*joinsPerWave)
	}
}

func TestBroadcastKeepsPerRecipientOrder(t *testing.T) {
	const recipients = 25
	const total = 500

	r := newWireRig(t, nil, nil)

	clients := make([]*websocket.Conn, recipients)
	for i := range clients {
		clients[i] = r.dialJoined(fmt.Sprint("j", i))
		defer clients[i].Close()
	}

	ch := r.channel()
	go func() {
		for i := 0; i < total; i++ {
			if err := ch.Broadcast("seq", sequenced{N: i}); err != nil {
				t.Errorf("Broadcast #%d: %v", i, err)
				return
			}
		}
	}()

	var wg sync.WaitGroup
	results := make([][]int, recipients)
	for i, c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = collectSequence(t, c, "seq", total)
		}()
	}
	wg.Wait()

	for i, got := range results {
		if len(got) != total {
			t.Fatalf("recipient %d received %d of %d", i, len(got), total)
		}
		assertAscending(t, fmt.Sprintf("recipient %d", i), got)
	}
}

func TestOrderAcrossChannelsOnOneConnection(t *testing.T) {
	r := newWireRig(t, nil, nil)
	second := r.ep.CreateChannel("/other/:id", func(c *JoinContext) error {
		c.Accept()
		return nil
	})
	_ = second

	c := r.dial()
	defer c.Close()
	r.join(c, "j1")
	if err := c.WriteJSON(map[string]any{"action": "JOIN_CHANNEL", "channelName": "/other/1", "requestId": "j2", "event": "JOIN_CHANNEL", "payload": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	acked := false
	for !acked {
		msgs, err := readMessages(c, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range msgs {
			if m.Event == string(acknowledgeEvent) {
				acked = true
			}
		}
	}

	a := r.channel()
	b, err := r.ep.GetChannelByName("/other/1")
	if err != nil {
		t.Fatal(err)
	}
	user := r.connID(0)

	const total = 3000
	go func() {
		for i := 0; i < total; i++ {
			target := a
			event := "seq"
			if i%2 == 1 {
				target = b
			}
			if err := target.BroadcastTo(event, sequenced{N: i}, user); err != nil {
				t.Errorf("BroadcastTo #%d: %v", i, err)
				return
			}
		}
	}()

	got := collectSequence(t, c, "seq", total)
	assertAscending(t, "alternating channels", got)
}

func TestPendingMessagesAreFlushedBeforeEvictionNotice(t *testing.T) {
	r := newWireRig(t, nil, nil)
	c := r.dialJoined("j1")
	defer c.Close()

	ch := r.channel()
	user := r.connID(0)
	for i := 0; i < 50; i++ {
		if err := ch.BroadcastTo("seq", sequenced{N: i}, user); err != nil {
			t.Fatal(err)
		}
	}
	if err := ch.EvictUser(user, "test"); err != nil {
		t.Fatal(err)
	}

	var events []string
	for !strings.Contains(strings.Join(events, ","), "evicted") {
		msgs, err := readMessages(c, 5*time.Second)
		if err != nil {
			t.Fatalf("read: %v (events %v)", err, events)
		}
		for _, m := range msgs {
			events = append(events, m.Event)
		}
	}
	seq := 0
	for _, e := range events {
		if e == "seq" {
			seq++
		}
	}
	if seq != 50 {
		t.Fatalf("eviction notice overtook pending messages: %d of 50 seq messages arrived before it", seq)
	}
}
