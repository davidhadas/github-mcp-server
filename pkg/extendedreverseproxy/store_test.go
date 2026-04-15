// Copyright 2026 IBM. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package extendedreverseproxy

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// mockPendingExchange is a mock implementation for testing.
type mockPendingExchange struct {
	key string
}

func (m *mockPendingExchange) Key() string {
	return m.key
}

func (m *mockPendingExchange) WaitResponse(ctx context.Context) (*http.Response, error) {
	return nil, nil
}

func (m *mockPendingExchange) Cancel(err error) {}

func TestMemoryStore_PutAndGet(t *testing.T) {
	store := NewMemoryStore()
	key := "test-key"
	pe := &mockPendingExchange{key: key}
	expiresAt := time.Now().Add(time.Minute)

	// Put
	err := store.Put(key, pe, expiresAt)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	// Get
	retrieved, ok := store.Get(key)
	if !ok {
		t.Fatal("Get failed: key not found")
	}
	if retrieved.Key() != key {
		t.Errorf("expected key %q, got %q", key, retrieved.Key())
	}
}

func TestMemoryStore_PutDuplicate(t *testing.T) {
	store := NewMemoryStore()
	key := "test-key"
	pe := &mockPendingExchange{key: key}
	expiresAt := time.Now().Add(time.Minute)

	// First put should succeed
	err := store.Put(key, pe, expiresAt)
	if err != nil {
		t.Fatalf("first Put failed: %v", err)
	}

	// Second put with same key should fail
	err = store.Put(key, pe, expiresAt)
	if err == nil {
		t.Error("expected error for duplicate key, got nil")
	}
}

func TestMemoryStore_GetNonExistent(t *testing.T) {
	store := NewMemoryStore()

	_, ok := store.Get("non-existent")
	if ok {
		t.Error("expected Get to return false for non-existent key")
	}
}

func TestMemoryStore_Delete(t *testing.T) {
	store := NewMemoryStore()
	key := "test-key"
	pe := &mockPendingExchange{key: key}
	expiresAt := time.Now().Add(time.Minute)

	store.Put(key, pe, expiresAt)

	// Delete
	err := store.Delete(key)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deleted
	_, ok := store.Get(key)
	if ok {
		t.Error("expected key to be deleted")
	}
}

func TestMemoryStore_GetExpired(t *testing.T) {
	store := NewMemoryStore()
	key := "test-key"
	pe := &mockPendingExchange{key: key}
	expiresAt := time.Now().Add(100 * time.Millisecond)

	store.Put(key, pe, expiresAt)

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Get should return false for expired key
	_, ok := store.Get(key)
	if ok {
		t.Error("expected Get to return false for expired key")
	}
}

func TestMemoryStore_CleanupExpired(t *testing.T) {
	store := NewMemoryStore()

	// Add some entries with different expiration times
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("key-%d", i)
		pe := &mockPendingExchange{key: key}
		var expiresAt time.Time
		if i < 3 {
			// First 3 expire soon
			expiresAt = time.Now().Add(50 * time.Millisecond)
		} else {
			// Last 2 expire later
			expiresAt = time.Now().Add(time.Minute)
		}
		store.Put(key, pe, expiresAt)
	}

	// Wait for first 3 to expire
	time.Sleep(100 * time.Millisecond)

	// Cleanup
	count := store.CleanupExpired()
	if count != 3 {
		t.Errorf("expected 3 expired entries, got %d", count)
	}

	// Verify remaining entries
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("key-%d", i)
		_, ok := store.Get(key)
		if i < 3 && ok {
			t.Errorf("expected key-%d to be cleaned up", i)
		}
		if i >= 3 && !ok {
			t.Errorf("expected key-%d to still exist", i)
		}
	}
}

func TestMemoryStore_Concurrent(t *testing.T) {
	store := NewMemoryStore()
	expiresAt := time.Now().Add(time.Minute)

	// Concurrent puts
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i)
			pe := &mockPendingExchange{key: key}
			store.Put(key, pe, expiresAt)
		}(i)
	}
	wg.Wait()

	// Concurrent gets
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i)
			_, ok := store.Get(key)
			if !ok {
				t.Errorf("expected to find key-%d", i)
			}
		}(i)
	}
	wg.Wait()

	// Concurrent deletes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i)
			store.Delete(key)
		}(i)
	}
	wg.Wait()

	// Verify all deleted
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%d", i)
		_, ok := store.Get(key)
		if ok {
			t.Errorf("expected key-%d to be deleted", i)
		}
	}
}

// Made with Bob
