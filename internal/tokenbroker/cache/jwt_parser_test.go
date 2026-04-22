package cache

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestParseJWTExpiry_ValidJWT(t *testing.T) {
	// Create a valid JWT with expiry in 1 hour
	expiresAt := time.Now().Add(1 * time.Hour)
	token := createValidJWT(expiresAt)

	parsedExpiry, err := ParseJWTExpiry(token)
	if err != nil {
		t.Fatalf("ParseJWTExpiry failed: %v", err)
	}

	// Allow 1 second tolerance for time comparison
	diff := parsedExpiry.Sub(expiresAt)
	if diff < -time.Second || diff > time.Second {
		t.Errorf("Expected expiry %v, got %v (diff: %v)", expiresAt, parsedExpiry, diff)
	}
}

func TestParseJWTExpiry_NonJWT(t *testing.T) {
	// Non-JWT token (e.g., GitHub PAT)
	token := "ghp_1234567890abcdefghijklmnopqrstuvwxyz"

	parsedExpiry, err := ParseJWTExpiry(token)
	if err != nil {
		t.Fatalf("ParseJWTExpiry failed: %v", err)
	}

	// Should return far-future expiry (1 year)
	expectedMin := time.Now().Add(364 * 24 * time.Hour)
	if parsedExpiry.Before(expectedMin) {
		t.Errorf("Expected far-future expiry, got %v", parsedExpiry)
	}
}

func TestParseJWTExpiry_InvalidBase64(t *testing.T) {
	// JWT with invalid base64 in payload
	token := "header.!!!invalid-base64!!!.signature"

	parsedExpiry, err := ParseJWTExpiry(token)
	if err != nil {
		t.Fatalf("ParseJWTExpiry failed: %v", err)
	}

	// Should treat as non-JWT and return far-future expiry
	expectedMin := time.Now().Add(364 * 24 * time.Hour)
	if parsedExpiry.Before(expectedMin) {
		t.Errorf("Expected far-future expiry for invalid JWT, got %v", parsedExpiry)
	}
}

func TestParseJWTExpiry_NoExpClaim(t *testing.T) {
	// JWT without exp claim
	payload := map[string]interface{}{
		"sub": "user123",
		"iat": time.Now().Unix(),
	}
	payloadBytes, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)
	token := fmt.Sprintf("eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.%s.signature", payloadB64)

	parsedExpiry, err := ParseJWTExpiry(token)
	if err != nil {
		t.Fatalf("ParseJWTExpiry failed: %v", err)
	}

	// Should return far-future expiry
	expectedMin := time.Now().Add(364 * 24 * time.Hour)
	if parsedExpiry.Before(expectedMin) {
		t.Errorf("Expected far-future expiry for JWT without exp, got %v", parsedExpiry)
	}
}

func TestIsTokenExpired_Expired(t *testing.T) {
	expiresAt := time.Now().Add(-1 * time.Hour) // 1 hour ago
	if !IsTokenExpired(expiresAt) {
		t.Error("Token should be expired")
	}
}

func TestIsTokenExpired_NearExpiry(t *testing.T) {
	expiresAt := time.Now().Add(3 * time.Minute) // 3 minutes from now (< 5 min threshold)
	if !IsTokenExpired(expiresAt) {
		t.Error("Token should be considered expired (near expiry)")
	}
}

func TestIsTokenExpired_Valid(t *testing.T) {
	expiresAt := time.Now().Add(10 * time.Minute) // 10 minutes from now
	if IsTokenExpired(expiresAt) {
		t.Error("Token should not be expired")
	}
}

func TestParseGitHubTokenResponse_Success(t *testing.T) {
	response := []byte(`{
		"access_token": "gho_test123",
		"token_type": "bearer",
		"scope": "repo,read:org"
	}`)

	token, expiresIn, err := ParseGitHubTokenResponse(response)
	if err != nil {
		t.Fatalf("ParseGitHubTokenResponse failed: %v", err)
	}

	if token != "gho_test123" {
		t.Errorf("Expected token gho_test123, got %s", token)
	}

	// GitHub tokens without expires_in should default to 1 year
	expectedExpiresIn := 365 * 24 * 60 * 60
	if expiresIn != expectedExpiresIn {
		t.Errorf("Expected expiresIn %d, got %d", expectedExpiresIn, expiresIn)
	}
}

func TestParseGitHubTokenResponse_WithExpiresIn(t *testing.T) {
	response := []byte(`{
		"access_token": "gho_test123",
		"token_type": "bearer",
		"expires_in": 3600,
		"scope": "repo"
	}`)

	token, expiresIn, err := ParseGitHubTokenResponse(response)
	if err != nil {
		t.Fatalf("ParseGitHubTokenResponse failed: %v", err)
	}

	if token != "gho_test123" {
		t.Errorf("Expected token gho_test123, got %s", token)
	}

	if expiresIn != 3600 {
		t.Errorf("Expected expiresIn 3600, got %d", expiresIn)
	}
}

func TestParseGitHubTokenResponse_Error(t *testing.T) {
	response := []byte(`{
		"error": "invalid_grant",
		"error_description": "The code has expired"
	}`)

	_, _, err := ParseGitHubTokenResponse(response)
	if err == nil {
		t.Fatal("Expected error, got nil")
	}

	expectedError := "OAuth error: invalid_grant - The code has expired"
	if err.Error() != expectedError {
		t.Errorf("Expected error %q, got %q", expectedError, err.Error())
	}
}

func TestParseGitHubTokenResponse_NoAccessToken(t *testing.T) {
	response := []byte(`{
		"token_type": "bearer"
	}`)

	_, _, err := ParseGitHubTokenResponse(response)
	if err == nil {
		t.Fatal("Expected error for missing access_token, got nil")
	}
}

func TestParseGitHubTokenResponse_InvalidJSON(t *testing.T) {
	response := []byte(`invalid json`)

	_, _, err := ParseGitHubTokenResponse(response)
	if err == nil {
		t.Fatal("Expected error for invalid JSON, got nil")
	}
}

// Helper function to create a valid JWT with specific expiry
func createValidJWT(expiresAt time.Time) string {
	// Create header
	header := map[string]string{
		"alg": "HS256",
		"typ": "JWT",
	}
	headerBytes, _ := json.Marshal(header)
	headerB64 := base64.RawURLEncoding.EncodeToString(headerBytes)

	// Create payload with exp claim
	payload := map[string]interface{}{
		"sub": "user123",
		"exp": expiresAt.Unix(),
		"iat": time.Now().Unix(),
	}
	payloadBytes, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	// Create signature (dummy for testing)
	signature := "test-signature"

	return fmt.Sprintf("%s.%s.%s", headerB64, payloadB64, signature)
}

// Made with Bob
