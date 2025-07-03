package main

import (
	"fmt"
	"sync"
	"testing"
)

func TestArrayCreation(t *testing.T) {
	t.Run("creates empty array", func(t *testing.T) {
		arr := newArray[int]()

		if arr == nil {
			t.Fatal("expected array to be created")
		}
		if arr.length() != 0 {
			t.Errorf("expected length 0, got %d", arr.length())
		}
		if arr.items == nil {
			t.Error("expected items slice to be initialized")
		}
	})

	t.Run("creates array of different types", func(t *testing.T) {
		intArr := newArray[int]()

		strArr := newArray[string]()

		structArr := newArray[struct{ ID int }]()

		if intArr == nil || strArr == nil || structArr == nil {
			t.Error("expected all arrays to be created")
		}
	})
}

func TestArrayPush(t *testing.T) {
	t.Run("pushes single item", func(t *testing.T) {
		arr := newArray[string]()

		arr.push("hello")

		if arr.length() != 1 {
			t.Errorf("expected length 1, got %d", arr.length())
		}
		if arr.items[0] != "hello" {
			t.Errorf("expected 'hello', got %s", arr.items[0])
		}
	})

	t.Run("pushes multiple items", func(t *testing.T) {
		arr := newArray[int]()

		for i := 0; i < 5; i++ {
			arr.push(i)
		}
		if arr.length() != 5 {
			t.Errorf("expected length 5, got %d", arr.length())
		}
		for i := 0; i < 5; i++ {
			if arr.items[i] != i {
				t.Errorf("expected items[%d] = %d, got %d", i, i, arr.items[i])
			}
		}
	})
}

func TestArrayFind(t *testing.T) {
	t.Run("finds existing item", func(t *testing.T) {
		arr := newArray[int]()

		arr.push(10)

		arr.push(20)

		arr.push(30)

		result := arr.find(func(item int) bool {
			return item == 20
		})

		if result == nil {
			t.Fatal("expected to find item")
		}
		if *result != 20 {
			t.Errorf("expected 20, got %d", *result)
		}
	})

	t.Run("returns nil when not found", func(t *testing.T) {
		arr := newArray[string]()

		arr.push("apple")

		arr.push("banana")

		result := arr.find(func(item string) bool {
			return item == "orange"
		})

		if result != nil {
			t.Error("expected nil when item not found")
		}
	})

	t.Run("finds first matching item", func(t *testing.T) {
		arr := newArray[int]()

		arr.push(1)

		arr.push(2)

		arr.push(2)

		arr.push(3)

		result := arr.find(func(item int) bool {
			return item == 2
		})

		if result == nil || *result != 2 {
			t.Error("expected to find first occurrence of 2")
		}
	})
}

func TestArrayFindIndex(t *testing.T) {
	t.Run("finds index of existing item", func(t *testing.T) {
		arr := newArray[string]()

		arr.push("a")

		arr.push("b")

		arr.push("c")

		index := arr.findIndex(func(item string) bool {
			return item == "b"
		})

		if index != 1 {
			t.Errorf("expected index 1, got %d", index)
		}
	})

	t.Run("returns -1 when not found", func(t *testing.T) {
		arr := newArray[int]()

		arr.push(1)

		arr.push(2)

		arr.push(3)

		index := arr.findIndex(func(item int) bool {
			return item == 4
		})

		if index != -1 {
			t.Errorf("expected -1, got %d", index)
		}
	})
}

func TestArrayFilter(t *testing.T) {
	t.Run("filters items based on predicate", func(t *testing.T) {
		arr := newArray[int]()

		for i := 1; i <= 10; i++ {
			arr.push(i)
		}
		filtered := arr.filter(func(item int) bool {
			return item%2 == 0
		})

		if filtered.length() != 5 {
			t.Errorf("expected 5 even numbers, got %d", filtered.length())
		}
		for i := 0; i < filtered.length(); i++ {
			if filtered.items[i]%2 != 0 {
				t.Errorf("expected even number, got %d", filtered.items[i])
			}
		}
	})

	t.Run("returns empty array when no matches", func(t *testing.T) {
		arr := newArray[string]()

		arr.push("apple")

		arr.push("banana")

		filtered := arr.filter(func(item string) bool {
			return item == "orange"
		})

		if filtered.length() != 0 {
			t.Errorf("expected empty array, got length %d", filtered.length())
		}
	})

	t.Run("returns new array instance", func(t *testing.T) {
		arr := newArray[int]()

		arr.push(1)

		filtered := arr.filter(func(item int) bool {
			return true
		})

		if &arr.items == &filtered.items {
			t.Error("expected filter to return new array instance")
		}
	})
}

func TestArraySome(t *testing.T) {
	t.Run("returns true when at least one match", func(t *testing.T) {
		arr := newArray[int]()

		arr.push(1)

		arr.push(2)

		arr.push(3)

		result := arr.some(func(item int) bool {
			return item > 2
		})

		if !result {
			t.Error("expected true when item > 2 exists")
		}
	})

	t.Run("returns false when no matches", func(t *testing.T) {
		arr := newArray[string]()

		arr.push("a")

		arr.push("b")

		result := arr.some(func(item string) bool {
			return item == "c"
		})

		if result {
			t.Error("expected false when no matches")
		}
	})

	t.Run("stops early when match found", func(t *testing.T) {
		arr := newArray[int]()

		for i := 0; i < 100; i++ {
			arr.push(i)
		}
		calls := 0
		arr.some(func(item int) bool {
			calls++
			return item == 5
		})

		if calls > 10 {
			t.Errorf("expected early termination, but had %d calls", calls)
		}
	})
}

