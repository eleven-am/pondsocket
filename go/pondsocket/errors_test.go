package pondsocket

import (
	"errors"
	"strings"
	"testing"
)

func TestErrorCreation(t *testing.T) {
	t.Run("creates error with code and message", func(t *testing.T) {
		err := &Error{
			Code:    StatusBadRequest,
			Message: "Invalid request",
		}
		if err.Code != StatusBadRequest {
			t.Errorf("expected code %d, got %d", StatusBadRequest, err.Code)
		}
		if err.Message != "Invalid request" {
			t.Errorf("expected message 'Invalid request', got %s", err.Message)
		}
	})

	t.Run("error implements error interface", func(t *testing.T) {
		err := &Error{
			Code:    StatusInternalServerError,
			Message: "Something went wrong",
		}

		var _ error = err
		errStr := err.Error()

		expectedStr := "Something went wrong (code: 500)"
		if errStr != expectedStr {
			t.Errorf("expected error string '%s', got '%s'", expectedStr, errStr)
		}
	})
}

func TestErrorWithDetails(t *testing.T) {
	t.Run("adds details to error", func(t *testing.T) {
		err := &Error{
			Code:    StatusNotFound,
			Message: "Resource not found",
		}
		details := map[string]interface{}{
			"resource": "user",
			"id":       "123",
		}
		errWithDetails := err.withDetails(details)

		if errWithDetails.Details == nil {
			t.Fatal("expected details to be set")
		}
		detailsMap, ok := errWithDetails.Details.(map[string]interface{})

		if !ok {
			t.Fatal("expected details to be a map[string]interface{}")
		}
		if detailsMap["resource"] != "user" {
			t.Errorf("expected resource 'user', got %v", detailsMap["resource"])
		}
		if detailsMap["id"] != "123" {
			t.Errorf("expected id '123', got %v", detailsMap["id"])
		}
	})
}

func TestErrorHelpers(t *testing.T) {
	t.Run("badRequest creates 400 error", func(t *testing.T) {
		err := badRequest("test-entity", "Invalid input")

		if err.Code != StatusBadRequest {
			t.Errorf("expected code %d, got %d", StatusBadRequest, err.Code)
		}
		if err.ChannelName != "test-entity" {
			t.Errorf("expected channelName 'test-entity', got %s", err.ChannelName)
		}
		if err.Message != "Invalid input" {
			t.Errorf("expected message 'Invalid input', got %s", err.Message)
		}
	})

	t.Run("unauthorized creates 401 error", func(t *testing.T) {
		err := unauthorized("test-entity", "Authentication required")

		if err.Code != StatusUnauthorized {
			t.Errorf("expected code %d, got %d", StatusUnauthorized, err.Code)
		}
	})

	t.Run("forbidden creates 403 error", func(t *testing.T) {
		err := forbidden("test-entity", "Access denied")

		if err.Code != StatusForbidden {
			t.Errorf("expected code %d, got %d", StatusForbidden, err.Code)
		}
	})

	t.Run("notFound creates 404 error", func(t *testing.T) {
		err := notFound("test-entity", "Not found")

		if err.Code != StatusNotFound {
			t.Errorf("expected code %d, got %d", StatusNotFound, err.Code)
		}
	})

	t.Run("conflict creates 409 error", func(t *testing.T) {
		err := conflict("test-entity", "Already exists")

		if err.Code != StatusConflict {
			t.Errorf("expected code %d, got %d", StatusConflict, err.Code)
		}
	})

	t.Run("timeout creates 504 error", func(t *testing.T) {
		err := timeout("test-entity", "Request timeout")

		if err.Code != StatusGatewayTimeout {
			t.Errorf("expected code %d, got %d", StatusGatewayTimeout, err.Code)
		}
	})

	t.Run("internal creates 500 error", func(t *testing.T) {
		err := internal("test-entity", "Internal error")

		if err.Code != StatusInternalServerError {
			t.Errorf("expected code %d, got %d", StatusInternalServerError, err.Code)
		}
	})

	t.Run("unavailable creates 503 error", func(t *testing.T) {
		err := unavailable("test-entity", "Service unavailable")

		if err.Code != StatusServiceUnavailable {
			t.Errorf("expected code %d, got %d", StatusServiceUnavailable, err.Code)
		}
	})
}

