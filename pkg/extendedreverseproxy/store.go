// Copyright 2026 IBM. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package extendedreverseproxy

import (
	"fmt"
	"sync"
	"time"
)

// memoryStore implements PendingStore with an in-memory map.
type memoryStore struct {
	mu      sync.RWMutex
	entries map[string]*storeEntry
}

type storeEntry struct {
	exchange  PendingExchange
	expiresAt time.Time
}

// NewMemoryStore creates a new in-memory pending store.
func NewMemoryStore() PendingStore {
	return &memoryStore{
		entries: make(map[string]*storeEntry),
	}
}

func (s *memoryStore) Put(key string, exchange PendingExchange, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.entries[key]; exists {
		return fmt.Errorf("key already exists: %s", key)
	}

	s.entries[key] = &storeEntry{
		exchange:  exchange,
		expiresAt: expiresAt,
	}
	return nil
}

func (s *memoryStore) Get(key string) (PendingExchange, bool) {
	s.mu.RLock()
	entry, ok := s.entries[key]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}

	if time.Now().After(entry.expiresAt) {
		entry.exchange.Cancel(fmt.Errorf("expired"))
		_ = s.Delete(key)
		return nil, false
	}

	return entry.exchange, true
}

func (s *memoryStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.entries, key)
	return nil
}

func (s *memoryStore) CleanupExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	count := 0

	for key, entry := range s.entries {
		if now.After(entry.expiresAt) {
			entry.exchange.Cancel(fmt.Errorf("expired"))
			delete(s.entries, key)
			count++
		}
	}

	return count
}

// Made with Bob
