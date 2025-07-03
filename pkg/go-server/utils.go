package pondsocket

import (
	"context"
	"encoding/json"
)

func mergeContexts(parent context.Context, contexts ...context.Context) (context.Context, context.CancelFunc) {
	ctx, cancelCause := context.WithCancelCause(parent)

	for _, inputCtx := range contexts {
		go func(childCtx context.Context) {
			select {
			case <-ctx.Done():
				return
			case <-childCtx.Done():
				cancelCause(childCtx.Err())

				return
			}
		}(inputCtx)
	}
	simpleCancel := func() {
		cancelCause(context.Canceled)
	}
	return ctx, simpleCancel
}

func mapToError[T any](arr *array[T], fn func(T) error) error {
	errs := mapArray(arr, fn)

	return combine(errs.items...)
}

func parsePayload(v interface{}, payload interface{}) error {
	marshaled, err := json.Marshal(payload)

	if err != nil {
		return wrapF(err, "failed to marshal payload: %v", err)
	}
	err = json.Unmarshal(marshaled, v)

	if err != nil {
		return wrapF(err, "failed to unmarshal payload: %v", err)
	}
	return nil
}

// parseAssigns safely converts assigns data (map[string]interface{}) into a struct.
// This is useful for deserializing user assigns into typed structs.
// Returns an error if the assigns data cannot be parsed into the target type.
func parseAssigns(v interface{}, assigns map[string]interface{}) error {
	if assigns == nil {
		return wrapF(nil, "assigns data is nil")
	}

	marshaled, err := json.Marshal(assigns)
	if err != nil {
		return wrapF(err, "failed to marshal assigns: %v", err)
	}

	err = json.Unmarshal(marshaled, v)
	if err != nil {
		return wrapF(err, "failed to unmarshal assigns: %v", err)
	}

	return nil
}

// parsePresence safely converts presence data (interface{}) into a struct.
// This is useful for deserializing user presence into typed structs.
// Returns an error if the presence data cannot be parsed into the target type.
func parsePresence(v interface{}, presence interface{}) error {
	if presence == nil {
		return wrapF(nil, "presence data is nil")
	}

	marshaled, err := json.Marshal(presence)
	if err != nil {
		return wrapF(err, "failed to marshal presence: %v", err)
	}

	err = json.Unmarshal(marshaled, v)
	if err != nil {
		return wrapF(err, "failed to unmarshal presence: %v", err)
	}

	return nil
}
