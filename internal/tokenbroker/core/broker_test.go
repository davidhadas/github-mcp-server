package core

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// mockOAuthDiscoverer is a mock implementation of OAuthDiscoverer for testing.
type mockOAuthDiscoverer struct {
	sendSyntheticRequestFunc func(ctx context.Context, mcpServerURL string) (bool, error)
	discoverAuthServerFunc   func(ctx context.Context, mcpServerURL string) (string, error)
	getAuthURLFunc           func(ctx context.Context, authServerURL, callbackURL string) (string, string, error)
}

func (m *mockOAuthDiscoverer) SendSyntheticRequest(ctx context.Context, mcpServerURL string) (bool, error) {
	if m.sendSyntheticRequestFunc != nil {
		return m.sendSyntheticRequestFunc(ctx, mcpServerURL)
	}
	return true, nil
}

func (m *mockOAuthDiscoverer) DiscoverAuthServer(ctx context.Context, mcpServerURL string) (string, error) {
	if m.discoverAuthServerFunc != nil {
		return m.discoverAuthServerFunc(ctx, mcpServerURL)
	}
	return "https://auth.example.com", nil
}

func (m *mockOAuthDiscoverer) GetAuthURL(ctx context.Context, authServerURL, callbackURL string) (string, string, error) {
	if m.getAuthURLFunc != nil {
		return m.getAuthURLFunc(ctx, authServerURL, callbackURL)
	}
	return "https://auth.example.com/authorize?...", "code_verifier_123", nil
}

// mockTokenExchanger is a mock implementation of TokenExchanger for testing.
type mockTokenExchanger struct {
	exchangeTokenFunc func(ctx context.Context, mcpServerURL, code, codeVerifier string) (string, error)
}

func (m *mockTokenExchanger) ExchangeToken(ctx context.Context, mcpServerURL, code, codeVerifier string) (string, error) {
	if m.exchangeTokenFunc != nil {
		return m.exchangeTokenFunc(ctx, mcpServerURL, code, codeVerifier)
	}
	return "access_token_123", nil
}

// mockTokenCache is a mock implementation of TokenCache for testing.
type mockTokenCache struct {
	getTokenFunc    func(userID, mcpServerURL string) (string, bool)
	setTokenFunc    func(userID, mcpServerURL, token string) error
	deleteTokenFunc func(userID, mcpServerURL string)
}

func (m *mockTokenCache) GetToken(userID, mcpServerURL string) (string, bool) {
	if m.getTokenFunc != nil {
		return m.getTokenFunc(userID, mcpServerURL)
	}
	return "", false
}

func (m *mockTokenCache) SetToken(userID, mcpServerURL, token string) error {
	if m.setTokenFunc != nil {
		return m.setTokenFunc(userID, mcpServerURL, token)
	}
	return nil
}

func (m *mockTokenCache) DeleteToken(userID, mcpServerURL string) {
	if m.deleteTokenFunc != nil {
		m.deleteTokenFunc(userID, mcpServerURL)
	}
}

// mockSessionStore is a mock implementation of SessionStore for testing.
type mockSessionStore struct {
	sessions map[string]*Session
}

func newMockSessionStore() *mockSessionStore {
	return &mockSessionStore{
		sessions: make(map[string]*Session),
	}
}

func (m *mockSessionStore) CreateSession(userID string) (string, error) {
	sessionKey := "session_" + userID
	m.sessions[sessionKey] = &Session{
		SessionKey:           sessionKey,
		UserID:               userID,
		CreatedAt:            time.Now(),
		EventWaiters:         make(chan Event, 1),
		TokenWaiters:         make(map[string][]chan TokenResult),
		AcquisitionSemaphore: &mockSemaphore{},
	}
	return sessionKey, nil
}

func (m *mockSessionStore) GetSession(sessionKey string) (*Session, error) {
	session, ok := m.sessions[sessionKey]
	if !ok {
		return nil, errors.New("session not found")
	}
	return session, nil
}

func (m *mockSessionStore) ValidateSession(sessionKey, userID string) error {
	session, ok := m.sessions[sessionKey]
	if !ok {
		return errors.New("session not found")
	}
	if session.UserID != userID {
		return errors.New("user ID mismatch")
	}
	return nil
}

func (m *mockSessionStore) EndSession(sessionKey string) error {
	delete(m.sessions, sessionKey)
	return nil
}

func (m *mockSessionStore) ExpireSession(sessionKey string) {
	delete(m.sessions, sessionKey)
}

func (m *mockSessionStore) ResetSessionTimer(sessionKey string) {}

func (m *mockSessionStore) StartSessionTimer(sessionKey string) {}

