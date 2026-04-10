// Package oauth provides OAuth 2.0 elicitation support for MCP servers.
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// ElicitationConfig holds the OAuth configuration for MCP elicitation protocol.
type ElicitationConfig struct {
	// ClientID is the OAuth client ID (GitHub App or OAuth App)
	ClientID string

	// RedirectURI is the OAuth redirect URI (optional, can be provided by client)
	RedirectURI string

	// Scopes are the default OAuth scopes to request
	Scopes []string

	// AuthorizationServer is the OAuth authorization server URL
	AuthorizationServer string
}

// AuthURLRequest represents the MCP auth/url request parameters.
type AuthURLRequest struct {
	// CallbackURL is the client's callback URL for receiving the authorization code
	CallbackURL string `json:"callback_url"`
}

// AuthURLResponse represents the MCP auth/url response.
type AuthURLResponse struct {
	// URL is the complete OAuth authorization URL for the user to visit
	URL string `json:"url"`

	// CodeVerifier is the PKCE code verifier that must be sent during token exchange
	CodeVerifier string `json:"code_verifier,omitempty"`

	// meta holds metadata for the MCP Result interface
	meta map[string]any
}

// GetMeta returns metadata from the response.
func (r *AuthURLResponse) GetMeta() map[string]any {
	return r.meta
}

// SetMeta sets the metadata on the response.
func (r *AuthURLResponse) SetMeta(meta map[string]any) {
	r.meta = meta
}

// PKCEChallenge holds PKCE (Proof Key for Code Exchange) parameters.
type PKCEChallenge struct {
	// Verifier is the code verifier (random string)
	Verifier string

	// Challenge is the code challenge (SHA256 hash of verifier, base64url encoded)
	Challenge string

	// Method is always "S256" for SHA256
	Method string
}

// GeneratePKCEChallenge generates a PKCE code verifier and challenge.
// This implements RFC 7636 for enhanced security in OAuth flows.
func GeneratePKCEChallenge() (*PKCEChallenge, error) {
	// Generate 32 random bytes for the verifier
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return nil, fmt.Errorf("failed to generate random verifier: %w", err)
	}

	// Base64url encode the verifier (without padding)
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// Create SHA256 hash of the verifier
	hash := sha256.Sum256([]byte(verifier))

	// Base64url encode the challenge (without padding)
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	return &PKCEChallenge{
		Verifier:  verifier,
		Challenge: challenge,
		Method:    "S256",
	}, nil
}

// GenerateState generates a cryptographically secure random state parameter.
func GenerateState() (string, error) {
	stateBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return "", fmt.Errorf("failed to generate random state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(stateBytes), nil
}

// BuildAuthorizationURL constructs the complete OAuth authorization URL.
func (c *ElicitationConfig) BuildAuthorizationURL(req AuthURLRequest) (*AuthURLResponse, error) {
	if c.ClientID == "" {
		return nil, fmt.Errorf("OAuth client ID is required")
	}

	if c.AuthorizationServer == "" {
		return nil, fmt.Errorf("authorization server URL is required")
	}

	if req.CallbackURL == "" && c.RedirectURI == "" {
		return nil, fmt.Errorf("callback URL or redirect URI is required")
	}

	// Use callback URL from request, or fall back to configured redirect URI
	redirectURI := req.CallbackURL
	if redirectURI == "" {
		redirectURI = c.RedirectURI
	}

	// Generate PKCE challenge for enhanced security
	pkce, err := GeneratePKCEChallenge()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PKCE challenge: %w", err)
	}

	// Generate state parameter
	state, err := GenerateState()
	if err != nil {
		return nil, fmt.Errorf("failed to generate state: %w", err)
	}

	// Build the authorization URL
	authURL, err := url.Parse(c.AuthorizationServer)
	if err != nil {
		return nil, fmt.Errorf("invalid authorization server URL: %w", err)
	}

	// Ensure the path ends with /authorize
	if !strings.HasSuffix(authURL.Path, "/authorize") {
		authURL.Path = strings.TrimSuffix(authURL.Path, "/") + "/authorize"
	}

	// Build query parameters
	params := url.Values{}
	params.Set("client_id", c.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("state", state)

	// Add scopes if configured
	if len(c.Scopes) > 0 {
		params.Set("scope", strings.Join(c.Scopes, " "))
	}

	// Add PKCE parameters
	params.Set("code_challenge", pkce.Challenge)
	params.Set("code_challenge_method", pkce.Method)

	authURL.RawQuery = params.Encode()

	return &AuthURLResponse{
		URL:          authURL.String(),
		CodeVerifier: pkce.Verifier,
	}, nil
}

// Validate checks if the elicitation configuration is valid.
func (c *ElicitationConfig) Validate() error {
	if c.ClientID == "" {
		return fmt.Errorf("OAuth client ID is required")
	}

	// AuthorizationServer is optional - it will be resolved from API host if not provided
	// Validate authorization server URL if provided
	if _, err := url.Parse(c.AuthorizationServer); err != nil {
		return fmt.Errorf("invalid authorization server URL: %w", err)
	}

	// Validate redirect URI if provided
	if c.RedirectURI != "" {
		if _, err := url.Parse(c.RedirectURI); err != nil {
			return fmt.Errorf("invalid redirect URI: %w", err)
		}
	}

	return nil
}

// Made with Bob
