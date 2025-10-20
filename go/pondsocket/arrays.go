package pondsocket

import "sync"

type array[T any] struct {
	items []T
	lock  sync.RWMutex
}

type arrayPredicate[T any] func(item T) bool

func newArray[T any]() *array[T] {
	return &array[T]{
		items: make([]T, 0),
	}
}

func (a *array[T]) findIndex(predicate arrayPredicate[T]) int {
	a.lock.RLock()
	defer a.lock.RUnlock()

	for i, item := range a.items {
		if predicate(item) {
			return i
		}
	}
	return -1
}

func (a *array[T]) find(predicate arrayPredicate[T]) *T {
	a.lock.RLock()
	defer a.lock.RUnlock()

	for i := range a.items {
		if predicate(a.items[i]) {
			return &a.items[i]
		}
	}
	return nil
}

func (a *array[T]) filter(predicate arrayPredicate[T]) *array[T] {
	a.lock.RLock()
	defer a.lock.RUnlock()

	result := newArray[T]()

	for _, item := range a.items {
		if predicate(item) {
			result.items = append(result.items, item)
		}
	}
	return result
}

func (a *array[T]) push(item T) {
	a.lock.Lock()
	defer a.lock.Unlock()

	a.items = append(a.items, item)
}

func (a *array[T]) some(predicate arrayPredicate[T]) bool {
	a.lock.RLock()
	defer a.lock.RUnlock()

	for _, item := range a.items {
		if predicate(item) {
			return true
		}
	}

	return false
}

func (a *array[T]) every(predicate arrayPredicate[T]) bool {
	a.lock.RLock()
	defer a.lock.RUnlock()

	for _, item := range a.items {
		if !predicate(item) {
			return false
		}
	}
	return true
}

func (a *array[T]) length() int {
	a.lock.RLock()
	defer a.lock.RUnlock()
	return len(a.items)
}

func (a *array[T]) forEach(fn func(T)) {
	a.lock.RLock()
	defer a.lock.RUnlock()
	for _, item := range a.items {
		fn(item)
	}
}

func fromSlice[T any](slice []T) *array[T] {
	clone := make([]T, len(slice))
	copy(clone, slice)
	return &array[T]{items: clone}
}

func mapArray[T, R any](arr *array[T], fn func(T) R) *array[R] {
	result := newArray[R]()
	arr.forEach(func(item T) {
		result.push(fn(item))
	})
	return result
}
