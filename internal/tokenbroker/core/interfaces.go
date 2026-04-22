// Package core provides the main token acquisition orchestration for the Token Broker.
package core

import (
	"context"
	"time"
)

// SessionStore manages OAuth sessions and their lifecycle.
type SessionStore interface {
	// CreateSession creates a new session for a user and returns the session key.
	CreateSession(userID string) (sessionKey string, err error)

	// GetSession retrieves a session by its key.
	GetSession(sessionKey string) (*Session, error)

	// ValidateSession checks if a session exists and belongs to the specified user.
	ValidateSession(sessionKey, userID string) error

	// EndSession terminates a session and releases all resources.
	EndSession(sessionKey string) error

	// ExpireSession marks a session as expired and fails all pending requests.
	ExpireSession(sessionKey string)

	// ResetSessionTimer resets the idle timeout timer for a session.
	ResetSessionTimer(sessionKey string)

	// StartSessionTimer starts the idle timeout timer for a session.
	StartSessionTimer(sessionKey string)
}

// TokenCache stores and retrieves access tokens per user and MCP server.
type TokenCache interface {
	// GetToken retrieves a cached token for a user and MCP server.
	// Returns the token and true if found and not expired, empty string and false otherwise.
	GetToken(userID, mcpServerURL string) (token string, found bool)

	// SetToken stores a token for a user and MCP server.
	SetToken(userID, mcpServerURL, token string) error

	// DeleteToken removes a token from the cache.
	DeleteToken(userID, mcpServerURL string)
}

// OAuthDiscoverer performs OAuth discovery against an MCP server.
type OAuthDiscoverer interface {
	// SendSyntheticRequest sends an unauthenticated MCP request to trigger OAuth elicitation.
	// Returns true if the server responds with 401, false otherwise.
	SendSyntheticRequest(ctx context.Context, mcpServerURL string) (is401 bool, err error)

	// DiscoverAuthServer discovers the OAuth authorization server for an MCP server.
	DiscoverAuthServer(ctx context.Context, mcpServerURL string) (authServerURL string, err error)

	// GetAuthURL requests an authorization URL from the MCP server.
	// The MCP server generates PKCE and returns both the auth URL and code_verifier.
	GetAuthURL(ctx context.Context, mcpServerURL, callbackURL string) (authURL, codeVerifier string, err error)
}

// TokenExchanger exchanges an authorization code for an access token.
type TokenExchanger interface {
	// ExchangeToken exchanges an authorization code for an access token via the MCP server.
	// The MCP server uses its client_secret to exchange with the OAuth provider.
	ExchangeToken(ctx context.Context, mcpServerURL, code, codeVerifier string) (accessToken string, err error)
}

// Clock provides time-related operations for testability.
type Clock interface {
	// Now returns the current time.
	Now() time.Time

	// After returns a channel that receives the current time after the specified duration.
	After(d time.Duration) <-chan time.Time
}

// Session represents an OAuth session.
type Session struct {
	SessionKey         string
	UserID             string
	CreatedAt          time.Time
	LastPollAt         time.Time
	ExpirationDeadline time.Time

	// Concurrency control
	AcquisitionSemaphore Semaphore

	// Event coordination
	EventWaiters chan Event
	TokenWaiters map[string][]chan TokenResult // mcpServerURL -> waiters

	// Active OAuth transaction
	ActiveOAuthTx *OAuthTransaction
}

// Semaphore provides mutual exclusion for token acquisition per session.
type Semaphore interface {
	// Acquire acquires the semaphore, blocking until available or context is cancelled.
	Acquire(ctx context.Context) error

	// Release releases the semaphore.
	Release()
}

// OAuthTransaction represents an active OAuth flow.
type OAuthTransaction struct {
	MCPServerURL   string
	AuthURL        string
	CodeVerifier   string // Received from MCP server
	Status         string // "waiting" | "completed" | "failed"
	CompletionChan chan OAuthCompletion
}

// OAuthCompletion represents the result of an OAuth flow.
type OAuthCompletion struct {
	Code  string
	State string
	Error error
}

// Event represents an event sent to the Backend via long-poll.
type Event struct {
	Type string `json:"type"` // "oauth_required" | "error"

	// For oauth_required
	MCPServerURL string `json:"mcp_server_url,omitempty"`
	AuthURL      string `json:"auth_url,omitempty"`

	// For error
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
}

// TokenResult represents the result of a token acquisition.
type TokenResult struct {
	Token string
	Error error
}

// RealClock implements Clock using the standard time package.
type RealClock struct{}

// Now returns the current time.
func (c *RealClock) Now() time.Time {
	return time.Now()
}

// After returns a channel that receives the current time after the specified duration.
func (c *RealClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

// Made with Bob
