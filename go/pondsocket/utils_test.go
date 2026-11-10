package pondsocket

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestParsePayload(t *testing.T) {
	t.Run("parses valid payload", func(t *testing.T) {
		payload := map[string]interface{}{
			"name": "test",
			"age":  float64(25),
		}

		var result struct {
			Name string  `json:"name"`
			Age  float64 `json:"age"`
		}

		err := parsePayload(&result, payload)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result.Name != "test" {
			t.Errorf("expected name 'test', got '%s'", result.Name)
		}
		if result.Age != 25 {
			t.Errorf("expected age 25, got %f", result.Age)
		}
	})

	t.Run("returns error for invalid payload", func(t *testing.T) {
		payload := make(chan int)

		var result struct{}
		err := parsePayload(&result, payload)
		if err == nil {
			t.Error("expected error for invalid payload")
		}
	})
}

func TestParseAssigns(t *testing.T) {
	t.Run("parses valid assigns", func(t *testing.T) {
		assigns := map[string]interface{}{
			"role":   "admin",
			"active": true,
		}

		var result struct {
			Role   string `json:"role"`
			Active bool   `json:"active"`
		}

		err := parseAssigns(&result, assigns)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result.Role != "admin" {
			t.Errorf("expected role 'admin', got '%s'", result.Role)
		}
		if !result.Active {
			t.Error("expected active to be true")
		}
	})

	t.Run("returns error for nil assigns", func(t *testing.T) {
		var result struct{}
		err := parseAssigns(&result, nil)
		if err == nil {
			t.Error("expected error for nil assigns")
		}
	})
}

func TestParsePresence(t *testing.T) {
	t.Run("parses valid presence", func(t *testing.T) {
		presence := map[string]interface{}{
			"status": "online",
			"typing": false,
		}

		var result struct {
			Status string `json:"status"`
			Typing bool   `json:"typing"`
		}

		err := parsePresence(&result, presence)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if result.Status != "online" {
			t.Errorf("expected status 'online', got '%s'", result.Status)
		}
	})

	t.Run("returns error for nil presence", func(t *testing.T) {
		var result struct{}
		err := parsePresence(&result, nil)
		if err == nil {
			t.Error("expected error for nil presence")
		}
	})
}

func TestMapToError(t *testing.T) {
	t.Run("returns nil when all operations succeed", func(t *testing.T) {
		arr := newArray[int]()
		arr.push(1)
		arr.push(2)
		arr.push(3)

		err := mapToError(arr, func(i int) error {
			return nil
		})

		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	})

	t.Run("returns combined errors when operations fail", func(t *testing.T) {
		arr := newArray[int]()
		arr.push(1)
		arr.push(2)

		err := mapToError(arr, func(i int) error {
			return badRequest("test", fmt.Sprintf("error %d", i))
		})

		if err == nil {
			t.Error("expected error")
		}
	})
}

func TestMergeContexts(t *testing.T) {
	type ctxKey string
	key := ctxKey("request-scoped-value")

	t.Run("values from child contexts are visible on the merged context", func(t *testing.T) {
		parent := context.Background()
		child := context.WithValue(context.Background(), key, "from-child")

		merged, cancel := mergeContexts(parent, child)
		defer cancel()

		if got := merged.Value(key); got != "from-child" {
			t.Errorf("expected merged context to expose child value %q, got %v", "from-child", got)
		}
	})

	t.Run("parent values remain visible when the child has no value", func(t *testing.T) {
		parent := context.WithValue(context.Background(), key, "from-parent")
		child := context.Background()

		merged, cancel := mergeContexts(parent, child)
		defer cancel()

		if got := merged.Value(key); got != "from-parent" {
			t.Errorf("expected merged context to expose parent value %q, got %v", "from-parent", got)
		}
	})

	t.Run("cancelling the parent cancels the merged context", func(t *testing.T) {
		parent, parentCancel := context.WithCancel(context.Background())
		child := context.Background()

		merged, cancel := mergeContexts(parent, child)
		defer cancel()

		parentCancel()

		select {
		case <-merged.Done():
		case <-time.After(time.Second):
			t.Error("expected merged context to be done after parent cancellation")
		}
	})

	t.Run("cancelling a child context cancels the merged context", func(t *testing.T) {
		parent := context.Background()
		child, childCancel := context.WithCancel(context.Background())

		merged, cancel := mergeContexts(parent, child)
		defer cancel()

		childCancel()

		select {
		case <-merged.Done():
		case <-time.After(time.Second):
			t.Error("expected merged context to be done after child cancellation")
		}
	})

	t.Run("calling the returned cancel func cancels the merged context", func(t *testing.T) {
		merged, cancel := mergeContexts(context.Background(), context.Background())

		cancel()

		select {
		case <-merged.Done():
		case <-time.After(time.Second):
			t.Error("expected merged context to be done after calling cancel")
		}
	})
}
