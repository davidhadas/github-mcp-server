package core

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// TokenBroker orchestrates token acquisition for OAuth sessions.
type TokenBroker struct {
	sessionStore    SessionStore
	tokenCache      TokenCache
	oauthDiscoverer OAuthDiscoverer
	tokenExchanger  TokenExchanger
	callbackURL     string
	waitTimeout     time.Duration
	logger          *slog.Logger
}

// NewTokenBroker creates a new token broker.
func NewTokenBroker(
	sessionStore SessionStore,
	tokenCache TokenCache,
	oauthDiscoverer OAuthDiscoverer,
	tokenExchanger TokenExchanger,
	callbackURL string,
	waitTimeout time.Duration,
	logger *slog.Logger,
) *TokenBroker {
	return &TokenBroker{
		sessionStore:    sessionStore,
		tokenCache:      tokenCache,
		oauthDiscoverer: oauthDiscoverer,
		tokenExchanger:  tokenExchanger,
		callbackURL:     callbackURL,
		waitTimeout:     waitTimeout,
		logger:          logger,
	}
}

// AcquireToken acquires a token for a user and MCP server.
// This implements the double-checked locking pattern with session semaphore.
func (tb *TokenBroker) AcquireToken(ctx context.Context, sessionKey, userID, mcpServerURL string) (string, error) {
	// Step 1: Check cache (fast path)
	if token, found := tb.tokenCache.GetToken(userID, mcpServerURL); found {
		tb.logger.Debug("Token found in cache (fast path)",
			"user_id", userID,
			"mcp_server", mcpServerURL)
		return token, nil
	}

	// Step 2: Validate session and user
	if err := tb.sessionStore.ValidateSession(sessionKey, userID); err != nil {
		return "", fmt.Errorf("session validation failed: %w", err)
	}

	// Get session
	session, err := tb.sessionStore.GetSession(sessionKey)
	if err != nil {
		return "", fmt.Errorf("failed to get session: %w", err)
	}

	// Step 3: Acquire per-session semaphore
	tb.logger.Debug("Acquiring session semaphore",
		"session_key", sessionKey,
		"user_id", userID,
		"mcp_server", mcpServerURL)

	if err := session.AcquisitionSemaphore.Acquire(ctx); err != nil {
		return "", fmt.Errorf("failed to acquire semaphore: %w", err)
	}
	defer session.AcquisitionSemaphore.Release()

	tb.logger.Debug("Session semaphore acquired",
		"session_key", sessionKey)

	// Step 4: Check cache again (double-check after semaphore)
	if token, found := tb.tokenCache.GetToken(userID, mcpServerURL); found {
		tb.logger.Info("Token found in cache after semaphore acquisition",
			"user_id", userID,
			"mcp_server", mcpServerURL)
		return token, nil
	}

	// Step 5: Token not in cache, initiate OAuth flow
	tb.logger.Info("Token not in cache, initiating OAuth flow",
		"session_key", sessionKey,
		"user_id", userID,
		"mcp_server", mcpServerURL)

	token, err := tb.performOAuthFlow(ctx, session, userID, mcpServerURL)
	if err != nil {
		return "", fmt.Errorf("OAuth flow failed: %w", err)
	}

	return token, nil
}

