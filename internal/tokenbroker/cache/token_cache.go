package cache

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/github/github-mcp-server/internal/tokenbroker/core"
)

// TokenEntry represents a cached token.
type TokenEntry struct {
	AccessToken string
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

// TokenCache stores and retrieves access tokens per user and MCP server.
type TokenCache struct {
	tokens map[string]map[string]*TokenEntry // userID -> mcpServerURL -> token
	mu     sync.RWMutex
	clock  core.Clock
}

// NewTokenCache creates a new token cache.
func NewTokenCache(clock core.Clock) *TokenCache {
	return &TokenCache{
		tokens: make(map[string]map[string]*TokenEntry),
		clock:  clock,
	}
}

// GetToken retrieves a cached token for a user and MCP server.
// Returns the token and true if found and not expired, empty string and false otherwise.
func (tc *TokenCache) GetToken(userID, mcpServerURL string) (string, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	userTokens, ok := tc.tokens[userID]
	if !ok {
		return "", false
	}

	entry, ok := userTokens[mcpServerURL]
	if !ok {
		return "", false
	}

	// Check if token is expired or near expiry
	if IsTokenExpired(entry.ExpiresAt) {
		slog.Debug("Token expired or near expiry",
			"user_id", userID,
			"mcp_server", mcpServerURL,
			"expires_at", entry.ExpiresAt,
			"now", tc.clock.Now())
		return "", false
	}

	slog.Debug("Token cache hit",
		"user_id", userID,
		"mcp_server", mcpServerURL,
		"expires_at", entry.ExpiresAt)

	return entry.AccessToken, true
}

// SetToken stores a token for a user and MCP server.
func (tc *TokenCache) SetToken(userID, mcpServerURL, token string) error {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	// Parse token expiry
	expiresAt, err := ParseJWTExpiry(token)
	if err != nil {
		return fmt.Errorf("failed to parse token expiry: %w", err)
	}

	// Create user token map if it doesn't exist
	if tc.tokens[userID] == nil {
		tc.tokens[userID] = make(map[string]*TokenEntry)
	}

	// Store token
	tc.tokens[userID][mcpServerURL] = &TokenEntry{
		AccessToken: token,
		ExpiresAt:   expiresAt,
		CreatedAt:   tc.clock.Now(),
	}

	slog.Info("Token cached",
		"user_id", userID,
		"mcp_server", mcpServerURL,
		"expires_at", expiresAt,
		"ttl", expiresAt.Sub(tc.clock.Now()))

	return nil
}

// DeleteToken removes a token from the cache.
func (tc *TokenCache) DeleteToken(userID, mcpServerURL string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if userTokens, ok := tc.tokens[userID]; ok {
		delete(userTokens, mcpServerURL)
		slog.Info("Token removed from cache",
			"user_id", userID,
			"mcp_server", mcpServerURL)

		// Clean up empty user map
		if len(userTokens) == 0 {
			delete(tc.tokens, userID)
		}
	}
}

// GetCacheSize returns the total number of cached tokens.
func (tc *TokenCache) GetCacheSize() int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	count := 0
	for _, userTokens := range tc.tokens {
		count += len(userTokens)
	}
	return count
}

// GetUserTokenCount returns the number of cached tokens for a user.
func (tc *TokenCache) GetUserTokenCount(userID string) int {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	if userTokens, ok := tc.tokens[userID]; ok {
		return len(userTokens)
	}
	return 0
}

// CleanupExpiredTokens removes all expired tokens from the cache.
// This can be called periodically to free memory.
func (tc *TokenCache) CleanupExpiredTokens() int {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	removed := 0
	for userID, userTokens := range tc.tokens {
		for mcpServerURL, entry := range userTokens {
			if IsTokenExpired(entry.ExpiresAt) {
				delete(userTokens, mcpServerURL)
				removed++
				slog.Debug("Expired token removed",
					"user_id", userID,
					"mcp_server", mcpServerURL)
			}
		}

		// Clean up empty user map
		if len(userTokens) == 0 {
			delete(tc.tokens, userID)
		}
	}

	if removed > 0 {
		slog.Info("Expired tokens cleaned up", "removed_count", removed)
	}

	return removed
}

// Made with Bob
