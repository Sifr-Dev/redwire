// Package store implements the single, concurrency-safe in-memory key-value
// store at the core of Cipher Redis: one implementation, one source of truth.
package store

import "sync"

// Store is a string->string map guarded by a RWMutex. All operations are
// safe for concurrent use.
type Store struct {
	data map[string]string
	mux  sync.RWMutex
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{data: make(map[string]string)}
}

// Set stores value under key.
func (s *Store) Set(key, value string) {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.data[key] = value
}

// Get returns the value stored under key, or false if absent.
func (s *Store) Get(key string) (string, bool) {
	s.mux.RLock()
	defer s.mux.RUnlock()
	val, ok := s.data[key]
	return val, ok
}

// Delete removes key and reports whether it existed.
func (s *Store) Delete(key string) bool {
	s.mux.Lock()
	defer s.mux.Unlock()
	_, exists := s.data[key]
	if exists {
		delete(s.data, key)
	}
	return exists
}

// Exists reports whether key is present.
func (s *Store) Exists(key string) bool {
	s.mux.RLock()
	defer s.mux.RUnlock()
	_, ok := s.data[key]
	return ok
}

// Len returns the number of keys.
func (s *Store) Len() int {
	s.mux.RLock()
	defer s.mux.RUnlock()
	return len(s.data)
}

// Keys returns the current keys (unordered).
func (s *Store) Keys() []string {
	s.mux.RLock()
	defer s.mux.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}