// performOAuthFlow performs the complete OAuth flow to obtain a token.
func (tb *TokenBroker) performOAuthFlow(ctx context.Context, session *Session, userID, mcpServerURL string) (string, error) {
	// Step 1: Send synthetic request to trigger 401
	tb.logger.Debug("Sending synthetic MCP request",
		"mcp_server", mcpServerURL)

	is401, err := tb.oauthDiscoverer.SendSyntheticRequest(ctx, mcpServerURL)
	if err != nil {
		return "", fmt.Errorf("synthetic request failed: %w", err)
	}

	if !is401 {
		return "", fmt.Errorf("MCP server did not return 401 (OAuth not required?)")
	}

	// Step 2: Discover authorization server
	tb.logger.Debug("Discovering authorization server",
		"mcp_server", mcpServerURL)

	authServerURL, err := tb.oauthDiscoverer.DiscoverAuthServer(ctx, mcpServerURL)
	if err != nil {
		return "", fmt.Errorf("auth server discovery failed: %w", err)
	}

	tb.logger.Info("Authorization server discovered",
		"mcp_server", mcpServerURL,
		"auth_server", authServerURL)

	// Step 3: Get authorization URL and code_verifier from MCP server
	tb.logger.Debug("Requesting authorization URL",
		"mcp_server", mcpServerURL,
		"callback_url", tb.callbackURL)

	authURL, codeVerifier, err := tb.oauthDiscoverer.GetAuthURL(ctx, mcpServerURL, tb.callbackURL)
	if err != nil {
		return "", fmt.Errorf("failed to get auth URL: %w", err)
	}

	tb.logger.Info("Authorization URL obtained",
		"mcp_server", mcpServerURL,
		"auth_url_length", len(authURL))

	// Step 4: Store OAuth transaction in session
	session.ActiveOAuthTx = &OAuthTransaction{
		MCPServerURL:   mcpServerURL,
		AuthURL:        authURL,
		CodeVerifier:   codeVerifier,
		Status:         "waiting",
		CompletionChan: make(chan OAuthCompletion, 1),
	}

	// Step 5: Publish oauth_required event to Backend
	tb.logger.Debug("Publishing oauth_required event",
		"session_key", session.SessionKey)

	event := Event{
		Type:         "oauth_required",
		MCPServerURL: mcpServerURL,
		AuthURL:      authURL,
	}

	select {
	case session.EventWaiters <- event:
		tb.logger.Info("oauth_required event published",
			"session_key", session.SessionKey,
			"mcp_server", mcpServerURL)
	case <-ctx.Done():
		return "", fmt.Errorf("context cancelled while publishing event: %w", ctx.Err())
	default:
		return "", fmt.Errorf("no event waiter available")
	}

	// Step 6: Wait for Backend to complete OAuth (with timeout)
	tb.logger.Info("Waiting for OAuth completion",
		"session_key", session.SessionKey,
		"timeout", tb.waitTimeout)

	waitCtx, cancel := context.WithTimeout(ctx, tb.waitTimeout)
	defer cancel()

	var completion OAuthCompletion
	select {
	case completion = <-session.ActiveOAuthTx.CompletionChan:
		if completion.Error != nil {
			return "", fmt.Errorf("OAuth completion failed: %w", completion.Error)
		}
		tb.logger.Info("OAuth completion received",
			"session_key", session.SessionKey,
			"code_length", len(completion.Code))

	case <-waitCtx.Done():
		session.ActiveOAuthTx.Status = "failed"
		return "", fmt.Errorf("timeout waiting for OAuth completion")

	case <-ctx.Done():
		session.ActiveOAuthTx.Status = "failed"
		return "", fmt.Errorf("context cancelled: %w", ctx.Err())
	}

	// Step 7: Exchange authorization code for token
	tb.logger.Debug("Exchanging authorization code for token",
		"mcp_server", mcpServerURL)

	token, err := tb.tokenExchanger.ExchangeToken(ctx, mcpServerURL, completion.Code, codeVerifier)
	if err != nil {
		session.ActiveOAuthTx.Status = "failed"
		return "", fmt.Errorf("token exchange failed: %w", err)
	}

	session.ActiveOAuthTx.Status = "completed"

	tb.logger.Info("Token obtained successfully",
		"session_key", session.SessionKey,
		"mcp_server", mcpServerURL,
		"token_length", len(token))

	// Step 8: Cache token
	if err := tb.tokenCache.SetToken(userID, mcpServerURL, token); err != nil {
		tb.logger.Error("Failed to cache token",
			"error", err,
			"user_id", userID,
			"mcp_server", mcpServerURL)
		// Don't fail the request, we have the token
	}

	// Step 9: Unblock any other waiters for this MCP server
	if waiters, ok := session.TokenWaiters[mcpServerURL]; ok {
		for _, waiter := range waiters {
			select {
			case waiter <- TokenResult{Token: token, Error: nil}:
			default:
			}
			close(waiter)
		}
		delete(session.TokenWaiters, mcpServerURL)
		tb.logger.Debug("Unblocked token waiters",
			"session_key", session.SessionKey,
			"mcp_server", mcpServerURL,
			"waiter_count", len(waiters))
	}

	// Clear active OAuth transaction
	session.ActiveOAuthTx = nil

	return token, nil
}

// CompleteOAuth completes an OAuth flow with the authorization code and state.
func (tb *TokenBroker) CompleteOAuth(sessionKey, userID, code, state string) error {
	// Validate session ownership (defense-in-depth)
	if err := tb.sessionStore.ValidateSession(sessionKey, userID); err != nil {
		return fmt.Errorf("session validation failed: %w", err)
	}

	session, err := tb.sessionStore.GetSession(sessionKey)
	if err != nil {
		return fmt.Errorf("session not found: %w", err)
	}

	if session.ActiveOAuthTx == nil {
		return fmt.Errorf("no active OAuth transaction")
	}

	tb.logger.Info("Completing OAuth flow",
		"session_key", sessionKey,
		"mcp_server", session.ActiveOAuthTx.MCPServerURL,
		"code_length", len(code),
		"state_length", len(state))

	// Send completion to waiting OAuth flow
	completion := OAuthCompletion{
		Code:  code,
		State: state,
		Error: nil,
	}

	select {
	case session.ActiveOAuthTx.CompletionChan <- completion:
		tb.logger.Debug("OAuth completion sent to waiting flow",
			"session_key", sessionKey)
		return nil
	default:
		return fmt.Errorf("no waiter for OAuth completion")
	}
}

// GetSessionStore returns the session store (for API handlers).
func (tb *TokenBroker) GetSessionStore() SessionStore {
	return tb.sessionStore
}

// Made with Bob
