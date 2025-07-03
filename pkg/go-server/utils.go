package main

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
