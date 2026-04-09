package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/viper"
)

// MCPServer represents an MCP server configuration
type MCPServer struct {
	Name string `mapstructure:"name"`
	URL  string `mapstructure:"url"`
}

// Config holds the OAuth coordinator configuration
type Config struct {
	Port         int         `mapstructure:"port"`
	RedirectURI  string      `mapstructure:"redirect_uri"`
	MCPServers   []MCPServer `mapstructure:"mcp_servers"`
	DemoPagePath string      `mapstructure:"demo_page_path"`
}

// Session represents a user OAuth session
type Session struct {
	MCPServerURL string
	Timestamp    time.Time
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

	expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)
	tc.tokens[userID][mcpServerURL] = &TokenCacheEntry{
		Token:     token,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
}

// DeleteToken removes a token for a user and MCP server
func (tc *TokenCache) DeleteToken(userID, mcpServerURL string) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if userTokens, ok := tc.tokens[userID]; ok {
		delete(userTokens, mcpServerURL)
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

// OAuthCoordinator manages OAuth flows for multiple providers
type OAuthCoordinator struct {
	config     *Config
	sessions   sync.Map // sessionID -> Session
	tokenCache *TokenCache
	mu         sync.RWMutex
}

func main() {
	// Load configuration
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./cmd/oauth-coordinator")

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		log.Fatalf("Error unmarshaling config: %v", err)
	}

	// Validate configuration
	if config.Port == 0 {
		config.Port = 8185
	}
	if config.RedirectURI == "" {
		config.RedirectURI = fmt.Sprintf("http://localhost:%d/callback", config.Port)
	}

	coordinator := &OAuthCoordinator{
		config:     &config,
		tokenCache: NewTokenCache(),
	}

	// Setup HTTP server
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// OAuth endpoints
	r.Post("/auth/url", coordinator.handleAuthURL)
	r.Post("/oauth/exchange-token", coordinator.handleTokenExchange)
	r.Get("/callback", coordinator.handleCallback)

	// Hybrid Approach: MCP call endpoint with token caching
	r.Post("/mcp/call", coordinator.handleMCPCall)

	// Token cache management endpoints
	r.Get("/tokens/status", coordinator.handleTokenStatus)
	r.Delete("/tokens/{user_id}/{mcp_server}", coordinator.handleDeleteToken)

	// Serve demo page if configured
	if config.DemoPagePath != "" {
		r.Get("/demo", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			http.ServeFile(w, r, config.DemoPagePath)
		})
		slog.Info("Demo page registered", "path", config.DemoPagePath)
	}

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	addr := fmt.Sprintf(":%d", config.Port)
	slog.Info("OAuth Coordinator starting (provider-agnostic)", "port", config.Port, "redirect_uri", config.RedirectURI)
	slog.Info("NO credentials stored - all OAuth operations delegated to MCP servers")
	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// corsMiddleware adds CORS headers
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// handleAuthURL forwards the auth URL request to the MCP server
func (c *OAuthCoordinator) handleAuthURL(w http.ResponseWriter, r *http.Request) {
	// Parse request
	var req struct {
		MCPServer   string `json:"mcp_server,omitempty"`
		RedirectURI string `json:"redirect_uri,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Get MCP server URL (use first configured if not specified)
	mcpServerURL := req.MCPServer
	if mcpServerURL == "" && len(c.config.MCPServers) > 0 {
		mcpServerURL = c.config.MCPServers[0].URL
	}
	if mcpServerURL == "" {
		http.Error(w, "No MCP server configured", http.StatusBadRequest)
		return
	}

	// Use configured redirect URI if not provided
	redirectURI := req.RedirectURI
	if redirectURI == "" {
		redirectURI = c.config.RedirectURI
	}

	// Forward request to MCP server
	slog.Info("Forwarding auth URL request to MCP server", "mcp_server", mcpServerURL)

	reqBody := map[string]string{
		"redirect_uri": redirectURI,
	}
	jsonData, _ := json.Marshal(reqBody)

	mcpResp, err := http.Post(
		mcpServerURL+"/auth/url",
		"application/json",
		bytes.NewReader(jsonData),
	)
	if err != nil {
		slog.Error("Failed to forward to MCP server", "error", err)
		http.Error(w, fmt.Sprintf("Failed to contact MCP server: %v", err), http.StatusInternalServerError)
		return
	}
	defer mcpResp.Body.Close()

	// Read response from MCP server
	body, err := io.ReadAll(mcpResp.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read MCP server response: %v", err), http.StatusInternalServerError)
		return
	}

	if mcpResp.StatusCode != http.StatusOK {
		slog.Error("MCP server returned error", "status", mcpResp.StatusCode, "body", string(body))
		http.Error(w, string(body), mcpResp.StatusCode)
		return
	}

	// Parse response to extract state for session tracking
	var authResp map[string]interface{}
	if err := json.Unmarshal(body, &authResp); err == nil {
		if url, ok := authResp["url"].(string); ok {
			// Extract state from URL for session tracking (optional)
			slog.Info("Auth URL generated by MCP server", "has_url", url != "")
		}
	}

	// Forward MCP server response to browser
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(mcpResp.StatusCode)
	w.Write(body)

	slog.Info("Auth URL request forwarded successfully")
}

// handleTokenExchange forwards the token exchange request to the MCP server
// and caches the token for the Hybrid Approach
func (c *OAuthCoordinator) handleTokenExchange(w http.ResponseWriter, r *http.Request) {
	// Parse request
	var req struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
		MCPServerURL string `json:"mcp_server_url,omitempty"`
		UserID       string `json:"user_id,omitempty"` // For Hybrid Approach token caching
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.Code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	if req.CodeVerifier == "" {
		http.Error(w, "Missing code_verifier", http.StatusBadRequest)
		return
	}

	// Get MCP server URL
	mcpServerURL := req.MCPServerURL
	if mcpServerURL == "" && len(c.config.MCPServers) > 0 {
		mcpServerURL = c.config.MCPServers[0].URL
	}
	if mcpServerURL == "" {
		http.Error(w, "No MCP server specified", http.StatusBadRequest)
		return
	}

	// Forward token exchange request to MCP server
	slog.Info("Forwarding token exchange to MCP server", "mcp_server", mcpServerURL)

	reqBody := map[string]string{
		"code":          req.Code,
		"code_verifier": req.CodeVerifier,
	}
	jsonData, _ := json.Marshal(reqBody)

	mcpResp, err := http.Post(
		mcpServerURL+"/oauth/exchange-token",
		"application/json",
		bytes.NewReader(jsonData),
	)
	if err != nil {
		slog.Error("Failed to forward to MCP server", "error", err)
		http.Error(w, fmt.Sprintf("Failed to contact MCP server: %v", err), http.StatusInternalServerError)
		return
	}
	defer mcpResp.Body.Close()

	// Read response from MCP server
	body, err := io.ReadAll(mcpResp.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to read MCP server response: %v", err), http.StatusInternalServerError)
		return
	}

	if mcpResp.StatusCode != http.StatusOK {
		slog.Error("MCP server token exchange failed", "status", mcpResp.StatusCode, "body", string(body))
	} else {
		slog.Info("Token exchange successful (handled by MCP server)")

		// Hybrid Approach: Cache the token if user_id is provided
		if req.UserID != "" {
			var tokenResp struct {
				AccessToken string `json:"access_token"`
				ExpiresIn   int    `json:"expires_in"`
			}
			if err := json.Unmarshal(body, &tokenResp); err == nil && tokenResp.AccessToken != "" {
				// Default to 1 hour if not specified
				expiresIn := tokenResp.ExpiresIn
				if expiresIn == 0 {
					expiresIn = 3600
				}
				c.tokenCache.SetToken(req.UserID, mcpServerURL, tokenResp.AccessToken, expiresIn)
				slog.Info("Token cached for user", "user_id", req.UserID, "mcp_server", mcpServerURL, "expires_in", expiresIn)
			}
		}
	}

	// Forward MCP server response to browser
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(mcpResp.StatusCode)
	w.Write(body)
}

// handleCallback handles OAuth callback (for browser-based flows)
func (c *OAuthCoordinator) handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" || state == "" {
		http.Error(w, "Missing code or state", http.StatusBadRequest)
		return
	}

	// Redirect back to demo page with code and state
	redirectURL := fmt.Sprintf("/demo?code=%s&state=%s", code, state)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// handleMCPCall handles MCP requests with automatic token management (Hybrid Approach)
func (c *OAuthCoordinator) handleMCPCall(w http.ResponseWriter, r *http.Request) {
	// Parse request
	var req struct {
		UserID       string                 `json:"user_id"`
		MCPServerURL string                 `json:"mcp_server_url"`
		Method       string                 `json:"method"`
		Params       map[string]interface{} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if req.UserID == "" {
		http.Error(w, "Missing user_id", http.StatusBadRequest)
		return
	}
	if req.MCPServerURL == "" {
		http.Error(w, "Missing mcp_server_url", http.StatusBadRequest)
		return
	}
	if req.Method == "" {
		http.Error(w, "Missing method", http.StatusBadRequest)
		return
	}

	// Check token cache
	token, hasToken := c.tokenCache.GetToken(req.UserID, req.MCPServerURL)

	if !hasToken {
		// No token cached - return auth required response with login URL
		slog.Info("No token cached for user", "user_id", req.UserID, "mcp_server", req.MCPServerURL)

		// Generate login URL via MCP server's auth/url endpoint
		authURLReq := map[string]string{
			"redirect_uri": c.config.RedirectURI,
		}
		jsonData, _ := json.Marshal(authURLReq)

		mcpResp, err := http.Post(
			req.MCPServerURL+"/auth/url",
			"application/json",
			bytes.NewReader(jsonData),
		)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to get auth URL: %v", err), http.StatusInternalServerError)
			return
		}
		defer mcpResp.Body.Close()

		body, _ := io.ReadAll(mcpResp.Body)
		var authResp struct {
			URL          string `json:"url"`
			CodeVerifier string `json:"code_verifier"`
		}
		json.Unmarshal(body, &authResp)

		// Return 401 with login URL (MCP-standard response)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":         "authentication_required",
			"error_message": "No token cached for this user and MCP server",
			"login_url":     authResp.URL,
			"code_verifier": authResp.CodeVerifier,
			"user_id":       req.UserID,
			"mcp_server":    req.MCPServerURL,
		})
		return
	}

	// Token exists - call GitHub API directly for demo
	slog.Info("Using cached token for GitHub API request", "user_id", req.UserID, "method", req.Method)

	// For demo purposes, we'll call GitHub API directly
	// In production, this would use MCP protocol over SSE
	var apiURL string
	var apiMethod string = "GET"

	switch req.Method {
	case "tools/list":
		// Return a simple list of available "tools" (demo)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tools": []map[string]interface{}{
				{"name": "get_me", "description": "Get authenticated user info"},
				{"name": "list_repos", "description": "List user repositories"},
				{"name": "search_repos", "description": "Search repositories"},
			},
		})
		return

	case "tools/call":
		// Extract tool name from params
		toolName, ok := req.Params["name"].(string)
		if !ok {
			http.Error(w, "Missing tool name in params", http.StatusBadRequest)
			return
		}

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
			http.Error(w, fmt.Sprintf("Unknown tool: %s", toolName), http.StatusBadRequest)
			return
		}

	default:
		http.Error(w, fmt.Sprintf("Unsupported method: %s", req.Method), http.StatusBadRequest)
		return
	}

	// Call GitHub API
	httpReq, _ := http.NewRequest(apiMethod, apiURL, nil)
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Accept", "application/vnd.github.v3+json")
	httpReq.Header.Set("User-Agent", "OAuth-Coordinator-Demo")

	client := &http.Client{}
	apiResp, err := client.Do(httpReq)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to call GitHub API: %v", err), http.StatusInternalServerError)
		return
	}
	defer apiResp.Body.Close()

	// Read response
	body, _ := io.ReadAll(apiResp.Body)

	// If 401, token might be expired - delete from cache
	if apiResp.StatusCode == http.StatusUnauthorized {
		slog.Info("Token expired, removing from cache", "user_id", req.UserID, "mcp_server", req.MCPServerURL)
		c.tokenCache.DeleteToken(req.UserID, req.MCPServerURL)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(apiResp.StatusCode)
	w.Write(body)
}

// handleTokenStatus returns the token cache status for debugging
func (c *OAuthCoordinator) handleTokenStatus(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")

	if userID == "" {
		http.Error(w, "Missing user_id parameter", http.StatusBadRequest)
		return
	}

	servers := c.tokenCache.ListUserTokens(userID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id":     userID,
		"mcp_servers": servers,
		"token_count": len(servers),
	})
}

// handleDeleteToken removes a token from the cache
func (c *OAuthCoordinator) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "user_id")
	mcpServer := chi.URLParam(r, "mcp_server")

	if userID == "" || mcpServer == "" {
		http.Error(w, "Missing user_id or mcp_server", http.StatusBadRequest)
		return
	}

	c.tokenCache.DeleteToken(userID, mcpServer)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":     "deleted",
		"user_id":    userID,
		"mcp_server": mcpServer,
	})
}

// Made with Bob