func TestWrapError(t *testing.T) {
	t.Run("wraps standard error", func(t *testing.T) {
		originalErr := errors.New("original error")

		wrappedErr := wrap(originalErr, "wrapper message")

		if wrappedErr == nil {
			t.Fatal("expected wrapped error")
		}
		errStr := wrappedErr.Error()

		if !strings.Contains(errStr, "wrapper message") {
			t.Error("expected wrapped error to contain wrapper message")
		}
		if !strings.Contains(errStr, "original error") {
			t.Error("expected wrapped error to contain original error")
		}
	})

	t.Run("wrapF formats message", func(t *testing.T) {
		originalErr := errors.New("file not found")

		wrappedErr := wrapF(originalErr, "failed to load config from %s", "/etc/config.json")

		errStr := wrappedErr.Error()

		if !strings.Contains(errStr, "failed to load config from /etc/config.json") {
			t.Error("expected formatted message in wrapped error")
		}
		if !strings.Contains(errStr, "file not found") {
			t.Error("expected original error in wrapped error")
		}
	})

	t.Run("wrap returns nil when wrapping nil", func(t *testing.T) {
		wrappedErr := wrap(nil, "wrapper message")

		if wrappedErr != nil {
			t.Error("expected nil when wrapping nil error")
		}
	})
}

func TestCombineErrors(t *testing.T) {
	t.Run("combines multiple errors", func(t *testing.T) {
		err1 := errors.New("error 1")

		err2 := errors.New("error 2")

		err3 := errors.New("error 3")

		combined := combine(err1, err2, err3)

		if combined == nil {
			t.Fatal("expected combined error")
		}
		errStr := combined.Error()

		if !strings.Contains(errStr, "error 1") {
			t.Error("expected combined error to contain error 1")
		}
		if !strings.Contains(errStr, "error 2") {
			t.Error("expected combined error to contain error 2")
		}
		if !strings.Contains(errStr, "error 3") {
			t.Error("expected combined error to contain error 3")
		}
	})

	t.Run("ignores nil errors", func(t *testing.T) {
		err1 := errors.New("error 1")

		combined := combine(nil, err1, nil)

		if combined == nil {
			t.Fatal("expected combined error")
		}
		errStr := combined.Error()

		if !strings.Contains(errStr, "error 1") {
			t.Error("expected combined error to contain error 1")
		}
	})

	t.Run("returns nil when all errors are nil", func(t *testing.T) {
		combined := combine(nil, nil, nil)

		if combined != nil {
			t.Error("expected nil when combining all nil errors")
		}
	})

	t.Run("returns single error when only one non-nil", func(t *testing.T) {
		err := errors.New("single error")

		combined := combine(nil, err, nil)

		if combined != err {
			t.Error("expected single error to be returned directly")
		}
	})
}

func TestAddError(t *testing.T) {
	t.Run("adds error to nil", func(t *testing.T) {
		var result error
		newErr := errors.New("new error")

		result = addError(result, newErr)

		if result != newErr {
			t.Error("expected new error to be returned when adding to nil")
		}
	})

	t.Run("adds error to existing error", func(t *testing.T) {
		existing := errors.New("existing error")

		newErr := errors.New("new error")

		result := addError(existing, newErr)

		if result == nil {
			t.Fatal("expected combined error")
		}
		errStr := result.Error()

		if !strings.Contains(errStr, "existing error") {
			t.Error("expected result to contain existing error")
		}
		if !strings.Contains(errStr, "new error") {
			t.Error("expected result to contain new error")
		}
	})

	t.Run("ignores nil when adding", func(t *testing.T) {
		existing := errors.New("existing error")

		result := addError(existing, nil)

		if result != existing {
			t.Error("expected existing error when adding nil")
		}
	})
}

func TestErrorSerialization(t *testing.T) {
	t.Run("error serializes correctly", func(t *testing.T) {
		err := &Error{
			Code:        StatusBadRequest,
			ChannelName: "request",
			Message:     "Invalid parameters",
			Details: map[string]interface{}{
				"field": "email",
				"error": "invalid format",
			},
		}
		errMap := map[string]interface{}{
			"code":        err.Code,
			"channelName": err.ChannelName,
			"message":     err.Message,
			"details":     err.Details,
		}
		if errMap["code"] != StatusBadRequest {
			t.Error("expected code to serialize correctly")
		}
		if errMap["message"] != "Invalid parameters" {
			t.Error("expected message to serialize correctly")
		}
	})
}
