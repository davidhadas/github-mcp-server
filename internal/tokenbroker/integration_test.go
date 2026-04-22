package tokenbroker_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/github/github-mcp-server/internal/tokenbroker/api"
	"github.com/github/github-mcp-server/internal/tokenbroker/cache"
	"github.com/github/github-mcp-server/internal/tokenbroker/core"
	"github.com/github/github-mcp-server/internal/tokenbroker/oauthflow"
	"github.com/github/github-mcp-server/internal/tokenbroker/session"
	"github.com/go-chi/chi/v5"
)

// --- Fake MCP Server ---

type fakeMCPRecorder struct {
	mu    sync.Mutex
	calls []string
	// Captured from /oauth/exchange-token
	lastExchangeCode     string
	lastExchangeVerifier string
}

func (r *fakeMCPRecorder) record(endpoint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, endpoint)
}

func (r *fakeMCPRecorder) getCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]string, len(r.calls))
	copy(result, r.calls)
	return result
}

func (r *fakeMCPRecorder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func setupFakeMCPServer(t *testing.T) (*httptest.Server, *fakeMCPRecorder) {
	t.Helper()
	recorder := &fakeMCPRecorder{}

	mux := chi.NewRouter()

	// POST /mcp — returns 401 to trigger OAuth elicitation
	mux.Post("/mcp", func(w http.ResponseWriter, r *http.Request) {
		recorder.record("POST /mcp")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	})

	// GET /.well-known/oauth-protected-resource — returns discovery metadata
	mux.Get("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		recorder.record("GET /.well-known/oauth-protected-resource")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"authorization_servers": []string{"https://github.com/login/oauth"},
		})
	})

	// POST /auth/url — returns authorization URL with embedded state + code_verifier
	mux.Post("/auth/url", func(w http.ResponseWriter, r *http.Request) {
		recorder.record("POST /auth/url")

		var req struct {
			CallbackURL string `json:"callback_url"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"url":           "https://github.com/login/oauth/authorize?client_id=test&state=test_state_abc&redirect_uri=" + url.QueryEscape(req.CallbackURL),
			"code_verifier": "test_code_verifier_xyz",
		})
	})

	// POST /oauth/exchange-token — exchanges code for token
	mux.Post("/oauth/exchange-token", func(w http.ResponseWriter, r *http.Request) {
		recorder.record("POST /oauth/exchange-token")

		var req struct {
			Code         string `json:"code"`
			CodeVerifier string `json:"code_verifier"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		recorder.mu.Lock()
		recorder.lastExchangeCode = req.Code
		recorder.lastExchangeVerifier = req.CodeVerifier
		recorder.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"access_token": "gho_test_token_abc",
			"token_type":   "bearer",
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, recorder
}

// --- Token Broker Server ---

func setupTokenBrokerServer(t *testing.T, mcpServerURL string, waitTimeout time.Duration) *httptest.Server {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	clock := &core.RealClock{}

	tokenCache := cache.NewTokenCache(clock)
	sessionMgr := session.NewSessionManager(60*time.Second, 5, clock, logger)
	t.Cleanup(sessionMgr.Shutdown)

	discoverer := oauthflow.NewDiscoverer(logger)
	exchanger := oauthflow.NewTokenExchanger(logger)

	broker := core.NewTokenBroker(
		sessionMgr,
		tokenCache,
		discoverer,
		exchanger,
		"http://test-backend/callback",
		waitTimeout,
		logger,
	)

	handler := api.NewHandler(broker, sessionMgr, logger)

	r := chi.NewRouter()
	handler.RegisterRoutes(r)

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// --- HTTP helpers ---

func brokerCreateSession(t *testing.T, brokerURL, userID string) string {
	t.Helper()

	req, err := http.NewRequest("POST", brokerURL+"/sessions", nil)
	if err != nil {
		t.Fatalf("creating session request: %v", err)
	}
	req.Header.Set("X-User-ID", userID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("creating session: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create session returned %d: %s", resp.StatusCode, body)
	}

	var result struct {
		OAuthSessionKey string `json:"oauth_session_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decoding session response: %v", err)
	}
	if result.OAuthSessionKey == "" {
		t.Fatal("empty session key")
	}
	return result.OAuthSessionKey
}

func brokerRequestToken(t *testing.T, brokerURL, sessionKey, userID, mcpServerURL string) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequest("POST", brokerURL+"/sessions/"+sessionKey+"/token", nil)
	if err != nil {
		t.Fatalf("creating token request: %v", err)
	}
	req.Header.Set("X-User-ID", userID)
	req.Header.Set("X-Mcp-Server-Url", mcpServerURL)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("requesting token: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func brokerPollEvents(t *testing.T, brokerURL, sessionKey, userID string) *core.Event {
	t.Helper()

	req, err := http.NewRequest("POST", brokerURL+"/sessions/"+sessionKey+"/events", nil)
	if err != nil {
		t.Fatalf("creating events request: %v", err)
	}
	req.Header.Set("X-User-ID", userID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("polling events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("poll events returned %d: %s", resp.StatusCode, body)
	}

	var event core.Event
	if err := json.NewDecoder(resp.Body).Decode(&event); err != nil {
		t.Fatalf("decoding event: %v", err)
	}
	return &event
}

func brokerCompleteOAuth(t *testing.T, brokerURL, sessionKey, userID, code, state string) *http.Response {
	t.Helper()

	u := fmt.Sprintf("%s/sessions/%s/events?code=%s&state=%s", brokerURL, sessionKey, url.QueryEscape(code), url.QueryEscape(state))
	req, err := http.NewRequest("POST", u, nil)
	if err != nil {
		t.Fatalf("creating OAuth completion request: %v", err)
	}
	req.Header.Set("X-User-ID", userID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("completing OAuth: %v", err)
	}
	resp.Body.Close()
	return resp
}

func brokerEndSession(t *testing.T, brokerURL, sessionKey, userID string) *http.Response {
	t.Helper()

	req, err := http.NewRequest("POST", brokerURL+"/sessions/"+sessionKey+"/end", nil)
	if err != nil {
		t.Fatalf("creating end session request: %v", err)
	}
	req.Header.Set("X-User-ID", userID)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ending session: %v", err)
	}
	resp.Body.Close()
	return resp
}

// extractStateFromAuthURL parses the state parameter from an authorization URL.
func extractStateFromAuthURL(authURL string) (string, error) {
	u, err := url.Parse(authURL)
	if err != nil {
		return "", err
	}
	state := u.Query().Get("state")
	if state == "" {
		return "", fmt.Errorf("no state parameter in auth URL")
	}
	return state, nil
}

// doFullOAuthFlow performs a complete OAuth flow and returns the token.
// This is reusable across tests.
func doFullOAuthFlow(t *testing.T, brokerURL, sessionKey, userID, mcpServerURL string) string {
	t.Helper()

	type tokenResult struct {
		resp *http.Response
		body string
	}

	tokenCh := make(chan tokenResult, 1)

	// Goroutine A: AuthBridge requests token (blocks until OAuth completes)
	go func() {
		resp, body := brokerRequestToken(t, brokerURL, sessionKey, userID, mcpServerURL)
		tokenCh <- tokenResult{resp, body}
	}()

	// Give the token request time to reach the broker and start the OAuth flow
	time.Sleep(200 * time.Millisecond)

	// Goroutine B (inline): Backend polls for events
	event := brokerPollEvents(t, brokerURL, sessionKey, userID)
	if event == nil {
		t.Fatal("expected oauth_required event, got nil (timeout)")
	}
	if event.Type != "oauth_required" {
		t.Fatalf("expected event type 'oauth_required', got %q", event.Type)
	}
	if event.AuthURL == "" {
		t.Fatal("oauth_required event missing auth_url")
	}

	// Extract state from auth URL
	state, err := extractStateFromAuthURL(event.AuthURL)
	if err != nil {
		t.Fatalf("extracting state from auth_url: %v", err)
	}

	// Complete OAuth with code and state
	completeResp := brokerCompleteOAuth(t, brokerURL, sessionKey, userID, "test_auth_code", state)
	if completeResp.StatusCode != http.StatusOK {
		t.Fatalf("OAuth completion returned %d", completeResp.StatusCode)
	}

	// Wait for token result
	select {
	case result := <-tokenCh:
		if result.resp.StatusCode != http.StatusOK {
			t.Fatalf("token request returned %d: %s", result.resp.StatusCode, result.body)
		}
		var tokenResp struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(result.body), &tokenResp); err != nil {
			t.Fatalf("decoding token response: %v", err)
		}
		return tokenResp.Token

	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for token response")
		return ""
	}
}

// --- Tests ---

func TestIntegration_FullOAuthFlow(t *testing.T) {
	mcpServer, recorder := setupFakeMCPServer(t)
	brokerServer := setupTokenBrokerServer(t, mcpServer.URL, 30*time.Second)

	userID := "demo-user"
	sessionKey := brokerCreateSession(t, brokerServer.URL, userID)

	token := doFullOAuthFlow(t, brokerServer.URL, sessionKey, userID, mcpServer.URL)

	if token != "gho_test_token_abc" {
		t.Errorf("expected token 'gho_test_token_abc', got %q", token)
	}

	// Verify the fake MCP server received all expected calls
	calls := recorder.getCalls()
	expectedCalls := []string{
		"POST /mcp",
		"GET /.well-known/oauth-protected-resource",
		"POST /auth/url",
		"POST /oauth/exchange-token",
	}
	if len(calls) != len(expectedCalls) {
		t.Fatalf("expected %d MCP server calls, got %d: %v", len(expectedCalls), len(calls), calls)
	}
	for i, expected := range expectedCalls {
		if calls[i] != expected {
			t.Errorf("call %d: expected %q, got %q", i, expected, calls[i])
		}
	}

	// Verify the exchange received correct parameters
	recorder.mu.Lock()
	exchangeCode := recorder.lastExchangeCode
	exchangeVerifier := recorder.lastExchangeVerifier
	recorder.mu.Unlock()

	if exchangeCode != "test_auth_code" {
		t.Errorf("exchange received wrong code: got %q, want 'test_auth_code'", exchangeCode)
	}
	if exchangeVerifier != "test_code_verifier_xyz" {
		t.Errorf("exchange received wrong code_verifier: got %q, want 'test_code_verifier_xyz'", exchangeVerifier)
	}
}

func TestIntegration_CachedTokenSkipsOAuth(t *testing.T) {
	mcpServer, recorder := setupFakeMCPServer(t)
	brokerServer := setupTokenBrokerServer(t, mcpServer.URL, 30*time.Second)

	userID := "demo-user"
	sessionKey := brokerCreateSession(t, brokerServer.URL, userID)

	// First request: full OAuth flow
	token1 := doFullOAuthFlow(t, brokerServer.URL, sessionKey, userID, mcpServer.URL)
	callsAfterFirst := recorder.callCount()

	// Second request: should use cached token (no new MCP server calls)
	resp, body := brokerRequestToken(t, brokerServer.URL, sessionKey, userID, mcpServer.URL)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cached token request returned %d: %s", resp.StatusCode, body)
	}

	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(body), &tokenResp); err != nil {
		t.Fatalf("decoding cached token response: %v", err)
	}

	if tokenResp.Token != token1 {
		t.Errorf("cached token mismatch: got %q, want %q", tokenResp.Token, token1)
	}

	callsAfterSecond := recorder.callCount()
	if callsAfterSecond != callsAfterFirst {
		t.Errorf("expected no new MCP calls for cached token, but got %d new calls", callsAfterSecond-callsAfterFirst)
	}
}

func TestIntegration_OAuthTimeout(t *testing.T) {
	mcpServer, _ := setupFakeMCPServer(t)
	// Very short wait timeout — OAuth will never complete
	brokerServer := setupTokenBrokerServer(t, mcpServer.URL, 500*time.Millisecond)

	userID := "demo-user"
	sessionKey := brokerCreateSession(t, brokerServer.URL, userID)

	// Start event poller so the oauth_required event can be delivered
	go func() {
		brokerPollEvents(t, brokerServer.URL, sessionKey, userID)
		// Don't complete OAuth — let it timeout
	}()

	// Request token — should timeout
	resp, body := brokerRequestToken(t, brokerServer.URL, sessionKey, userID, mcpServer.URL)
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected token request to fail due to timeout, but got 200")
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d: %s", resp.StatusCode, body)
	}

	if !strings.Contains(body, "oauth_failed") && !strings.Contains(body, "timeout") {
		t.Errorf("expected timeout/oauth_failed error, got: %s", body)
	}
}

func TestIntegration_SessionValidation(t *testing.T) {
	mcpServer, _ := setupFakeMCPServer(t)
	brokerServer := setupTokenBrokerServer(t, mcpServer.URL, 10*time.Second)

	// Create session for user1
	sessionKey := brokerCreateSession(t, brokerServer.URL, "user1")

	// Try to get token with user2 on user1's session
	resp, body := brokerRequestToken(t, brokerServer.URL, sessionKey, "user2", mcpServer.URL)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong user, got %d: %s", resp.StatusCode, body)
	}
}

func TestIntegration_EndSession(t *testing.T) {
	mcpServer, _ := setupFakeMCPServer(t)
	brokerServer := setupTokenBrokerServer(t, mcpServer.URL, 30*time.Second)

	userID := "demo-user"
	sessionKey := brokerCreateSession(t, brokerServer.URL, userID)

	// Complete OAuth flow
	_ = doFullOAuthFlow(t, brokerServer.URL, sessionKey, userID, mcpServer.URL)

	// End session
	endResp := brokerEndSession(t, brokerServer.URL, sessionKey, userID)
	if endResp.StatusCode != http.StatusOK {
		t.Fatalf("end session returned %d", endResp.StatusCode)
	}

	// Try to get token on ended session
	resp, _ := brokerRequestToken(t, brokerServer.URL, sessionKey, userID, mcpServer.URL)
	if resp.StatusCode == http.StatusOK {
		t.Error("expected token request to fail on ended session, but got 200")
	}
}
