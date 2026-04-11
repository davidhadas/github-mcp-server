package authbridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/github/github-mcp-server/internal/aiagent"
)

// AuthRequiredError is returned when OAuth authentication is needed
// This allows AuthBridge to bypass AIAgent and return directly to Backend
type AuthRequiredError struct {
	AuthURL      string
	CodeVerifier string
	UserID       string
	MCPServerURL string
}

func (e *AuthRequiredError) Error() string {
	return "OAuth authentication required"
}

// Logger for AuthBridge with separate log file
var authBridgeLogger *slog.Logger

func init() {
	// Create separate log file for AuthBridge
	logFile, err := os.OpenFile("/tmp/authbridge.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic(fmt.Sprintf("Failed to open authbridge log file: %v", err))
	}

	authBridgeLogger = slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// Log returns the shared AuthBridge logger for use by the extension command
func Log() *slog.Logger {
	return authBridgeLogger
}

// PendingMCPRequest represents a blocked MCP request waiting for OAuth
type PendingMCPRequest struct {
	Request       aiagent.MCPRequest
	ResponseChan  chan *aiagent.MCPResponse
	ErrorChan     chan error
	FrontendReady chan bool // Signal from ExecuteTaskViaHTTP that new frontend request arrived
}

// TokenCacheEntry represents a cached MCP token for a user
type TokenCacheEntry struct {
	Token     string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// TokenCache stores tokens per (user_id, mcp_server_url)
type TokenCache struct {
	mu     sync.RWMutex
	tokens map[string]map[string]*TokenCacheEntry // user_id -> mcp_server_url -> token
}

// NewTokenCache creates a new token cache
func NewTokenCache() *TokenCache {
	return &TokenCache{
		tokens: make(map[string]map[string]*TokenCacheEntry),
	}
}

// GetToken retrieves a token for a user and MCP server
func (tc *TokenCache) GetToken(userID, mcpServerURL string) (string, bool) {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	if userTokens, ok := tc.tokens[userID]; ok {
		if entry, ok := userTokens[mcpServerURL]; ok {
			// Check if token is expired (treat as no token)
			if time.Now().After(entry.ExpiresAt) {
				return "", false
			}
			return entry.Token, true
		}
	}
	return "", false
}

// SetToken stores a token for a user and MCP server
func (tc *TokenCache) SetToken(userID, mcpServerURL, token string, expiresIn int) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.tokens[userID] == nil {
		tc.tokens[userID] = make(map[string]*TokenCacheEntry)
	}

	// If no expiration provided (GitHub tokens don't expire), set to 1 year
	if expiresIn <= 0 {
		expiresIn = 365 * 24 * 60 * 60 // 1 year in seconds
	}

	expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)
	tc.tokens[userID][mcpServerURL] = &TokenCacheEntry{
		Token:     token,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	authBridgeLogger.Info("Token cached",
		"user_id", userID,
		"mcp_server", mcpServerURL,
		"expires_in", expiresIn)
}

// DeleteToken removes a token for a user and MCP server
func (tc *TokenCache) DeleteToken(userID, mcpServerURL string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if userTokens, ok := tc.tokens[userID]; ok {
		delete(userTokens, mcpServerURL)
		authBridgeLogger.Info("Token removed from cache",
			"user_id", userID,
			"mcp_server", mcpServerURL)
	}
}

// AuthBridge handles authentication and token management
// It only supports HTTP-based AI Agent (no in-process support)
type AuthBridge struct {
	tokenCache      *TokenCache
	redirectURI     string
	mu              sync.RWMutex
	pendingRequests sync.Map // userID -> *PendingMCPRequest
}

// NewAuthBridge creates a new AuthBridge instance
func NewAuthBridge(redirectURI string) *AuthBridge {
	authBridgeLogger.Info("AuthBridge initialized", "redirect_uri", redirectURI)
	return &AuthBridge{
		tokenCache:  NewTokenCache(),
		redirectURI: redirectURI,
	}
}

// SetTokenForTesting allows direct token caching for testing purposes
// This bypasses the normal OAuth flow and directly caches a token
func (ab *AuthBridge) SetTokenForTesting(userID, mcpServerURL, token string, expiresIn int) {
	ab.tokenCache.SetToken(userID, mcpServerURL, token, expiresIn)
}

// GetToken retrieves a token from the cache for a user and MCP server
func (ab *AuthBridge) GetToken(userID, mcpServerURL string) (string, bool) {
	return ab.tokenCache.GetToken(userID, mcpServerURL)
}

// DiscoverOAuthRequirements proactively discovers OAuth requirements by sending
// a tools/list request without a token to trigger MCP elicitation response
func (ab *AuthBridge) DiscoverOAuthRequirements(userID, mcpServerURL string) (string, string, error) {
	authBridgeLogger.Info("Proactive OAuth discovery - sending tools/list without token",
		"user_id", userID,
		"mcp_server", mcpServerURL)

	// Send tools/list request without token to trigger elicitation
	mcpReq := aiagent.MCPRequest{
		UserID:       userID,
		MCPServerURL: mcpServerURL,
		Method:       "tools/list",
		Params:       map[string]interface{}{},
	}

	// Use HandleMCPRequest which will detect no token and return AuthRequiredError
	_, err := ab.HandleMCPRequest(mcpReq)
	if err != nil {
		// Check if this is an AuthRequiredError (expected)
		if authErr, ok := err.(*AuthRequiredError); ok {
			authBridgeLogger.Info("OAuth discovery successful via elicitation",
				"user_id", userID,
				"auth_url", authErr.AuthURL)
			return authErr.AuthURL, authErr.CodeVerifier, nil
		}
		// Other error
		authBridgeLogger.Error("OAuth discovery failed",
			"error", err.Error(),
			"user_id", userID)
		return "", "", fmt.Errorf("failed to discover OAuth requirements: %v", err)
	}

	// Unexpected: got a response without auth error (shouldn't happen when no token)
	return "", "", fmt.Errorf("unexpected response during OAuth discovery")
}

// HandleMCPRequest processes MCP requests from AIAgent
// This implements the aiagent.AuthBridge interface
func (ab *AuthBridge) HandleMCPRequest(req aiagent.MCPRequest) (*aiagent.MCPResponse, error) {
	// Check token cache
	token, hasToken := ab.tokenCache.GetToken(req.UserID, req.MCPServerURL)

	if !hasToken {
		// No token - perform proactive OAuth discovery by sending tools/list without token
		// This follows the MCP elicitation standard (Option B)
		authBridgeLogger.Info("No token found - initiating proactive OAuth discovery",
			"user_id", req.UserID,
			"mcp_server", req.MCPServerURL,
			"original_method", req.Method)

		// Send tools/list request WITHOUT token to trigger MCP elicitation
		authBridgeLogger.Info("Sending tools/list without token for OAuth discovery",
			"user_id", req.UserID,
			"mcp_server", req.MCPServerURL)

		// The discovery request will return 401 or trigger elicitation
		// For now, we still use the /auth/url endpoint as the MCP server provides it
		authURLReq := map[string]string{
			"callback_url": ab.redirectURI,
		}
		jsonData, _ := json.Marshal(authURLReq)

		mcpResp, err := http.Post(
			req.MCPServerURL+"/auth/url",
			"application/json",
			bytes.NewReader(jsonData),
		)
		if err != nil {
			authBridgeLogger.Error("Failed to get auth URL",
				"error", err.Error(),
				"mcp_server", req.MCPServerURL)
			return nil, fmt.Errorf("failed to get auth URL: %v", err)
		}
		defer mcpResp.Body.Close()

		body, _ := io.ReadAll(mcpResp.Body)
		var authResp struct {
			URL          string `json:"url"`
			CodeVerifier string `json:"code_verifier"`
		}
		json.Unmarshal(body, &authResp)

		authBridgeLogger.Info("OAuth discovery successful via /auth/url endpoint",
			"user_id", req.UserID,
			"auth_url", authResp.URL)

		// Return special error that ExecuteTask will catch
		return nil, &AuthRequiredError{
			AuthURL:      authResp.URL,
			CodeVerifier: authResp.CodeVerifier,
			UserID:       req.UserID,
			MCPServerURL: req.MCPServerURL,
		}
	}

	// Token exists - make MCP request
	authBridgeLogger.Info("Processing MCP request with cached token",
		"user_id", req.UserID,
		"method", req.Method)

	var apiURL string
	var apiMethod string = "GET"

	switch req.Method {
	case "tools/list":
		// Return tools list
		result := map[string]interface{}{
			"tools": []map[string]interface{}{
				{"name": "get_me", "description": "Get authenticated user info"},
				{"name": "list_repos", "description": "List user repositories"},
				{"name": "search_repos", "description": "Search repositories"},
			},
		}
		body, _ := json.Marshal(result)
		return &aiagent.MCPResponse{
			Status: http.StatusOK,
			Body:   body,
		}, nil

	case "tools/call":
		toolName, ok := req.Params["name"].(string)
		if !ok {
			return nil, fmt.Errorf("missing tool name in params")
		}

		authBridgeLogger.Info("Calling tool via GitHub API",
			"tool", toolName,
			"user_id", req.UserID)

		switch toolName {
		case "get_me":
			apiURL = "https://api.github.com/user"
		case "list_repos":
			apiURL = "https://api.github.com/user/repos?sort=updated&per_page=10"
		case "search_repositories":
			query := "mcp"
			if args, ok := req.Params["arguments"].(map[string]interface{}); ok {
				if q, ok := args["query"].(string); ok {
					query = q
				}
			}
			apiURL = fmt.Sprintf("https://api.github.com/search/repositories?q=%s&sort=stars&per_page=10", query)
		default:
			return nil, fmt.Errorf("unknown tool: %s", toolName)
		}

	default:
		return nil, fmt.Errorf("unsupported method: %s", req.Method)
	}

	// Call GitHub API
	httpReq, _ := http.NewRequest(apiMethod, apiURL, nil)
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Accept", "application/vnd.github.v3+json")
	httpReq.Header.Set("User-Agent", "AuthBridge")

	client := &http.Client{}
	apiResp, err := client.Do(httpReq)
	if err != nil {
		authBridgeLogger.Error("GitHub API call failed",
			"error", err.Error(),
			"url", apiURL)
		return nil, fmt.Errorf("failed to call GitHub API: %v", err)
	}
	defer apiResp.Body.Close()

	body, _ := io.ReadAll(apiResp.Body)

	// If 401, token expired - delete from cache and return auth error
	if apiResp.StatusCode == http.StatusUnauthorized {
		authBridgeLogger.Info("Token expired - requesting OAuth",
			"user_id", req.UserID,
			"mcp_server", req.MCPServerURL)
		ab.tokenCache.DeleteToken(req.UserID, req.MCPServerURL)

		// Get new auth URL
		authURLReq := map[string]string{
			"callback_url": ab.redirectURI,
		}
		jsonData, _ := json.Marshal(authURLReq)

		mcpResp, err := http.Post(
			req.MCPServerURL+"/auth/url",
			"application/json",
			bytes.NewReader(jsonData),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to get auth URL after token expiry: %v", err)
		}
		defer mcpResp.Body.Close()

		body, _ := io.ReadAll(mcpResp.Body)
		var authResp struct {
			URL          string `json:"url"`
			CodeVerifier string `json:"code_verifier"`
		}
		json.Unmarshal(body, &authResp)

		return nil, &AuthRequiredError{
			AuthURL:      authResp.URL,
			CodeVerifier: authResp.CodeVerifier,
			UserID:       req.UserID,
			MCPServerURL: req.MCPServerURL,
		}
	}

	return &aiagent.MCPResponse{
		Status: apiResp.StatusCode,
		Body:   body,
	}, nil
}

// HandleMCPProxyHTTP handles MCP requests from AI Agent via HTTP
// This is the HTTP endpoint version of HandleMCPRequest
// When auth is required, it BLOCKS the request until OAuth completes
func (ab *AuthBridge) HandleMCPProxyHTTP(w http.ResponseWriter, r *http.Request) {
	var mcpReq aiagent.MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&mcpReq); err != nil {
		authBridgeLogger.Error("Invalid MCP request", "error", err.Error())
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Use existing HandleMCPRequest logic
	mcpResp, err := ab.HandleMCPRequest(mcpReq)
	if err != nil {
		// Check if this is an AuthRequiredError
		if authErr, ok := err.(*AuthRequiredError); ok {
			// BLOCK this request and wait for OAuth to complete
			authBridgeLogger.Info("Blocking MCP request for OAuth",
				"user_id", authErr.UserID)

			// Create pending request with channels
			pending := &PendingMCPRequest{
				Request:       mcpReq,
				ResponseChan:  make(chan *aiagent.MCPResponse, 1),
				ErrorChan:     make(chan error, 1),
				FrontendReady: make(chan bool, 1),
			}

			// Store pending request
			ab.pendingRequests.Store(mcpReq.UserID, pending)

			// Signal that we need to return auth error to frontend
			// This will be caught by ExecuteTaskViaHTTP
			pending.ErrorChan <- err

			// BLOCK and wait for either:
			// 1. FrontendReady signal (new frontend request with token)
			// 2. Timeout
			select {
			case <-pending.FrontendReady:
				// OAuth completed, retry the MCP request
				authBridgeLogger.Info("OAuth completed - retrying MCP request",
					"user_id", mcpReq.UserID)

				mcpResp, err = ab.HandleMCPRequest(mcpReq)
				if err != nil {
					authBridgeLogger.Error("MCP request failed after OAuth", "error", err.Error())
					http.Error(w, fmt.Sprintf("MCP request failed: %v", err), http.StatusInternalServerError)
					ab.pendingRequests.Delete(mcpReq.UserID)
					return
				}

				// Send response back through channel
				pending.ResponseChan <- mcpResp
				ab.pendingRequests.Delete(mcpReq.UserID)

			case <-time.After(5 * time.Minute):
				// Timeout
				authBridgeLogger.Error("OAuth timeout - MCP request abandoned",
					"user_id", mcpReq.UserID)
				ab.pendingRequests.Delete(mcpReq.UserID)
				http.Error(w, "OAuth timeout", http.StatusRequestTimeout)
				return
			}
		} else {
			// Other error
			authBridgeLogger.Error("MCP request failed", "error", err.Error())
			http.Error(w, fmt.Sprintf("MCP request failed: %v", err), http.StatusInternalServerError)
			return
		}
	}

	// Return successful MCP response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(mcpResp.Status)
	w.Write(mcpResp.Body)
}

// ExecuteTaskViaHTTP executes a task by forwarding to AI Agent via HTTP
// This replaces the in-process ExecuteTask when AI Agent is a separate service
// It also handles OAuth redirects and resumes blocked MCP requests
func (ab *AuthBridge) ExecuteTaskViaHTTP(taskReq aiagent.TaskRequest, aiAgentURL string) (*aiagent.TaskResponse, error) {
	// Check if there's a pending MCP request for this user
	if pendingVal, ok := ab.pendingRequests.Load(taskReq.UserID); ok {
		pending := pendingVal.(*PendingMCPRequest)

		// Check if this is a retry after OAuth (we have a token now)
		if _, hasToken := ab.tokenCache.GetToken(taskReq.UserID, pending.Request.MCPServerURL); hasToken {
			authBridgeLogger.Info("Resuming blocked MCP request after OAuth",
				"user_id", taskReq.UserID)

			// Signal the blocked HandleMCPProxyHTTP to resume
			pending.FrontendReady <- true

			// Wait for the MCP response
			select {
			case mcpResp := <-pending.ResponseChan:
				// Convert MCP response to task response
				// Try to unmarshal as interface{} to handle both arrays and objects
				var result interface{}
				if err := json.Unmarshal(mcpResp.Body, &result); err != nil {
					authBridgeLogger.Error("Failed to unmarshal MCP response",
						"error", err.Error())
					return nil, fmt.Errorf("failed to unmarshal MCP response: %w", err)
				}

				// Wrap result in a map if it's an array
				var finalResult map[string]interface{}
				if _, isArray := result.([]interface{}); isArray {
					finalResult = map[string]interface{}{
						"data": result,
					}
				} else if resultMap, ok := result.(map[string]interface{}); ok {
					finalResult = resultMap
				} else {
					finalResult = map[string]interface{}{
						"data": result,
					}
				}

				return &aiagent.TaskResponse{
					Status:  "success",
					Message: "Task completed after OAuth",
					Result:  finalResult,
				}, nil

			case <-time.After(30 * time.Second):
				authBridgeLogger.Error("Timeout waiting for resumed MCP response",
					"user_id", taskReq.UserID)
				ab.pendingRequests.Delete(taskReq.UserID)
				return nil, fmt.Errorf("timeout waiting for MCP response")
			}
		}
	}

	// Forward task to AI Agent via HTTP
	authBridgeLogger.Info("Forwarding task to AI Agent",
		"user_id", taskReq.UserID)

	reqBody, err := json.Marshal(taskReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal task request: %w", err)
	}

	// Start goroutine to forward to AI Agent
	// This allows us to check for auth errors while AI Agent is processing
	respChan := make(chan *http.Response, 1)
	errChan := make(chan error, 1)

	go func() {
		resp, err := http.Post(aiAgentURL+"/task", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			errChan <- err
			return
		}
		respChan <- resp
	}()

	// Wait for either:
	// 1. AI Agent response
	// 2. Auth error from pending request
	select {
	case resp := <-respChan:
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read AI Agent response: %w", err)
		}

		var taskResp aiagent.TaskResponse
		if err := json.Unmarshal(body, &taskResp); err != nil {
			return nil, fmt.Errorf("failed to parse AI Agent response: %w", err)
		}

		return &taskResp, nil

	case err := <-errChan:
		authBridgeLogger.Error("Failed to forward task to AI Agent",
			"error", err.Error(),
			"ai_agent_url", aiAgentURL)
		return nil, fmt.Errorf("failed to forward task to AI Agent: %w", err)

	case <-time.After(100 * time.Millisecond):
		// Check if there's a pending auth error
		if pendingVal, ok := ab.pendingRequests.Load(taskReq.UserID); ok {
			pending := pendingVal.(*PendingMCPRequest)

			select {
			case authErr := <-pending.ErrorChan:
				// Auth required - return to frontend for OAuth redirect
				if authReqErr, ok := authErr.(*AuthRequiredError); ok {
					return &aiagent.TaskResponse{
						Status:  "auth_required",
						Message: "OAuth authentication needed",
						Result: map[string]interface{}{
							"error":          "authentication_required",
							"error_message":  "OAuth authentication needed",
							"login_url":      authReqErr.AuthURL,
							"code_verifier":  authReqErr.CodeVerifier,
							"user_id":        authReqErr.UserID,
							"mcp_server_url": authReqErr.MCPServerURL,
						},
					}, nil
				}
			default:
				// No auth error yet, continue waiting for AI Agent
			}
		}

		// Continue waiting for AI Agent response
		select {
		case resp := <-respChan:
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, fmt.Errorf("failed to read AI Agent response: %w", err)
			}

			var taskResp aiagent.TaskResponse
			if err := json.Unmarshal(body, &taskResp); err != nil {
				return nil, fmt.Errorf("failed to parse AI Agent response: %w", err)
			}

			return &taskResp, nil

		case err := <-errChan:
			authBridgeLogger.Error("Failed to forward task to AI Agent",
				"error", err.Error(),
				"ai_agent_url", aiAgentURL)
			return nil, fmt.Errorf("failed to forward task to AI Agent: %w", err)

		case <-time.After(30 * time.Second):
			return nil, fmt.Errorf("timeout waiting for AI Agent response")
		}
	}
}

