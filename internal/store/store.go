// Package store implements the bounded, concurrency-safe string store.
package store

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	DefaultMaxKeys        = 10000
	DefaultMaxMemoryBytes = int64(64 << 20)
)

var (
	ErrKeyLimit    = errors.New("store: key limit exceeded")
	ErrMemoryLimit = errors.New("store: memory limit exceeded")
)

// Limits are hard bounds. Bytes counts key and value bytes, not Go/runtime or
// allocator overhead. A complete command is separately bounded by RESP.
type Limits struct {
	MaxKeys        int
	MaxMemoryBytes int64
}

type entry struct {
	value     string
	expiresAt int64 // Unix milliseconds; zero means no expiry.
}

type snapshotEntry struct {
	Key       []byte `json:"key"`
	Value     []byte `json:"value"`
	ExpiresAt int64  `json:"expires_at_ms,omitempty"`
}

type commitFunc func([]snapshotEntry) error

// Store serializes mutations with snapshot commits. Expired entries are
// removed lazily by the next operation.
type Store struct {
	mu     sync.RWMutex
	data   map[string]entry
	bytes  int64
	limits Limits
	commit commitFunc
	now    func() time.Time
}

// NewStore returns a bounded, non-persistent store.
func NewStore() *Store {
	return newStore(Limits{MaxKeys: DefaultMaxKeys, MaxMemoryBytes: DefaultMaxMemoryBytes}, nil)
}

func newStore(limits Limits, commit commitFunc) *Store {
	return &Store{data: make(map[string]entry), limits: limits, commit: commit, now: time.Now}
}

// Open recovers path when non-empty, then returns a synchronously persistent
// store. Zero limits select defaults; negative limits are rejected.
func Open(path string, limits Limits) (*Store, error) {
	if limits.MaxKeys < 0 || limits.MaxMemoryBytes < 0 {
		return nil, errors.New("store: limits cannot be negative")
	}
	if limits.MaxKeys == 0 {
		limits.MaxKeys = DefaultMaxKeys
	}
	if limits.MaxMemoryBytes == 0 {
		limits.MaxMemoryBytes = DefaultMaxMemoryBytes
	}
	if path == "" {
		return newStore(limits, nil), nil
	}
	entries, err := loadSnapshot(path, limits, time.Now())
	if err != nil {
		return nil, err
	}
	s := newStore(limits, func(entries []snapshotEntry) error {
		return writeSnapshot(path, entries)
	})
	for _, item := range entries {
		key, value := string(item.Key), string(item.Value)
		s.data[key] = entry{value: value, expiresAt: item.ExpiresAt}
		s.bytes += int64(len(key) + len(value))
	}
	return s, nil
}

// Set stores value under key, clearing any previous TTL. Successful durable
// mutations are committed before this method returns. On error state is unchanged.
func (s *Store) Set(key, value string) error {
	return s.SetTTL(key, value, 0)
}

// SetTTL stores value with ttl; zero means no expiry, negative TTL is invalid.
func (s *Store) SetTTL(key, value string, ttl time.Duration) error {
	if ttl < 0 {
		return errors.New("store: negative TTL")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UnixMilli()
	s.removeExpiredLocked(now)
	var expiresAt int64
	if ttl > 0 {
		expiresAt = s.now().Add(ttl).UnixMilli()
		if expiresAt <= now {
			return errors.New("store: TTL overflows deadline")
		}
	}
	old, exists := s.data[key]
	newBytes := s.bytes + int64(len(key)+len(value))
	if exists {
		newBytes -= int64(len(key) + len(old.value))
	}
	if !exists && len(s.data) >= s.limits.MaxKeys {
		return ErrKeyLimit
	}
	if newBytes > s.limits.MaxMemoryBytes {
		return ErrMemoryLimit
	}
	candidate := make(map[string]entry, len(s.data)+1)
	for k, v := range s.data {
		candidate[k] = v
	}
	candidate[key] = entry{value: value, expiresAt: expiresAt}
	if err := s.commitLocked(candidate); err != nil {
		return fmt.Errorf("store: snapshot commit: %w", err)
	}
	s.data = candidate
	s.bytes = newBytes
	return nil
}

// Get returns the value stored under key, or false if absent or expired.
func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked(s.now().UnixMilli())
	e, ok := s.data[key]
	return e.value, ok
}

