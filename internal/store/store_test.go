package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestGetSetDeleteExists(t *testing.T) {
	s := NewStore()
	if _, ok := s.Get("k"); ok {
		t.Fatal("expected miss on empty store")
	}
	if err := s.Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Get("k"); !ok || got != "v" {
		t.Fatalf("got (%q, %v), want (v, true)", got, ok)
	}
	if !s.Exists("k") {
		t.Fatal("expected Exists true")
	}
	if ok, err := s.Delete("k"); err != nil || !ok {
		t.Fatalf("Delete = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := s.Delete("k"); err != nil || ok {
		t.Fatalf("second Delete = (%v, %v), want (false, nil)", ok, err)
	}
	if s.Exists("k") || s.Len() != 0 || s.MemoryBytes() != 0 {
		t.Fatal("delete did not release key and memory accounting")
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
				if err := s.Set(key, fmt.Sprintf("value-%d-%d", id, i)); err != nil {
					t.Errorf("Set: %v", err)
					return
				}
				s.Get(key)
				if i%3 == 0 {
					if _, err := s.Delete(key); err != nil {
						t.Errorf("Delete: %v", err)
					}
				}
				s.Exists(key)
			}
		}(g)
	}
	wg.Wait()
	_ = s.Keys()
	_ = s.Len()
}

func TestHardLimitsAndReplacementAccounting(t *testing.T) {
	s := newStore(Limits{MaxKeys: 1, MaxMemoryBytes: 6}, nil)
	if err := s.Set("a", "123"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("b", "1"); !errors.Is(err, ErrKeyLimit) {
		t.Fatalf("key limit error = %v", err)
	}
	if err := s.Set("a", "123456"); !errors.Is(err, ErrMemoryLimit) {
		t.Fatalf("memory limit error = %v", err)
	}
	if err := s.Set("a", "12345"); err != nil {
		t.Fatal(err)
	}
	if got := s.MemoryBytes(); got != 6 {
		t.Fatalf("MemoryBytes = %d, want 6", got)
	}
}

func TestTTLUsesAbsoluteClock(t *testing.T) {
	now := time.Unix(100, 0)
	s := newStore(Limits{MaxKeys: 10, MaxMemoryBytes: 100}, nil)
	s.now = func() time.Time { return now }
	if err := s.SetTTL("k", "v", 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if ttl, ok := s.TTL("k"); !ok || ttl != 10*time.Second {
		t.Fatalf("TTL = (%v, %v)", ttl, ok)
	}
	now = now.Add(4 * time.Second)
	if ttl, ok := s.TTL("k"); !ok || ttl != 6*time.Second {
		t.Fatalf("TTL after 4s = (%v, %v)", ttl, ok)
	}
	now = now.Add(7 * time.Second)
	if _, ok := s.Get("k"); ok || s.Exists("k") || s.Len() != 0 || s.MemoryBytes() != 0 {
		t.Fatal("expired key was not removed from all views")
	}
	if _, ok := s.TTL("k"); ok {
		t.Fatal("expired key still has TTL")
	}
	if err := s.Set("forever", "v"); err != nil {
		t.Fatal(err)
	}
	if ttl, ok := s.TTL("forever"); !ok || ttl != -1 {
		t.Fatalf("persistent key TTL = (%v, %v)", ttl, ok)
	}
	if changed, err := s.Expire("forever", 0); err != nil || !changed {
		t.Fatalf("immediate expiry = (%v, %v)", changed, err)
	}
}

func TestSnapshotRecoveryAndAbsoluteTTL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path, Limits{MaxKeys: 10, MaxMemoryBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("binary\x00", "value\r\n\x00"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTTL("short", "lived", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	recovered, err := Open(path, Limits{MaxKeys: 10, MaxMemoryBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := recovered.Get("binary\x00"); !ok || got != "value\r\n\x00" {
		t.Fatalf("binary round trip = (%q, %v)", got, ok)
	}
	if ttl, ok := recovered.TTL("short"); !ok || ttl <= 0 || ttl > 2*time.Second {
		t.Fatalf("recovered TTL = (%v, %v), want original deadline", ttl, ok)
	}
	if ok, err := recovered.Delete("binary\x00"); err != nil || !ok {
		t.Fatalf("persistent delete = (%v, %v)", ok, err)
	}
	restarted, err := Open(path, Limits{MaxKeys: 10, MaxMemoryBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Exists("binary\x00") {
		t.Fatal("delete was not durable")
	}
}

func TestSnapshotRejectsCorruptionAndTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path, Limits{MaxKeys: 10, MaxMemoryBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("key", "value"); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, corrupt := range map[string][]byte{
		"truncated": contents[:len(contents)/2],
		"checksum":  append([]byte("x"), contents[1:]...),
		"trailing":  append(contents[:len(contents):len(contents)], []byte("{}")...),
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, corrupt, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(path, Limits{MaxKeys: 10, MaxMemoryBytes: 100}); err == nil {
				t.Fatal("corrupt snapshot must not be partially recovered")
			}
		})
	}
}

func TestSnapshotFileSyncFailureRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path, Limits{MaxKeys: 10, MaxMemoryBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected fsync failure")
	old := syncSnapshotFile
	syncSnapshotFile = func(*os.File) error { return injected }
	defer func() { syncSnapshotFile = old }()
	if err := s.Set("key", "value"); !errors.Is(err, injected) {
		t.Fatalf("Set error = %v, want injected failure", err)
	}
	if s.Exists("key") {
		t.Fatal("failed snapshot published in-memory mutation")
	}
	syncSnapshotFile = old
	if err := s.Set("old", "state"); err != nil {
		t.Fatal(err)
	}
	syncSnapshotFile = func(*os.File) error { return injected }
	if err := s.Set("key", "new"); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	if _, err := s.Delete("old"); !errors.Is(err, injected) {
		t.Fatalf("Delete error = %v", err)
	}
	syncSnapshotFile = old
	recovered, err := Open(path, Limits{MaxKeys: 10, MaxMemoryBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.Exists("old") || recovered.Exists("key") {
		t.Fatal("failed mutations changed the durable snapshot")
	}
}

func TestSnapshotDirectorySyncFailureIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	s, err := Open(path, Limits{MaxKeys: 10, MaxMemoryBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected directory fsync failure")
	old := syncSnapshotDir
	syncSnapshotDir = func(*os.File) error { return injected }
	defer func() { syncSnapshotDir = old }()
	if err := s.Set("key", "value"); !errors.Is(err, injected) {
		t.Fatalf("Set error = %v, want injected directory fsync failure", err)
	}
	if s.Exists("key") {
		t.Fatal("directory sync error acknowledged mutation")
	}
}
