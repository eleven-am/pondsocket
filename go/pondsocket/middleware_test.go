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

func TestMiddlewareCompose(t *testing.T) {
	t.Run("composes multiple middlewares", func(t *testing.T) {
		mw1 := newMiddleWare[string, string]()
		mw2 := newMiddleWare[string, string]()

		order := make([]int, 0)

		mw1.Use(func(ctx context.Context, req string, res string, next nextFunc) error {
			order = append(order, 1)
			return next()
		})

		mw2.Use(func(ctx context.Context, req string, res string, next nextFunc) error {
			order = append(order, 2)
			return next()
		})

		composed := mw1.Compose(mw2)

		err := composed.Handle(context.Background(), "test", "response", func(req string, res string) error {
			order = append(order, 3)
			return nil
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		expectedOrder := []int{1, 2, 3}
		if len(order) != len(expectedOrder) {
			t.Fatalf("expected order length %d, got %d", len(expectedOrder), len(order))
		}
		for i, v := range order {
			if v != expectedOrder[i] {
				t.Errorf("expected order[%d] = %d, got %d", i, expectedOrder[i], v)
			}
		}
	})

	t.Run("composes empty middlewares", func(t *testing.T) {
		mw1 := newMiddleWare[string, string]()
		mw2 := newMiddleWare[string, string]()

		composed := mw1.Compose(mw2)

		called := false
		err := composed.Handle(context.Background(), "test", "response", func(req string, res string) error {
			called = true
			return nil
		})

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if !called {
			t.Error("expected final handler to be called")
		}
	})

	t.Run("composes three middlewares", func(t *testing.T) {
		mw1 := newMiddleWare[int, int]()
		mw2 := newMiddleWare[int, int]()
		mw3 := newMiddleWare[int, int]()

		order := make([]int, 0)

		mw1.Use(func(ctx context.Context, req int, res int, next nextFunc) error {
			order = append(order, 1)
			return next()
		})

		mw2.Use(func(ctx context.Context, req int, res int, next nextFunc) error {
			order = append(order, 2)
			return next()
		})

		mw3.Use(func(ctx context.Context, req int, res int, next nextFunc) error {
			order = append(order, 3)
			return next()
		})

		composed := mw1.Compose(mw2, mw3)

		err := composed.Handle(context.Background(), 42, 0, func(req int, res int) error {
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
	})

	t.Run("composed middleware is independent", func(t *testing.T) {
		mw1 := newMiddleWare[string, string]()

		mw1.Use(func(ctx context.Context, req string, res string, next nextFunc) error {
			return next()
		})

		composed := mw1.Compose()

		mw1.Use(func(ctx context.Context, req string, res string, next nextFunc) error {
			return errors.New("should not be called")
		})

		err := composed.Handle(context.Background(), "test", "response", func(req string, res string) error {
			return nil
		})

		if err != nil {
			t.Errorf("expected no error from composed, got %v", err)
		}
	})
}

type testCtx struct {
	Value string
}

func TestExecuteWithMiddleware(t *testing.T) {
	t.Run("no middleware calls handler directly", func(t *testing.T) {
		ctx := &testCtx{Value: "original"}
		called := false
		err := executeWithMiddleware(ctx, func(c *testCtx) error {
			called = true
			return nil
		}, nil)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if !called {
			t.Error("expected handler to be called")
		}
	})

	t.Run("single middleware runs before handler", func(t *testing.T) {
		ctx := &testCtx{Value: "original"}
		order := make([]int, 0)
		mws := []MiddlewareFunc[testCtx]{
			func(c *testCtx, next func() error) error {
				order = append(order, 1)
				return next()
			},
		}
		err := executeWithMiddleware(ctx, func(c *testCtx) error {
			order = append(order, 2)
			return nil
		}, mws)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(order) != 2 || order[0] != 1 || order[1] != 2 {
			t.Errorf("expected order [1 2], got %v", order)
		}
	})

	t.Run("multiple middleware execute in order", func(t *testing.T) {
		ctx := &testCtx{}
		order := make([]int, 0)
		mws := []MiddlewareFunc[testCtx]{
			func(c *testCtx, next func() error) error {
				order = append(order, 1)
				return next()
			},
			func(c *testCtx, next func() error) error {
				order = append(order, 2)
				return next()
			},
			func(c *testCtx, next func() error) error {
				order = append(order, 3)
				return next()
			},
		}
		err := executeWithMiddleware(ctx, func(c *testCtx) error {
			order = append(order, 4)
			return nil
		}, mws)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		expected := []int{1, 2, 3, 4}
		if len(order) != len(expected) {
			t.Fatalf("expected %d items, got %d", len(expected), len(order))
		}
		for i, v := range order {
			if v != expected[i] {
				t.Errorf("order[%d] = %d, want %d", i, v, expected[i])
			}
		}
	})

	t.Run("short-circuit stops chain", func(t *testing.T) {
		ctx := &testCtx{}
		handlerCalled := false
		secondCalled := false
		mws := []MiddlewareFunc[testCtx]{
			func(c *testCtx, next func() error) error {
				return nil
			},
			func(c *testCtx, next func() error) error {
				secondCalled = true
				return next()
			},
		}
		err := executeWithMiddleware(ctx, func(c *testCtx) error {
			handlerCalled = true
			return nil
		}, mws)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if secondCalled {
			t.Error("expected second middleware not to be called")
		}
		if handlerCalled {
			t.Error("expected handler not to be called")
		}
	})

	t.Run("error propagation from middleware", func(t *testing.T) {
		ctx := &testCtx{}
		testErr := errors.New("middleware error")
		mws := []MiddlewareFunc[testCtx]{
			func(c *testCtx, next func() error) error {
				return testErr
			},
		}
		err := executeWithMiddleware(ctx, func(c *testCtx) error {
			return nil
		}, mws)
		if err != testErr {
			t.Errorf("expected %v, got %v", testErr, err)
		}
	})

	t.Run("error propagation from handler", func(t *testing.T) {
		ctx := &testCtx{}
		testErr := errors.New("handler error")
		mws := []MiddlewareFunc[testCtx]{
			func(c *testCtx, next func() error) error {
				return next()
			},
		}
		err := executeWithMiddleware(ctx, func(c *testCtx) error {
			return testErr
		}, mws)
		if err != testErr {
			t.Errorf("expected %v, got %v", testErr, err)
		}
	})

	t.Run("middleware can modify context", func(t *testing.T) {
		ctx := &testCtx{Value: "original"}
		mws := []MiddlewareFunc[testCtx]{
			func(c *testCtx, next func() error) error {
				c.Value = "modified"
				return next()
			},
		}
		var handlerSaw string
		err := executeWithMiddleware(ctx, func(c *testCtx) error {
			handlerSaw = c.Value
			return nil
		}, mws)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if handlerSaw != "modified" {
			t.Errorf("expected handler to see 'modified', got '%s'", handlerSaw)
		}
	})

	t.Run("empty middleware slice calls handler directly", func(t *testing.T) {
		ctx := &testCtx{}
		called := false
		err := executeWithMiddleware(ctx, func(c *testCtx) error {
			called = true
			return nil
		}, []MiddlewareFunc[testCtx]{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if !called {
			t.Error("expected handler to be called")
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