// Delete removes key and durably commits the deletion before reporting true.
func (s *Store) Delete(key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked(s.now().UnixMilli())
	old, exists := s.data[key]
	if !exists {
		return false, nil
	}
	candidate := make(map[string]entry, len(s.data)-1)
	for k, v := range s.data {
		if k != key {
			candidate[k] = v
		}
	}
	if err := s.commitLocked(candidate); err != nil {
		return false, fmt.Errorf("store: snapshot commit: %w", err)
	}
	delete(s.data, key)
	s.bytes -= int64(len(key) + len(old.value))
	return true, nil
}

// Expire sets a relative TTL and reports whether key existed. Non-positive TTL
// deletes an existing key. Like Set/Delete, errors leave state unchanged.
func (s *Store) Expire(key string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked(s.now().UnixMilli())
	old, exists := s.data[key]
	if !exists {
		return false, nil
	}
	if ttl <= 0 {
		candidate := make(map[string]entry, len(s.data)-1)
		for k, v := range s.data {
			if k != key {
				candidate[k] = v
			}
		}
		if err := s.commitLocked(candidate); err != nil {
			return false, fmt.Errorf("store: snapshot commit: %w", err)
		}
		delete(s.data, key)
		s.bytes -= int64(len(key) + len(old.value))
		return true, nil
	}
	candidate := clone(s.data)
	e := candidate[key]
	e.expiresAt = s.now().Add(ttl).UnixMilli()
	candidate[key] = e
	if err := s.commitLocked(candidate); err != nil {
		return false, fmt.Errorf("store: snapshot commit: %w", err)
	}
	s.data = candidate
	return true, nil
}

// TTL returns the remaining time and false when key is absent or expired.
func (s *Store) TTL(key string) (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked(s.now().UnixMilli())
	e, ok := s.data[key]
	if !ok {
		return 0, false
	}
	if e.expiresAt == 0 {
		return -1, true
	}
	remaining := time.Duration(e.expiresAt-s.now().UnixMilli()) * time.Millisecond
	if remaining < 0 {
		remaining = 0
	}
	return remaining, true
}

// Exists reports whether key is present and unexpired.
func (s *Store) Exists(key string) bool {
	_, ok := s.Get(key)
	return ok
}

// Len returns the number of unexpired keys.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked(s.now().UnixMilli())
	return len(s.data)
}

// MemoryBytes returns accounted live key and value bytes.
func (s *Store) MemoryBytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked(s.now().UnixMilli())
	return s.bytes
}

// Keys returns current unexpired keys in stable order.
func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked(s.now().UnixMilli())
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (s *Store) commitLocked(candidate map[string]entry) error {
	if s.commit == nil {
		return nil
	}
	entries := make([]snapshotEntry, 0, len(candidate))
	for k, e := range candidate {
		entries = append(entries, snapshotEntry{Key: []byte(k), Value: []byte(e.value), ExpiresAt: e.expiresAt})
	}
	sort.Slice(entries, func(i, j int) bool { return string(entries[i].Key) < string(entries[j].Key) })
	return s.commit(entries)
}

func clone(source map[string]entry) map[string]entry {
	result := make(map[string]entry, len(source))
	for k, v := range source {
		result[k] = v
	}
	return result
}

func (s *Store) removeExpiredLocked(now int64) {
	for k, e := range s.data {
		if e.expiresAt != 0 && e.expiresAt <= now {
			delete(s.data, k)
			s.bytes -= int64(len(k) + len(e.value))
		}
	}
}
