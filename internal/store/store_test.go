package store

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"pack-calculator/internal/packing"
)

func TestStore_InMemory(t *testing.T) {
	s, err := New([]int{500, 250}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Get(); !reflect.DeepEqual(got, []int{250, 500}) {
		t.Fatalf("Get() = %v", got)
	}

	got, err := s.Set([]int{53, 23, 31, 23})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{23, 31, 53}; !reflect.DeepEqual(got, want) || !reflect.DeepEqual(s.Get(), want) {
		t.Fatalf("Set() = %v, Get() = %v, want %v", got, s.Get(), want)
	}

	// Returned slices are copies.
	got[0] = 999
	if s.Get()[0] != 23 {
		t.Fatal("store state was mutated through returned slice")
	}
}

func TestStore_RejectsInvalid(t *testing.T) {
	if _, err := New(nil, ""); err == nil {
		t.Fatal("expected error for empty defaults")
	}
	s, _ := New([]int{250}, "")
	if _, err := s.Set([]int{0}); !errors.Is(err, packing.ErrInvalidPackSize) {
		t.Fatalf("got %v, want ErrInvalidPackSize", err)
	}
	if got := s.Get(); !reflect.DeepEqual(got, []int{250}) {
		t.Fatalf("invalid Set changed state: %v", got)
	}
}

func TestStore_Persistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "packs.json")

	s, err := New([]int{250, 500}, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set([]int{10, 20}); err != nil {
		t.Fatal(err)
	}

	// A new store reading the same file sees the saved sizes, not defaults.
	s2, err := New([]int{250, 500}, path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get(); !reflect.DeepEqual(got, []int{10, 20}) {
		t.Fatalf("reloaded sizes = %v", got)
	}
}

func TestStore_CorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "packs.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New([]int{250}, path); err == nil {
		t.Fatal("expected error for corrupt file")
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s, _ := New([]int{250}, "")
	var wg sync.WaitGroup
	for i := 1; i <= 50; i++ {
		wg.Add(2)
		go func(n int) { defer wg.Done(); _, _ = s.Set([]int{n, n * 2}) }(i)
		go func() { defer wg.Done(); _ = s.Get() }()
	}
	wg.Wait()
}
