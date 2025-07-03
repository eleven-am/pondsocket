package pondsocket

import (
	"sync"
)

type store[T any] struct {
	mutex sync.RWMutex
	store map[string]T
}

func newStore[T any]() *store[T] {
	return &store[T]{
		mutex: sync.RWMutex{},
		store: make(map[string]T),
	}
}

func (s *store[T]) Create(key string, value T) error {
	s.mutex.Lock()

	defer s.mutex.Unlock()

	if _, exists := s.store[key]; exists {
		return conflict(key, "Key already exists")
	}
	s.store[key] = value
	return nil
}

func (s *store[T]) Read(key string) (T, error) {
	s.mutex.RLock()

	defer s.mutex.RUnlock()

	var zeroValue T
	value, exists := s.store[key]
	if !exists {
		return zeroValue, notFound(key, "Key does not exist")
	}
	return value, nil
}

func (s *store[T]) Update(key string, value T) error {
	s.mutex.Lock()

	defer s.mutex.Unlock()

	if _, exists := s.store[key]; !exists {
		return notFound(key, "Key does not exist")
	}
	s.store[key] = value
	return nil
}

func (s *store[T]) Delete(key string) error {
	s.mutex.Lock()

	defer s.mutex.Unlock()

	if _, exists := s.store[key]; !exists {
		return notFound(key, "Key does not exist")
	}
	delete(s.store, key)

	return nil
}

func (s *store[T]) List() map[string]T {
	s.mutex.RLock()

	defer s.mutex.RUnlock()

	result := make(map[string]T)

	for key, value := range s.store {
		result[key] = value
	}
	return result
}

func (s *store[T]) Keys() *array[string] {
	s.mutex.RLock()

	defer s.mutex.RUnlock()

	keys := newArray[string]()

	for key := range s.store {
		keys.push(key)
	}
	return keys
}

func (s *store[T]) Values() *array[T] {
	s.mutex.RLock()

	defer s.mutex.RUnlock()

	values := newArray[T]()

	for _, value := range s.store {
		values.push(value)
	}
	return values
}

func (s *store[T]) GetByKeys(keys ...string) *array[T] {
	s.mutex.RLock()

	defer s.mutex.RUnlock()

	values := newArray[T]()

	for _, key := range keys {
		if value, exists := s.store[key]; exists {
			values.push(value)
		}
	}
	return values
}

func (s *store[T]) Len() int {
	s.mutex.RLock()

	defer s.mutex.RUnlock()

	return len(s.store)
}
