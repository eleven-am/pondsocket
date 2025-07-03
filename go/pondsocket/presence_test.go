package pondsocket

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func createTestChannel(t *testing.T) *Channel {
	ctx := context.Background()

	opts := options{
		Name:                 "test-channel",
		Middleware:           newMiddleWare[*messageEvent, *Channel](),
		Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
		InternalQueueTimeout: 1 * time.Second,
	}
	return newChannel(ctx, opts)
}

func TestPresenceCreation(t *testing.T) {
	channel := createTestChannel(t)

	defer channel.Close()

	t.Run("creates presence client", func(t *testing.T) {
		presence := newPresence(channel)

		if presence == nil {
			t.Fatal("expected presence client to be created")
		}
		if presence.store == nil {
			t.Error("expected store to be initialized")
		}
		if presence.channel != channel {
			t.Error("expected channel reference to be set")
		}
	})
}

func TestPresenceTrack(t *testing.T) {
	channel := createTestChannel(t)

	defer channel.Close()

	conn := createTestConn("user1", nil)

	channel.addUser(conn)

	channel.connections.Update(conn.ID, conn)

	t.Run("tracks user successfully", func(t *testing.T) {
		presenceData := map[string]interface{}{
			"status": "online",
			"device": "desktop",
		}
		err := channel.presence.Track("user1", presenceData)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		data, err := channel.presence.Get("user1")

		if err != nil {
			t.Errorf("expected to get presence data, got error: %v", err)
		}
		if data == nil {
			t.Error("expected presence data to be stored")
		}
	})

	t.Run("prevents duplicate tracking", func(t *testing.T) {
		err := channel.presence.Track("user1", map[string]interface{}{"status": "online"})

		if err == nil {
			t.Error("expected error when tracking duplicate user")
		}
	})

	t.Run("broadcasts join event", func(t *testing.T) {
		conn2 := createTestConn("user2", nil)

		channel.addUser(conn2)

		select {
		case <-conn.send:
		default:
		}
		err := channel.presence.Track("user2", map[string]interface{}{"status": "online"})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		time.Sleep(50 * time.Millisecond)

		select {
		case msg := <-conn.send:
			if len(msg) == 0 {
				t.Error("expected broadcast message")
			}
			msgStr := string(msg)

			if !contains(msgStr, "presence:join") {
				t.Error("expected presence:join event in broadcast")
			}
		default:
			t.Error("expected broadcast message to be sent")
		}
	})
}

func TestPresenceUpdate(t *testing.T) {
	channel := createTestChannel(t)

	defer channel.Close()

	conn := createTestConn("user1", nil)

	channel.addUser(conn)

	t.Run("updates tracked user", func(t *testing.T) {
		channel.presence.Track("user1", map[string]interface{}{"status": "online"})

		newData := map[string]interface{}{
			"status": "away",
			"since":  time.Now().Unix(),
		}
		err := channel.presence.Update("user1", newData)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		data, _ := channel.presence.Get("user1")

		dataMap := data.(map[string]interface{})

		if dataMap["status"] != "away" {
			t.Errorf("expected status to be 'away', got %v", dataMap["status"])
		}
	})

	t.Run("fails for untracked user", func(t *testing.T) {
		err := channel.presence.Update("nonexistent", map[string]interface{}{"status": "online"})

		if err == nil {
			t.Error("expected error when updating untracked user")
		}
	})

	t.Run("broadcasts update event", func(t *testing.T) {
		channel.presence.Track("user1", map[string]interface{}{"status": "online"})

		conn2 := createTestConn("user2", nil)

		channel.addUser(conn2)

		channel.presence.Track("user2", map[string]interface{}{"status": "online"})

		time.Sleep(50 * time.Millisecond)

		for {
			select {
			case <-conn.send:
				continue
			case <-conn2.send:
				continue
			default:
				goto done
			}
		}
	done:
		err := channel.presence.Update("user2", map[string]interface{}{"status": "busy"})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		time.Sleep(50 * time.Millisecond)

		select {
		case msg := <-conn.send:
			msgStr := string(msg)

			if !contains(msgStr, "presence:update") {
				t.Error("expected presence:update event in broadcast")
			}
		case msg := <-conn2.send:
			msgStr := string(msg)

			if !contains(msgStr, "presence:update") {
				t.Error("expected presence:update event in broadcast")
			}
		default:
			t.Error("expected broadcast message to be sent")
		}
	})
}

