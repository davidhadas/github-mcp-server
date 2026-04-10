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

// ListUserTokens returns all MCP servers a user has tokens for
func (tc *TokenCache) ListUserTokens(userID string) []string {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	var servers []string
	if userTokens, ok := tc.tokens[userID]; ok {
		for server := range userTokens {
			servers = append(servers, server)
		}
	}
	return servers
}

// AuthBridge handles authentication and token management
// It implements the aiagent.AuthBridge interface and wraps AIAgent
type AuthBridge struct {
	tokenCache  *TokenCache
	redirectURI string
	agents      sync.Map // userID -> *aiagent.AIAgent
	mu          sync.RWMutex
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
	authBridgeLogger.Info("Token set for testing", "user_id", userID, "mcp_server", mcpServerURL)
}

// GetOrCreateAgent returns an existing agent or creates a new one for the user
func (ab *AuthBridge) GetOrCreateAgent(userID string) *aiagent.AIAgent {
	if agent, ok := ab.agents.Load(userID); ok {
		return agent.(*aiagent.AIAgent)
	}

	authBridgeLogger.Info("Creating new AIAgent instance", "user_id", userID)
	agent := aiagent.NewAIAgent(ab, userID)
	ab.agents.Store(userID, agent)
	return agent
}

// ExecuteTask executes a task through the wrapped AIAgent
// This is the main entry point from Backend
func (ab *AuthBridge) ExecuteTask(taskReq aiagent.TaskRequest) (*aiagent.TaskResponse, error) {
	authBridgeLogger.Info("← Received task request from Backend",
		"user_id", taskReq.UserID,
		"task", taskReq.Task)

	// Get or create agent for this user
	agent := ab.GetOrCreateAgent(taskReq.UserID)

	// Execute task through AIAgent
	authBridgeLogger.Info("→ Forwarding task to AIAgent",
		"user_id", taskReq.UserID)

	result, err := agent.ExecuteTask(taskReq)

	if err != nil {
		// Check if this is an AuthRequiredError
		if authErr, ok := err.(*AuthRequiredError); ok {
			// Auth required - return directly to Backend (bypassing AIAgent's normal flow)
			authBridgeLogger.Info("← Caught AuthRequiredError, returning directly to Backend",
				"user_id", authErr.UserID)

			return &aiagent.TaskResponse{
				Status:  "auth_required",
				Message: "OAuth authentication needed",
				Result: map[string]interface{}{
					"error":         "authentication_required",
					"error_message": "OAuth authentication needed",
					"login_url":     authErr.AuthURL,
					"code_verifier": authErr.CodeVerifier,
					"user_id":       authErr.UserID,
				},
			}, nil
		}

		// Other error
		authBridgeLogger.Error("← AIAgent returned error",
			"error", err.Error(),
			"user_id", taskReq.UserID)
		return nil, err
	}

	authBridgeLogger.Info("← Received result from AIAgent",
		"user_id", taskReq.UserID,
		"status", result.Status)

	return result, nil
}

// GetTokenCache returns the token cache
func (ab *AuthBridge) GetTokenCache() *TokenCache {
	return ab.tokenCache
}

// HandleMCPRequest processes MCP requests from AIAgent
// This implements the aiagent.AuthBridge interface
func (ab *AuthBridge) HandleMCPRequest(req aiagent.MCPRequest) (*aiagent.MCPResponse, error) {
	authBridgeLogger.Info("← Received MCP request from AIAgent",
		"user_id", req.UserID,
		"mcp_server", req.MCPServerURL,
		"method", req.Method)

	// Check token cache
	token, hasToken := ab.tokenCache.GetToken(req.UserID, req.MCPServerURL)

	if !hasToken {
		// No token - get auth URL from MCP server
		authBridgeLogger.Info("No token available, requesting auth URL from MCP server",
			"user_id", req.UserID,
			"mcp_server", req.MCPServerURL)

		authURLReq := map[string]string{
			"redirect_uri": ab.redirectURI,
		}
		jsonData, _ := json.Marshal(authURLReq)

		mcpResp, err := http.Post(
			req.MCPServerURL+"/auth/url",
			"application/json",
			bytes.NewReader(jsonData),
		)
		if err != nil {
			authBridgeLogger.Error("Failed to get auth URL from MCP server",
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

		authBridgeLogger.Info("Auth URL obtained from MCP server",
			"user_id", req.UserID,
			"mcp_server", req.MCPServerURL,
			"has_url", authResp.URL != "")

		authBridgeLogger.Info("→ Returning AuthRequiredError (will bypass AIAgent)",
			"user_id", req.UserID,
			"has_auth_url", authResp.URL != "")

		// Return special error that ExecuteTask will catch
		return nil, &AuthRequiredError{
			AuthURL:      authResp.URL,
			CodeVerifier: authResp.CodeVerifier,
			UserID:       req.UserID,
		}
	}

	// Token exists - make MCP request
	authBridgeLogger.Info("Using cached token for MCP request",
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
		authBridgeLogger.Info("→ Returning tools list to AIAgent",
			"user_id", req.UserID,
			"tool_count", 3)
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

	// If 401, token expired - delete from cache
	if apiResp.StatusCode == http.StatusUnauthorized {
		authBridgeLogger.Info("Token expired, removing from cache",
			"user_id", req.UserID,
			"mcp_server", req.MCPServerURL)
		ab.tokenCache.DeleteToken(req.UserID, req.MCPServerURL)

		authBridgeLogger.Info("→ Returning auth required to AIAgent",
			"user_id", req.UserID)
		return &aiagent.MCPResponse{
			Status:    http.StatusUnauthorized,
			NeedsAuth: true,
		}, nil
	}

	authBridgeLogger.Info("→ Returning MCP response to AIAgent",
		"user_id", req.UserID,
		"status", apiResp.StatusCode,
		"body_size", len(body))

	return &aiagent.MCPResponse{
		Status: apiResp.StatusCode,
		Body:   body,
	}, nil
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

	// Log raw response for debugging
	authBridgeLogger.Info("Received token response from MCP server",
		"status", mcpResp.StatusCode,
		"body", string(body))

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

	authBridgeLogger.Info("Token exchange successful and cached",
		"user_id", userID,
		"mcp_server", mcpServerURL,
		"expires_in", tokenResp.ExpiresIn)

	return nil
}

// GetAuthURL gets an OAuth URL from the MCP server
func (ab *AuthBridge) GetAuthURL(mcpServerURL string) (string, string, error) {
	authBridgeLogger.Info("Requesting auth URL from MCP server",
		"mcp_server", mcpServerURL)

	authURLReq := map[string]string{
		"redirect_uri": ab.redirectURI,
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
