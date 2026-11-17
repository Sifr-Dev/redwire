package store

import (
	"fmt"
	"sync"
	"testing"
)

func TestGetSetDeleteExists(t *testing.T) {
	s := NewStore()
	if _, ok := s.Get("k"); ok {
		t.Fatal("expected miss on empty store")
	}
	s.Set("k", "v")
	if got, ok := s.Get("k"); !ok || got != "v" {
		t.Fatalf("got (%q, %v), want (v, true)", got, ok)
	}
	if !s.Exists("k") {
		t.Fatal("expected Exists true")
	}
	if !s.Delete("k") {
		t.Fatal("expected Delete true")
	}
	if s.Delete("k") {
		t.Fatal("expected Delete false after delete")
	}
	if s.Exists("k") {
		t.Fatal("expected Exists false after delete")
	}
	if s.Len() != 0 {
		t.Fatalf("Len = %d, want 0", s.Len())
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("key-%d", (id+i)%64)
				s.Set(key, fmt.Sprintf("value-%d-%d", id, i))
				s.Get(key)
				if i%3 == 0 {
					s.Delete(key)
				}
				s.Exists(key)
			}
		}(g)
	}
	wg.Wait()
	_ = s.Keys()
	_ = s.Len()
}