func TestPresenceUnTrack(t *testing.T) {
	channel := createTestChannel(t)

	defer channel.Close()

	conn := createTestConn("user1", nil)

	channel.addUser(conn)

	t.Run("untracks user successfully", func(t *testing.T) {
		channel.presence.Track("user1", map[string]interface{}{"status": "online"})

		err := channel.presence.UnTrack("user1")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		_, err = channel.presence.Get("user1")

		if err == nil {
			t.Error("expected error when getting untracked user")
		}
	})

	t.Run("returns nil for already untracked user", func(t *testing.T) {
		err := channel.presence.UnTrack("nonexistent")

		if err != nil {
			t.Error("expected nil when untracking non-existent user")
		}
	})

	t.Run("broadcasts leave event", func(t *testing.T) {
		channel.presence.Track("user1", map[string]interface{}{"status": "online"})

		conn2 := createTestConn("user2", nil)

		channel.addUser(conn2)

		channel.presence.Track("user2", map[string]interface{}{"status": "online"})

		time.Sleep(50 * time.Millisecond)

		for {
			select {
			case <-conn.send:
				continue
			case <-conn2.send:
				continue
			default:
				goto done2
			}
		}
	done2:
		err := channel.presence.UnTrack("user2")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		time.Sleep(50 * time.Millisecond)

		select {
		case msg := <-conn.send:
			msgStr := string(msg)

			if !contains(msgStr, "presence:leave") {
				t.Error("expected presence:leave event in broadcast")
			}
		default:
			t.Error("expected broadcast message to be sent to user1")
		}
	})
}

func TestPresenceGetAll(t *testing.T) {
	channel := createTestChannel(t)

	defer channel.Close()

	t.Run("returns all tracked users", func(t *testing.T) {
		users := map[string]interface{}{
			"user1": map[string]interface{}{"status": "online"},
			"user2": map[string]interface{}{"status": "away"},
			"user3": map[string]interface{}{"status": "busy"},
		}
		for userID, data := range users {
			channel.presence.Track(userID, data)
		}
		allPresence := channel.presence.GetAll()

		if len(allPresence) != 3 {
			t.Errorf("expected 3 users, got %d", len(allPresence))
		}
		for userID := range users {
			if allPresence[userID] == nil {
				t.Errorf("expected user %s to be in presence map", userID)
			}
		}
	})

	t.Run("returns empty map when no users", func(t *testing.T) {
		channel := createTestChannel(t)

		defer channel.Close()

		allPresence := channel.presence.GetAll()

		if len(allPresence) != 0 {
			t.Errorf("expected empty map, got %d users", len(allPresence))
		}
	})
}

func TestPresenceConcurrency(t *testing.T) {
	channel := createTestChannel(t)

	defer channel.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			userID := fmt.Sprintf("user%d", n)

			data := map[string]interface{}{
				"status": "online",
				"id":     n,
			}
			channel.presence.Track(userID, data)
		}(i)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			channel.presence.GetAll()
		}()
	}
	wg.Wait()

	allPresence := channel.presence.GetAll()

	if len(allPresence) == 0 {
		t.Error("expected some users to be tracked")
	}
}

func TestPresenceBroadcastOptimization(t *testing.T) {
	channel := createTestChannel(t)

	defer channel.Close()

	conn := createTestConn("user1", nil)

	channel.addUser(conn)

	channel.presence.Track("user1", map[string]interface{}{"status": "online"})

	t.Run("join event includes full presence list", func(t *testing.T) {
		select {
		case <-conn.send:
		default:
		}
		channel.presence.Track("user2", map[string]interface{}{"status": "online"})

		time.Sleep(50 * time.Millisecond)

		select {
		case msg := <-conn.send:
			msgStr := string(msg)

			if !contains(msgStr, "presence") {
				t.Error("expected presence array in join broadcast")
			}
		default:
			t.Error("expected broadcast message")
		}
	})

	t.Run("update event does not include full list", func(t *testing.T) {
		conn2 := createTestConn("user2", nil)

		channel.addUser(conn2)

		channel.presence.Track("user2", map[string]interface{}{"status": "online"})

		select {
		case <-conn.send:
		default:
		}
		channel.presence.Update("user2", map[string]interface{}{"status": "away"})

		time.Sleep(50 * time.Millisecond)

		select {
		case msg := <-conn.send:
			msgStr := string(msg)

			if contains(msgStr, `"presence":[`) {
				t.Error("update event should not include full presence list")
			}
		default:
			t.Error("expected broadcast message")
		}
	})
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