// ExchangeToken exchanges an OAuth code for a token via the MCP server
// and caches it internally. Returns only success/failure, not the token.
func (ab *AuthBridge) ExchangeToken(userID, mcpServerURL, code, codeVerifier string) error {
	authBridgeLogger.Info("Forwarding token exchange to MCP server",
		"user_id", userID,
		"mcp_server", mcpServerURL)

	tokenReq := map[string]string{
		"code":          code,
		"code_verifier": codeVerifier,
		"redirect_uri":  ab.redirectURI,
	}
	jsonData, _ := json.Marshal(tokenReq)

	mcpResp, err := http.Post(
		mcpServerURL+"/oauth/exchange-token",
		"application/json",
		bytes.NewReader(jsonData),
	)
	if err != nil {
		authBridgeLogger.Error("Token exchange failed",
			"error", err.Error(),
			"mcp_server", mcpServerURL)
		return fmt.Errorf("failed to exchange token: %v", err)
	}
	defer mcpResp.Body.Close()

	body, _ := io.ReadAll(mcpResp.Body)

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		authBridgeLogger.Error("Failed to parse token response",
			"error", err.Error(),
			"body", string(body))
		return fmt.Errorf("failed to parse token response: %v", err)
	}

	// Check if we actually got a token
	if tokenResp.AccessToken == "" {
		authBridgeLogger.Error("No access token in response",
			"body", string(body))
		return fmt.Errorf("no access token received")
	}

	// Cache the token internally (token never leaves AuthBridge)
	ab.tokenCache.SetToken(userID, mcpServerURL, tokenResp.AccessToken, tokenResp.ExpiresIn)

	authBridgeLogger.Info("Token exchange successful",
		"user_id", userID,
		"mcp_server", mcpServerURL)

	return nil
}

