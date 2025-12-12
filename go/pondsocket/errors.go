package pondsocket

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func (e *Error) Error() string {
	if e.ChannelName != "" {
		return fmt.Sprintf("Error in Channel %s: %s (code: %d)", e.ChannelName, e.Message, e.Code)
	}
	return fmt.Sprintf("%s (code: %d)", e.Message, e.Code)
}

func (e *Error) Unwrap() error {
	return e.cause
}

func (e *Error) withCause(err error) *Error {
	e.cause = err
	return e
}

func (e *Error) withDetails(details interface{}) *Error {
	e.Details = details
	return e
}

func (e *Error) withChannelName(channelName string) *Error {
	e.ChannelName = channelName
	return e
}

func wrap(err error, message string) *Error {
	if err == nil {
		return nil
	}

	var e *Error
	if errors.As(err, &e) {
		return &Error{
			ChannelName: e.ChannelName,
			Message:     fmt.Sprintf("%s: %s", message, e.Message),
			Code:        e.Code,
			Temporary:   e.Temporary,
			Details:     e.Details,
			cause:       e.cause,
		}
	}
	return &Error{
		Message: fmt.Sprintf("%s: %s", message, err),
		Code:    StatusInternalServerError,
		cause:   err,
	}
}

func wrapF(err error, format string, args ...interface{}) *Error {
	if err == nil {
		return nil
	}
	return wrap(err, fmt.Sprintf(format, args...))
}

func badRequest(channelName, message string) *Error {
	return &Error{
		Message:     message,
		Code:        StatusBadRequest,
		ChannelName: channelName,
		Temporary:   false,
	}
}

func notFound(channelName, message string) *Error {
	return &Error{
		Message:     message,
		Code:        StatusNotFound,
		ChannelName: channelName,
		Temporary:   false,
	}
}

func conflict(channelName, message string) *Error {
	return &Error{
		Message:     message,
		Code:        StatusConflict,
		ChannelName: channelName,
		Temporary:   false,
	}
}

func unauthorized(channelName, message string) *Error {
	return &Error{
		Message:     message,
		Code:        StatusUnauthorized,
		ChannelName: channelName,
		Temporary:   false,
	}
}

func forbidden(channelName, message string) *Error {
	return &Error{
		Message:     message,
		Code:        StatusForbidden,
		ChannelName: channelName,
		Temporary:   false,
	}
}

func internal(channelName, message string) *Error {
	return &Error{
		Message:     message,
		Code:        StatusInternalServerError,
		ChannelName: channelName,
		Temporary:   false,
	}
}

func temporary(channelName, message string, code int) *Error {
	return &Error{
		Message:     message,
		Code:        code,
		ChannelName: channelName,
		Temporary:   true,
	}
}

func unavailable(channelName, message string) *Error {
	return &Error{
		Message:     message,
		Code:        StatusServiceUnavailable,
		ChannelName: channelName,
		Temporary:   true,
	}
}

func timeout(channelName, message string) *Error {
	return &Error{
		Message:     message,
		Code:        StatusGatewayTimeout,
		ChannelName: channelName,
		Temporary:   true,
	}
}

type MultiError struct {
	errors []error
}

func (m *MultiError) Error() string {
	if len(m.errors) == 0 {
		return "no errors"
	}
	messages := make([]string, len(m.errors))

	for i, err := range m.errors {
		messages[i] = err.Error()
	}
	return strings.Join(messages, "; ")
}

func (m *MultiError) Unwrap() []error {
	return m.errors
}

func combine(errs ...error) error {

	var nonNil []error
	for _, err := range errs {
		if err != nil {
			nonNil = append(nonNil, err)
		}
	}
	if len(nonNil) == 0 {
		return nil
	}
	if len(nonNil) == 1 {
		return nonNil[0]
	}
	return &MultiError{errors: nonNil}
}

func addError(base, new error) error {
	if base == nil {
		return new
	}
	if new == nil {
		return base
	}

	var me *MultiError
	if errors.As(base, &me) {
		me.errors = append(me.errors, new)

		return me
	}
	return &MultiError{errors: []error{base, new}}
}

func errorEvent(err error) *Event {
	if err == nil {
		return nil
	}

	var e *Error
	if errors.As(err, &e) {
		message := e.Message
		if e.cause != nil {
			message = e.cause.Error()
		}
		return &Event{
			Action:      system,
			ChannelName: e.ChannelName,
			RequestId:   uuid.NewString(),
			Event:       string(internalErrorEvent),
			Payload: map[string]interface{}{
				"code":      e.Code,
				"details":   e.Details,
				"temporary": e.Temporary,
				"message":   message,
			},
		}
	}

	return &Event{
		Action:      system,
		ChannelName: string(gatewayEntity),
		RequestId:   uuid.NewString(),
		Event:       string(internalErrorEvent),
		Payload: map[string]interface{}{
			"message": err.Error(),
		},
	}
}
