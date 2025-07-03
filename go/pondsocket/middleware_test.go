package pondsocket

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMiddleware(t *testing.T) {
	t.Run("executes single handler", func(t *testing.T) {
		mw := newMiddleWare[string, int]()

		handlerCalled := false
		mw.Use(func(ctx context.Context, req string, res int, next nextFunc) error {
			handlerCalled = true
			return next()
		})

		err := mw.Handle(context.Background(), "test", 42, func(req string, res int) error {
			return nil
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if !handlerCalled {
			t.Error("expected handler to be called")
		}
	})

	t.Run("executes handlers in order", func(t *testing.T) {
		mw := newMiddleWare[string, string]()

		order := make([]int, 0)

		mw.Use(func(ctx context.Context, req string, res string, next nextFunc) error {
			order = append(order, 1)

			return next()
		})

		mw.Use(func(ctx context.Context, req string, res string, next nextFunc) error {
			order = append(order, 2)

			return next()
		})

		mw.Use(func(ctx context.Context, req string, res string, next nextFunc) error {
			order = append(order, 3)

			return next()
		})

		err := mw.Handle(context.Background(), "test", "response", func(req string, res string) error {
			order = append(order, 4)

			return nil
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		expectedOrder := []int{1, 2, 3, 4}
		if len(order) != len(expectedOrder) {
			t.Fatalf("expected order length %d, got %d", len(expectedOrder), len(order))
		}
		for i, v := range order {
			if v != expectedOrder[i] {
				t.Errorf("expected order[%d] = %d, got %d", i, expectedOrder[i], v)
			}
		}
	})

	t.Run("stops on error", func(t *testing.T) {
		mw := newMiddleWare[string, string]()

		testErr := errors.New("test error")

		handler2Called := false
		mw.Use(func(ctx context.Context, req string, res string, next nextFunc) error {
			return testErr
		})

		mw.Use(func(ctx context.Context, req string, res string, next nextFunc) error {
			handler2Called = true
			return next()
		})

		err := mw.Handle(context.Background(), "test", "response", func(req string, res string) error {
			return nil
		})

		if err != testErr {
			t.Errorf("expected error %v, got %v", testErr, err)
		}
		if handler2Called {
			t.Error("expected second handler not to be called after error")
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		mw := newMiddleWare[string, string]()

		ctx, cancel := context.WithCancel(context.Background())

		cancel()

		mw.Use(func(ctx context.Context, req string, res string, next nextFunc) error {
			select {
			case <-ctx.Done():
				return ctx.Err()

			default:
				return next()
			}
		})

		err := mw.Handle(ctx, "test", "response", func(req string, res string) error {
			return nil
		})

		if err != context.Canceled {
			t.Errorf("expected context.Canceled error, got %v", err)
		}
	})

	t.Run("handler can choose not to call next", func(t *testing.T) {
		mw := newMiddleWare[string, string]()

		handler2Called := false
		finalHandlerCalled := false
		mw.Use(func(ctx context.Context, req string, res string, next nextFunc) error {
			return nil
		})

		mw.Use(func(ctx context.Context, req string, res string, next nextFunc) error {
			handler2Called = true
			return next()
		})

		err := mw.Handle(context.Background(), "test", "response", func(req string, res string) error {
			finalHandlerCalled = true
			return nil
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if handler2Called {
			t.Error("expected second handler not to be called")
		}
		if finalHandlerCalled {
			t.Error("expected final handler not to be called")
		}
	})

	t.Run("concurrent usage", func(t *testing.T) {
		mw := newMiddleWare[int, int]()

		mw.Use(func(ctx context.Context, req int, res int, next nextFunc) error {
			time.Sleep(10 * time.Millisecond)

			return next()
		})

		done := make(chan bool, 10)

		for i := 0; i < 10; i++ {
			go func(n int) {
				err := mw.Handle(context.Background(), n, 0, func(req int, res int) error {
					if req != n {
						t.Errorf("expected req %d, got %d", n, req)
					}
					return nil
				})

				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				done <- true
			}(i)
		}
		for i := 0; i < 10; i++ {
			<-done
		}
	})
}

func TestMiddlewareWithComplexTypes(t *testing.T) {

	type Request struct {
		ID   string
		Data map[string]interface{}
	}

	type Response struct {
		Status string
		Result interface{}
	}

	t.Run("modifies request and response", func(t *testing.T) {
		mw := newMiddleWare[*Request, *Response]()

		mw.Use(func(ctx context.Context, req *Request, res *Response, next nextFunc) error {
			req.Data["middleware1"] = true
			return next()
		})

		mw.Use(func(ctx context.Context, req *Request, res *Response, next nextFunc) error {
			err := next()

			if err == nil {
				res.Status = "processed"
			}
			return err
		})

		req := &Request{
			ID:   "test-123",
			Data: make(map[string]interface{}),
		}
		res := &Response{}
		err := mw.Handle(context.Background(), req, res, func(req *Request, res *Response) error {
			res.Result = req.Data
			return nil
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if !req.Data["middleware1"].(bool) {
			t.Error("expected middleware1 to set data")
		}
		if res.Status != "processed" {
			t.Errorf("expected status 'processed', got %s", res.Status)
		}
	})
}
