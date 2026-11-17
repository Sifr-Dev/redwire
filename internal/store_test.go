package internal

import (
	"fmt"
	"sync"
	"testing"
)

func TestSetGetDelete(t *testing.T) {
	s := NewStore()
	s.Set("k", "v")
	if v, ok := s.Get("k"); !ok || v != "v" {
		t.Fatal("get failed")
	}
	if !s.Delete("k") {
		t.Fatal("delete of present key returned false")
	}
	if _, ok := s.Get("k"); ok {
		t.Fatal("key still present after delete")
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			k := fmt.Sprintf("k%d", i%10)
			s.Set(k, fmt.Sprintf("v%d", i))
			s.Get(k)
			s.Keys()
		}(i)
	}
	wg.Wait()
}
