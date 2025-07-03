package main

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestStoreCreate(t *testing.T) {
	t.Run("creates new entry successfully", func(t *testing.T) {
		s := newStore[string]()

		err := s.Create("key1", "value1")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		val, err := s.Read("key1")

		if err != nil {
			t.Errorf("expected no error reading created value, got %v", err)
		}
		if val != "value1" {
			t.Errorf("expected value1, got %v", val)
		}
	})

	t.Run("returns error when key already exists", func(t *testing.T) {
		s := newStore[string]()

		_ = s.Create("key1", "value1")

		err := s.Create("key1", "value2")

		if err == nil {
			t.Error("expected error for duplicate key, got nil")
		}

		var pondErr *Error
		if !errors.As(err, &pondErr) || pondErr.Code != StatusConflict {
			t.Errorf("expected conflict error, got %v", err)
		}
	})
}

func TestStoreRead(t *testing.T) {
	t.Run("reads existing value successfully", func(t *testing.T) {
		s := newStore[int]()

		_ = s.Create("key1", 42)

		val, err := s.Read("key1")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if val != 42 {
			t.Errorf("expected 42, got %v", val)
		}
	})

	t.Run("returns error for non-existent key", func(t *testing.T) {
		s := newStore[string]()

		_, err := s.Read("nonexistent")

		if err == nil {
			t.Error("expected error for non-existent key, got nil")
		}

		var pondErr *Error
		if !errors.As(err, &pondErr) || pondErr.Code != StatusNotFound {
			t.Errorf("expected not found error, got %v", err)
		}
	})
}

func TestStoreUpdate(t *testing.T) {
	t.Run("updates existing value successfully", func(t *testing.T) {
		s := newStore[string]()

		_ = s.Create("key1", "value1")

		err := s.Update("key1", "value2")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		val, _ := s.Read("key1")

		if val != "value2" {
			t.Errorf("expected value2, got %v", val)
		}
	})

	t.Run("returns error for non-existent key", func(t *testing.T) {
		s := newStore[string]()

		err := s.Update("nonexistent", "value")

		if err == nil {
			t.Error("expected error for non-existent key, got nil")
		}

		var pondErr *Error
		if !errors.As(err, &pondErr) || pondErr.Code != StatusNotFound {
			t.Errorf("expected not found error, got %v", err)
		}
	})
}

func TestStoreDelete(t *testing.T) {
	t.Run("deletes existing key successfully", func(t *testing.T) {
		s := newStore[string]()

		_ = s.Create("key1", "value1")

		err := s.Delete("key1")

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		_, err = s.Read("key1")

		if err == nil {
			t.Error("expected error reading deleted key, got nil")
		}
	})

	t.Run("returns error for non-existent key", func(t *testing.T) {
		s := newStore[string]()

		err := s.Delete("nonexistent")

		if err == nil {
			t.Error("expected error for non-existent key, got nil")
		}

		var pondErr *Error
		if !errors.As(err, &pondErr) || pondErr.Code != StatusNotFound {
			t.Errorf("expected not found error, got %v", err)
		}
	})
}

func TestStoreList(t *testing.T) {
	s := newStore[string]()

	_ = s.Create("key1", "value1")

	_ = s.Create("key2", "value2")

	list := s.List()

	if len(list) != 2 {
		t.Errorf("expected 2 items, got %d", len(list))
	}
	if list["key1"] != "value1" {
		t.Errorf("expected value1 for key1, got %v", list["key1"])
	}
	if list["key2"] != "value2" {
		t.Errorf("expected value2 for key2, got %v", list["key2"])
	}
}

func TestStoreKeys(t *testing.T) {
	s := newStore[string]()

	_ = s.Create("key1", "value1")

	_ = s.Create("key2", "value2")

	keys := s.Keys()

	if keys.length() != 2 {
		t.Errorf("expected 2 keys, got %d", keys.length())
	}
	foundKey1, foundKey2 := false, false
	keys.forEach(func(key string) {
		if key == "key1" {
			foundKey1 = true
		}
		if key == "key2" {
			foundKey2 = true
		}
	})

	if !foundKey1 || !foundKey2 {
		t.Error("expected both key1 and key2 to be present")
	}
}

func TestStoreValues(t *testing.T) {
	s := newStore[string]()

	_ = s.Create("key1", "value1")

	_ = s.Create("key2", "value2")

	values := s.Values()

	if values.length() != 2 {
		t.Errorf("expected 2 values, got %d", values.length())
	}
	foundValue1, foundValue2 := false, false
	values.forEach(func(val string) {
		if val == "value1" {
			foundValue1 = true
		}
		if val == "value2" {
			foundValue2 = true
		}
	})

	if !foundValue1 || !foundValue2 {
		t.Error("expected both value1 and value2 to be present")
	}
}

func TestStoreGetByKeys(t *testing.T) {
	s := newStore[string]()

	_ = s.Create("key1", "value1")

	_ = s.Create("key2", "value2")

	_ = s.Create("key3", "value3")

	values := s.GetByKeys("key1", "key3", "nonexistent")

	if values.length() != 2 {
		t.Errorf("expected 2 values, got %d", values.length())
	}
	foundValue1, foundValue3 := false, false
	values.forEach(func(val string) {
		if val == "value1" {
			foundValue1 = true
		}
		if val == "value3" {
			foundValue3 = true
		}
	})

	if !foundValue1 || !foundValue3 {
		t.Error("expected value1 and value3 to be present")
	}
}

func TestStoreLen(t *testing.T) {
	s := newStore[string]()

	if s.Len() != 0 {
		t.Errorf("expected empty store to have length 0, got %d", s.Len())
	}
	_ = s.Create("key1", "value1")

	if s.Len() != 1 {
		t.Errorf("expected store with 1 item to have length 1, got %d", s.Len())
	}
	_ = s.Create("key2", "value2")

	if s.Len() != 2 {
		t.Errorf("expected store with 2 items to have length 2, got %d", s.Len())
	}
	_ = s.Delete("key1")

	if s.Len() != 1 {
		t.Errorf("expected store after deletion to have length 1, got %d", s.Len())
	}
}

func TestStoreConcurrency(t *testing.T) {
	s := newStore[int]()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			key := fmt.Sprintf("key%d", n)

			_ = s.Create(key, n)
		}(i)
	}
	wg.Wait()

	if s.Len() != 100 {
		t.Errorf("expected 100 items after concurrent writes, got %d", s.Len())
	}
	for i := 0; i < 100; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			key := fmt.Sprintf("key%d", n)

			val, err := s.Read(key)

			if err != nil {
				t.Errorf("error reading key %s: %v", key, err)
			}
			if val != n {
				t.Errorf("expected value %d for key %s, got %d", n, key, val)
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < 100; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			key := fmt.Sprintf("key%d", n)

			_ = s.Update(key, n*2)
		}(i)
	}
	wg.Wait()

	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key%d", i)

		val, _ := s.Read(key)

		if val != i*2 {
			t.Errorf("expected value %d for key %s after update, got %d", i*2, key, val)
		}
	}
}
