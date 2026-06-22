package internal

import "sync"

type Store struct {
	data map[string]string
	mux  sync.RWMutex
}

func NewStore() *Store {
	return &Store{
		data: make(map[string]string),
	}
}

func (s *Store) Set(key, value string) {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.data[key] = value
}

func (s *Store) Get(key string) (string, bool) {
	s.mux.RLock()
	defer s.mux.RUnlock()
	val, ok := s.data[key]
	return val, ok
}

func (s *Store) Delete(key string) {
	s.mux.Lock()
	defer s.mux.Unlock()
	delete(s.data, key)
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
