// Package internal: the shared in-memory store. Single implementation used by
// the REPL and the HTTP handler — no split-brain copies.
package internal

import "sync"

type Store struct {
	data map[string]string
	mux  sync.RWMutex
}

func NewStore() *Store {
	return &Store{data: make(map[string]string)}
}

func (s *Store) Set(key, value string) {
	s.mux.Lock()
	s.data[key] = value
	s.mux.Unlock()
}

func (s *Store) Get(key string) (string, bool) {
	s.mux.RLock()
	defer s.mux.RUnlock()
	v, ok := s.data[key]
	return v, ok
}

func (s *Store) Delete(key string) bool {
	s.mux.Lock()
	defer s.mux.Unlock()
	_, ok := s.data[key]
	delete(s.data, key)
	return ok
}

func (s *Store) Keys() []string {
	s.mux.RLock()
	defer s.mux.RUnlock()
	keys := make([]string, 0, len(s.data))
	for k := range s.data {
		keys = append(keys, k)
	}
	return keys
}