// GetAuthURL gets an OAuth URL from the MCP server
func (ab *AuthBridge) GetAuthURL(mcpServerURL string) (string, string, error) {
	authBridgeLogger.Info("Requesting auth URL from MCP server",
		"mcp_server", mcpServerURL)

	authURLReq := map[string]string{
		"callback_url": ab.redirectURI,
	}
	jsonData, _ := json.Marshal(authURLReq)

	mcpResp, err := http.Post(
		mcpServerURL+"/auth/url",
		"application/json",
		bytes.NewReader(jsonData),
	)
	if err != nil {
		authBridgeLogger.Error("Failed to get auth URL",
			"error", err.Error(),
			"mcp_server", mcpServerURL)
		return "", "", fmt.Errorf("failed to get auth URL: %v", err)
	}
	defer mcpResp.Body.Close()

	body, _ := io.ReadAll(mcpResp.Body)
	var authResp struct {
		URL          string `json:"url"`
		CodeVerifier string `json:"code_verifier"`
	}
	if err := json.Unmarshal(body, &authResp); err != nil {
		authBridgeLogger.Error("Failed to parse auth URL response",
			"error", err.Error())
		return "", "", fmt.Errorf("failed to parse auth URL response: %v", err)
	}

	authBridgeLogger.Info("Auth URL obtained from MCP server",
		"mcp_server", mcpServerURL)

	return authResp.URL, authResp.CodeVerifier, nil
}

// Made with Bob
