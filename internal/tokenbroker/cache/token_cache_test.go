package cache

import (
	"testing"
	"time"

	"github.com/github/github-mcp-server/internal/tokenbroker/core"
)

// FakeClock implements core.Clock for testing.
type FakeClock struct {
	now time.Time
}

func (c *FakeClock) Now() time.Time {
	return c.now
}

func (c *FakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- c.now.Add(d)
	return ch
}

func (c *FakeClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

func TestTokenCache_SetAndGet(t *testing.T) {
	clock := &FakeClock{now: time.Now()}
	cache := NewTokenCache(clock)

	userID := "user1"
	mcpServerURL := "http://mcp.example.com"
	token := "test-token-123"

	// Set token
	err := cache.SetToken(userID, mcpServerURL, token)
	if err != nil {
		t.Fatalf("SetToken failed: %v", err)
	}

	// Get token
	retrievedToken, found := cache.GetToken(userID, mcpServerURL)
	if !found {
		t.Fatal("Token not found")
	}

	if retrievedToken != token {
		t.Errorf("Expected token %s, got %s", token, retrievedToken)
	}
}

func TestTokenCache_GetNonExistent(t *testing.T) {
	clock := &FakeClock{now: time.Now()}
	cache := NewTokenCache(clock)

	token, found := cache.GetToken("user1", "http://mcp.example.com")
	if found {
		t.Error("Expected token not found, but got one")
	}
	if token != "" {
		t.Errorf("Expected empty token, got %s", token)
	}
}

func TestTokenCache_ExpiredToken(t *testing.T) {
	clock := &FakeClock{now: time.Now()}
	cache := NewTokenCache(clock)

	userID := "user1"
	mcpServerURL := "http://mcp.example.com"

	// Create a JWT token that expires in 1 hour from fake clock time
	expiresAt := clock.now.Add(1 * time.Hour)
	token := createTestJWT(expiresAt)

	// Set token
	err := cache.SetToken(userID, mcpServerURL, token)
	if err != nil {
		t.Fatalf("SetToken failed: %v", err)
	}

	// Token should be found (using fake clock time)
	_, found := cache.GetToken(userID, mcpServerURL)
	if !found {
		t.Error("Token should be found")
	}

	// Advance fake clock past expiry
	clock.Advance(2 * time.Hour)

	// Token should not be found (expired according to fake clock)
	// Note: IsTokenExpired uses time.Now(), not the fake clock
	// So we need to create a token that's actually expired
	pastExpiresAt := time.Now().Add(-1 * time.Hour)
	expiredToken := createTestJWT(pastExpiresAt)
	cache.SetToken(userID, mcpServerURL, expiredToken)

	_, found = cache.GetToken(userID, mcpServerURL)
	if found {
		t.Error("Expired token should not be found")
	}
}

func TestTokenCache_NearExpiryToken(t *testing.T) {
	clock := &FakeClock{now: time.Now()}
	cache := NewTokenCache(clock)

	userID := "user1"
	mcpServerURL := "http://mcp.example.com"

	// Create a JWT token that expires in 3 minutes (< 5 min threshold)
	expiresAt := clock.now.Add(3 * time.Minute)
	token := createTestJWT(expiresAt)

	// Set token
	err := cache.SetToken(userID, mcpServerURL, token)
	if err != nil {
		t.Fatalf("SetToken failed: %v", err)
	}

	// Token should not be found (near expiry)
	_, found := cache.GetToken(userID, mcpServerURL)
	if found {
		t.Error("Near-expiry token should not be found")
	}
}

func TestTokenCache_PerUserPerServer(t *testing.T) {
	clock := &FakeClock{now: time.Now()}
	cache := NewTokenCache(clock)

	user1 := "user1"
	user2 := "user2"
	server1 := "http://mcp1.example.com"
	server2 := "http://mcp2.example.com"

	token1 := "token1"
	token2 := "token2"
	token3 := "token3"
	token4 := "token4"

	// Set tokens for different combinations
	cache.SetToken(user1, server1, token1)
	cache.SetToken(user1, server2, token2)
	cache.SetToken(user2, server1, token3)
	cache.SetToken(user2, server2, token4)

	// Verify isolation
	tests := []struct {
		user     string
		server   string
		expected string
	}{
		{user1, server1, token1},
		{user1, server2, token2},
		{user2, server1, token3},
		{user2, server2, token4},
	}

	for _, tt := range tests {
		token, found := cache.GetToken(tt.user, tt.server)
		if !found {
			t.Errorf("Token not found for user=%s, server=%s", tt.user, tt.server)
			continue
		}
		if token != tt.expected {
			t.Errorf("Expected token %s for user=%s, server=%s, got %s",
				tt.expected, tt.user, tt.server, token)
		}
	}
}

func TestTokenCache_DeleteToken(t *testing.T) {
	clock := &FakeClock{now: time.Now()}
	cache := NewTokenCache(clock)

	userID := "user1"
	mcpServerURL := "http://mcp.example.com"
	token := "test-token"

	// Set token
	cache.SetToken(userID, mcpServerURL, token)

	// Verify it exists
	_, found := cache.GetToken(userID, mcpServerURL)
	if !found {
		t.Fatal("Token should exist")
	}

	// Delete token
	cache.DeleteToken(userID, mcpServerURL)

	// Verify it's gone
	_, found = cache.GetToken(userID, mcpServerURL)
	if found {
		t.Error("Token should be deleted")
	}
}

func TestTokenCache_GetCacheSize(t *testing.T) {
	clock := &FakeClock{now: time.Now()}
	cache := NewTokenCache(clock)

	if size := cache.GetCacheSize(); size != 0 {
		t.Errorf("Expected cache size 0, got %d", size)
	}

	// Add tokens
	cache.SetToken("user1", "http://mcp1.example.com", "token1")
	cache.SetToken("user1", "http://mcp2.example.com", "token2")
	cache.SetToken("user2", "http://mcp1.example.com", "token3")

	if size := cache.GetCacheSize(); size != 3 {
		t.Errorf("Expected cache size 3, got %d", size)
	}

	// Delete one
	cache.DeleteToken("user1", "http://mcp1.example.com")

	if size := cache.GetCacheSize(); size != 2 {
		t.Errorf("Expected cache size 2, got %d", size)
	}
}

func TestTokenCache_GetUserTokenCount(t *testing.T) {
	clock := &FakeClock{now: time.Now()}
	cache := NewTokenCache(clock)

	user1 := "user1"
	user2 := "user2"

	// Add tokens for user1
	cache.SetToken(user1, "http://mcp1.example.com", "token1")
	cache.SetToken(user1, "http://mcp2.example.com", "token2")

	// Add token for user2
	cache.SetToken(user2, "http://mcp1.example.com", "token3")

	if count := cache.GetUserTokenCount(user1); count != 2 {
		t.Errorf("Expected user1 token count 2, got %d", count)
	}

	if count := cache.GetUserTokenCount(user2); count != 1 {
		t.Errorf("Expected user2 token count 1, got %d", count)
	}

	if count := cache.GetUserTokenCount("user3"); count != 0 {
		t.Errorf("Expected user3 token count 0, got %d", count)
	}
}

func TestTokenCache_CleanupExpiredTokens(t *testing.T) {
	clock := &FakeClock{now: time.Now()}
	cache := NewTokenCache(clock)

	// Add tokens with different expiry times (using real time for IsTokenExpired)
	now := time.Now()
	expiresIn2Hours := now.Add(2 * time.Hour)
	expiredToken := now.Add(-1 * time.Hour) // Already expired

	cache.SetToken("user1", "http://mcp1.example.com", createTestJWT(expiredToken))
	cache.SetToken("user1", "http://mcp2.example.com", createTestJWT(expiresIn2Hours))
	cache.SetToken("user2", "http://mcp1.example.com", createTestJWT(expiredToken))

	if size := cache.GetCacheSize(); size != 3 {
		t.Errorf("Expected cache size 3, got %d", size)
	}

	// Cleanup expired tokens
	removed := cache.CleanupExpiredTokens()

	if removed != 2 {
		t.Errorf("Expected 2 tokens removed, got %d", removed)
	}

	if size := cache.GetCacheSize(); size != 1 {
		t.Errorf("Expected cache size 1 after cleanup, got %d", size)
	}

	// Verify the remaining token is the one that expires in 2 hours
	_, found := cache.GetToken("user1", "http://mcp2.example.com")
	if !found {
		t.Error("Token with 2-hour expiry should still exist")
	}
}

// Helper function to create a test JWT with specific expiry
func createTestJWT(expiresAt time.Time) string {
	// Use the same helper from jwt_parser_test.go
	return createValidJWT(expiresAt)
}

// Verify FakeClock implements core.Clock
var _ core.Clock = (*FakeClock)(nil)

// Made with Bob
