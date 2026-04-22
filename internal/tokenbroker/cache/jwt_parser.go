package cache

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// JWTClaims represents the standard JWT claims we care about.
type JWTClaims struct {
	Exp int64 `json:"exp"` // Expiration time (Unix timestamp)
}

// ParseJWTExpiry extracts the expiration time from a JWT token.
// Returns the expiration time or an error if the token is not a valid JWT.
// For non-JWT tokens (e.g., GitHub PATs), returns a far-future time.
func ParseJWTExpiry(token string) (time.Time, error) {
	// Check if token looks like a JWT (three base64-encoded parts separated by dots)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		// Not a JWT, treat as long-lived token (1 year)
		return time.Now().Add(365 * 24 * time.Hour), nil
	}

	// Decode the payload (second part)
	payload := parts[1]

	// Add padding if needed (JWT uses base64url without padding)
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	// Decode base64
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		// If decoding fails, treat as non-JWT token
		return time.Now().Add(365 * 24 * time.Hour), nil
	}

	// Parse JSON
	var claims JWTClaims
	if err := json.Unmarshal(decoded, &claims); err != nil {
		// If JSON parsing fails, treat as non-JWT token
		return time.Now().Add(365 * 24 * time.Hour), nil
	}

	// Check if exp claim exists
	if claims.Exp == 0 {
		// No expiration claim, treat as long-lived
		return time.Now().Add(365 * 24 * time.Hour), nil
	}

	// Convert Unix timestamp to time.Time
	expiresAt := time.Unix(claims.Exp, 0)

	return expiresAt, nil
}

// IsTokenExpired checks if a token is expired or near expiry (< 5 minutes remaining).
func IsTokenExpired(expiresAt time.Time) bool {
	now := time.Now()

	// Token is expired
	if now.After(expiresAt) {
		return true
	}

	// Token is near expiry (< 5 minutes remaining)
	if expiresAt.Sub(now) < 5*time.Minute {
		return true
	}

	return false
}

// ParseGitHubTokenResponse parses a GitHub OAuth token response.
// GitHub returns: {"access_token": "...", "token_type": "bearer", "scope": "..."}
// Note: GitHub tokens typically don't have an expires_in field (they're long-lived).
func ParseGitHubTokenResponse(body []byte) (accessToken string, expiresIn int, err error) {
	var response struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in,omitempty"`
		Scope       string `json:"scope,omitempty"`
		Error       string `json:"error,omitempty"`
		ErrorDesc   string `json:"error_description,omitempty"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return "", 0, fmt.Errorf("failed to parse token response: %w", err)
	}

	if response.Error != "" {
		return "", 0, fmt.Errorf("OAuth error: %s - %s", response.Error, response.ErrorDesc)
	}

	if response.AccessToken == "" {
		return "", 0, fmt.Errorf("no access token in response")
	}

	// If no expires_in, default to 1 year (GitHub tokens are long-lived)
	if response.ExpiresIn == 0 {
		response.ExpiresIn = 365 * 24 * 60 * 60
	}

	return response.AccessToken, response.ExpiresIn, nil
}

// Made with Bob