// mockSemaphore is a mock implementation of Semaphore for testing.
type mockSemaphore struct {
	acquired bool
}

func (m *mockSemaphore) Acquire(ctx context.Context) error {
	m.acquired = true
	return nil
}

func (m *mockSemaphore) Release() {
	m.acquired = false
}

// TestOAuthFlowCallsMCPServerForAuthURL verifies that the OAuth flow calls
// the MCP server's /auth/url endpoint, not the discovered auth server.
func TestOAuthFlowCallsMCPServerForAuthURL(t *testing.T) {
	const (
		mcpServerURL  = "https://mcp.example.com"
		authServerURL = "https://auth.example.com"
		callbackURL   = "https://broker.example.com/callback"
	)

	var capturedGetAuthURLTarget string

	mockDiscoverer := &mockOAuthDiscoverer{
		sendSyntheticRequestFunc: func(ctx context.Context, url string) (bool, error) {
			if url != mcpServerURL {
				t.Errorf("SendSyntheticRequest called with wrong URL: got %s, want %s", url, mcpServerURL)
			}
			return true, nil
		},
		discoverAuthServerFunc: func(ctx context.Context, url string) (string, error) {
			if url != mcpServerURL {
				t.Errorf("DiscoverAuthServer called with wrong URL: got %s, want %s", url, mcpServerURL)
			}
			return authServerURL, nil
		},
		getAuthURLFunc: func(ctx context.Context, url, callback string) (string, string, error) {
			capturedGetAuthURLTarget = url
			if callback != callbackURL {
				t.Errorf("GetAuthURL called with wrong callback: got %s, want %s", callback, callbackURL)
			}
			return "https://auth.example.com/authorize?...", "code_verifier_123", nil
		},
	}

	mockCache := &mockTokenCache{
		getTokenFunc: func(userID, mcpURL string) (string, bool) {
			return "", false
		},
	}

	sessionStore := newMockSessionStore()
	sessionKey, _ := sessionStore.CreateSession("user123")

	logger := slog.New(slog.NewTextHandler(nil, &slog.HandlerOptions{Level: slog.LevelError + 1}))

	broker := NewTokenBroker(
		sessionStore,
		mockCache,
		mockDiscoverer,
		&mockTokenExchanger{},
		callbackURL,
		30*time.Second,
		logger,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	go func() {
		_, _ = broker.AcquireToken(ctx, sessionKey, "user123", mcpServerURL)
	}()

	time.Sleep(50 * time.Millisecond)

	// GetAuthURL must be called with the MCP server URL (which hosts /auth/url),
	// NOT the discovered auth server URL (e.g. GitHub)
	if capturedGetAuthURLTarget != mcpServerURL {
		t.Errorf("GetAuthURL was called with wrong URL: got %s, want %s (MCP server, not auth server)",
			capturedGetAuthURLTarget, mcpServerURL)
	}

	if capturedGetAuthURLTarget == authServerURL {
		t.Error("CRITICAL: GetAuthURL was called with auth server URL instead of MCP server URL!")
	}
}

// TestCompleteOAuthValidatesUserID verifies that CompleteOAuth validates the user ID
// matches the session owner (defense-in-depth).
func TestCompleteOAuthValidatesUserID(t *testing.T) {
	sessionStore := newMockSessionStore()
	sessionKey, _ := sessionStore.CreateSession("user123")

	// Create a no-op logger for testing
	logger := slog.New(slog.NewTextHandler(nil, &slog.HandlerOptions{Level: slog.LevelError + 1}))

	broker := NewTokenBroker(
		sessionStore,
		&mockTokenCache{},
		&mockOAuthDiscoverer{},
		&mockTokenExchanger{},
		"https://broker.example.com/callback",
		30*time.Second,
		logger,
	)

	// Try to complete OAuth with wrong user ID
	err := broker.CompleteOAuth(sessionKey, "wrong_user", "code123", "state123")
	if err == nil {
		t.Error("Expected error when completing OAuth with wrong user ID, got nil")
	}
	if err != nil && err.Error() != "session validation failed: user ID mismatch" {
		t.Errorf("Expected user ID mismatch error, got: %v", err)
	}

	// Try with correct user ID (should fail for different reason - no active OAuth tx)
	err = broker.CompleteOAuth(sessionKey, "user123", "code123", "state123")
	if err == nil {
		t.Error("Expected error (no active OAuth tx), got nil")
	}
	if err != nil && err.Error() == "session validation failed: user ID mismatch" {
		t.Error("Should not get user ID mismatch error with correct user ID")
	}
}

// Made with Bob