func TestArrayEvery(t *testing.T) {
	t.Run("returns true when all match", func(t *testing.T) {
		arr := newArray[int]()

		arr.push(2)

		arr.push(4)

		arr.push(6)

		result := arr.every(func(item int) bool {
			return item%2 == 0
		})

		if !result {
			t.Error("expected true when all items are even")
		}
	})

	t.Run("returns false when one doesn't match", func(t *testing.T) {
		arr := newArray[int]()

		arr.push(2)

		arr.push(3)

		arr.push(4)

		result := arr.every(func(item int) bool {
			return item%2 == 0
		})

		if result {
			t.Error("expected false when not all items are even")
		}
	})

	t.Run("returns true for empty array", func(t *testing.T) {
		arr := newArray[int]()

		result := arr.every(func(item int) bool {
			return false
		})

		if !result {
			t.Error("expected true for empty array")
		}
	})
}

func TestArrayForEach(t *testing.T) {
	t.Run("iterates over all items", func(t *testing.T) {
		arr := newArray[int]()

		for i := 0; i < 5; i++ {
			arr.push(i)
		}
		sum := 0
		arr.forEach(func(item int) {
			sum += item
		})

		if sum != 10 {
			t.Errorf("expected sum 10, got %d", sum)
		}
	})

	t.Run("handles empty array", func(t *testing.T) {
		arr := newArray[string]()

		called := false
		arr.forEach(func(item string) {
			called = true
		})

		if called {
			t.Error("expected forEach not to be called on empty array")
		}
	})
}

func TestFromSlice(t *testing.T) {
	t.Run("creates array from slice", func(t *testing.T) {
		slice := []string{"a", "b", "c"}
		arr := fromSlice(slice)

		if arr.length() != 3 {
			t.Errorf("expected length 3, got %d", arr.length())
		}
		for i, v := range slice {
			if arr.items[i] != v {
				t.Errorf("expected items[%d] = %s, got %s", i, v, arr.items[i])
			}
		}
	})

	t.Run("creates independent copy", func(t *testing.T) {
		slice := []int{1, 2, 3}
		arr := fromSlice(slice)

		slice[0] = 99
		if arr.items[0] == 99 {
			t.Error("expected array to be independent of original slice")
		}
	})

	t.Run("handles empty slice", func(t *testing.T) {
		slice := []int{}
		arr := fromSlice(slice)

		if arr.length() != 0 {
			t.Errorf("expected empty array, got length %d", arr.length())
		}
	})
}

func TestMapArray(t *testing.T) {
	t.Run("maps values to new type", func(t *testing.T) {
		arr := newArray[int]()

		arr.push(1)

		arr.push(2)

		arr.push(3)

		strArr := mapArray(arr, func(n int) string {
			return fmt.Sprintf("num-%d", n)
		})

		if strArr.length() != 3 {
			t.Errorf("expected length 3, got %d", strArr.length())
		}
		expected := []string{"num-1", "num-2", "num-3"}
		for i, v := range expected {
			if strArr.items[i] != v {
				t.Errorf("expected items[%d] = %s, got %s", i, v, strArr.items[i])
			}
		}
	})

	t.Run("handles empty array", func(t *testing.T) {
		arr := newArray[int]()

		result := mapArray(arr, func(n int) string {
			return "should not be called"
		})

		if result.length() != 0 {
			t.Errorf("expected empty result, got length %d", result.length())
		}
	})
}

func TestArrayConcurrency(t *testing.T) {
	t.Run("concurrent push operations", func(t *testing.T) {
		arr := newArray[int]()

		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)

			go func(start int) {
				defer wg.Done()

				for j := 0; j < 100; j++ {
					arr.push(start*100 + j)
				}
			}(i)
		}
		wg.Wait()

		if arr.length() != 1000 {
			t.Errorf("expected 1000 items, got %d", arr.length())
		}
	})

	t.Run("concurrent read operations", func(t *testing.T) {
		arr := newArray[string]()

		for i := 0; i < 100; i++ {
			arr.push(fmt.Sprintf("item-%d", i))
		}

		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)

			go func() {
				defer wg.Done()

				_ = arr.length()

				_ = arr.find(func(s string) bool {
					return s == "item-50"
				})

				_ = arr.some(func(s string) bool {
					return s == "item-99"
				})
			}()
		}
		wg.Wait()
	})

	t.Run("concurrent mixed operations", func(t *testing.T) {
		arr := newArray[int]()

		var wg sync.WaitGroup
		for i := 0; i < 5; i++ {
			wg.Add(1)

			go func(n int) {
				defer wg.Done()

				for j := 0; j < 20; j++ {
					arr.push(n*20 + j)
				}
			}(i)
		}
		for i := 0; i < 5; i++ {
			wg.Add(1)

			go func() {
				defer wg.Done()

				for j := 0; j < 10; j++ {
					_ = arr.length()

					_ = arr.filter(func(n int) bool {
						return n%2 == 0
					})
				}
			}()
		}
		wg.Wait()

		if arr.length() == 0 {
			t.Error("expected items to be added")
		}
	})
}
