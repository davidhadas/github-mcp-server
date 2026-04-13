package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/github/github-mcp-server/internal/authbridge"
)

// Build-time variables
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// OAuthCompletionData contains OAuth parameters for token exchange
type OAuthCompletionData struct {
	Code         string
	CodeVerifier string
	MCPServerURL string
}

// SuspendedRequest stores a request that is waiting for OAuth completion
type SuspendedRequest struct {
	OriginalRequest *http.Request
	ResponseWriter  http.ResponseWriter
	UserID          string
	MCPServerURL    string
	Timestamp       time.Time
	CompletionChan  chan *OAuthCompletionData // Receive OAuth params to do token exchange
}

// SidecarProxy intercepts all pod traffic using reverse proxy
type SidecarProxy struct {
	authBridge          *authbridge.AuthBridge
	logger              *slog.Logger
	defaultMCPServer    string
	suspendedRequests   map[string]*SuspendedRequest
	suspendedMutex      sync.RWMutex
	backendWriters      map[string]http.ResponseWriter // Store backend response writers
	backendWritersMutex sync.RWMutex
	aiAgentProxy        *httputil.ReverseProxy
	mcpServerProxy      *httputil.ReverseProxy
}

func main() {
	// Set up structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Get default MCP server URL from environment
	defaultMCPServer := os.Getenv("DEFAULT_MCP_SERVER_URL")
	if defaultMCPServer == "" {
		defaultMCPServer = "http://mcp-server-service:8184"
	}

	// Create AuthBridge instance (no callback URL needed - frontend handles OAuth)
	ab := authbridge.NewAuthBridge("")

	// Parse MCP server URL
	mcpURL, err := url.Parse(defaultMCPServer)
	if err != nil {
		logger.Error("Invalid MCP server URL", "error", err)
		os.Exit(1)
	}

	// Parse AI Agent URL
	aiAgentURL, err := url.Parse("http://127.0.0.1:8186")
	if err != nil {
		logger.Error("Invalid AI Agent URL", "error", err)
		os.Exit(1)
	}

	proxy := &SidecarProxy{
		authBridge:        ab,
		logger:            logger,
		defaultMCPServer:  defaultMCPServer,
		suspendedRequests: make(map[string]*SuspendedRequest),
		backendWriters:    make(map[string]http.ResponseWriter),
		aiAgentProxy:      httputil.NewSingleHostReverseProxy(aiAgentURL),
		mcpServerProxy:    httputil.NewSingleHostReverseProxy(mcpURL),
	}

	// Customize the MCP server proxy to add auth headers
	proxy.mcpServerProxy.Director = proxy.mcpDirector(mcpURL)
	proxy.mcpServerProxy.ModifyResponse = proxy.mcpModifyResponse

	// Create HTTP server on intercept port
	server := &http.Server{
		Addr:    ":15001",
		Handler: proxy,
	}

	logger.Info("AuthBridge sidecar proxy started using httputil.ReverseProxy",
		"port", 15001,
		"mode", "reverse_proxy",
		"default_mcp_server", defaultMCPServer,
		"version", version,
		"commit", commit,
		"build_time", date)

	if err := server.ListenAndServe(); err != nil {
		logger.Error("Server failed", "error", err)
		os.Exit(1)
	}
}

// ServeHTTP implements http.Handler interface
func (p *SidecarProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.logger.Info("Request received",
		"method", r.Method,
		"path", r.URL.Path,
		"host", r.Host,
		"remote_addr", r.RemoteAddr)

	// Handle OAuth completion endpoint
	if strings.HasPrefix(r.URL.Path, "/oauth-complete") {
		p.handleOAuthCompletion(w, r)
		return
	}

	// Determine traffic direction
	if p.isInboundRequest(r) {
		p.handleInbound(w, r)
	} else {
		p.handleOutbound(w, r)
	}
}

func (p *SidecarProxy) isInboundRequest(req *http.Request) bool {
	// Inbound: requests TO AI Agent (port 8186) or OAuth completion
	// Outbound: requests FROM AI Agent to external services
	return strings.Contains(req.Host, ":8186") ||
		req.URL.Port() == "8186" ||
		strings.HasPrefix(req.URL.Path, "/task") || // AI Agent's task endpoint
		strings.HasPrefix(req.URL.Path, "/oauth-complete") || // OAuth completion endpoint
		strings.HasPrefix(req.URL.Path, "/health") // Health check endpoint
}

func (p *SidecarProxy) handleInbound(backend_w http.ResponseWriter, backend_r *http.Request) {
	p.logger.Info("Inbound request from Backend",
		"path", backend_r.URL.Path,
		"method", backend_r.Method)

	// Extract user ID and store backend response writer for OAuth flow
	userID := backend_r.Header.Get("X-User-ID")
	if userID != "" && backend_r.URL.Path == "/task" {
		p.backendWritersMutex.Lock()
		p.backendWriters[userID] = backend_w
		p.backendWritersMutex.Unlock()
		p.logger.Info("Stored backend response writer for OAuth flow", "user_id", userID)
	}

	// Forward to AI Agent using reverse proxy
	p.aiAgentProxy.ServeHTTP(backend_w, backend_r)

	// Clean up backend response writer after request completes
	if userID != "" && backend_r.URL.Path == "/task" {
		p.backendWritersMutex.Lock()
		delete(p.backendWriters, userID)
		p.backendWritersMutex.Unlock()
		p.logger.Info("Cleaned up backend response writer", "user_id", userID)
	}
}

func (p *SidecarProxy) handleOAuthCompletion(backend_w http.ResponseWriter, backend_r *http.Request) {
	// OAuth completion from Backend - signal waiting handler with OAuth params

	// Extract OAuth parameters from headers
	code := backend_r.Header.Get("X-OAuth-Code")
	codeVerifier := backend_r.Header.Get("X-Code-Verifier")
	userID := backend_r.Header.Get("X-User-ID")
	mcpServerURL := backend_r.Header.Get("X-MCP-Server-URL")

	if code == "" || codeVerifier == "" || userID == "" {
		p.logger.Error("Missing OAuth parameters in completion request")
		http.Error(backend_w, `{"error":"Missing OAuth parameters"}`, http.StatusBadRequest)
		return
	}

	if mcpServerURL == "" {
		mcpServerURL = p.defaultMCPServer
	}

	p.logger.Info("OAuth completion request received, signaling waiting handler",
		"user_id", userID,
		"mcp_server", mcpServerURL)

	// Find the suspended AI Agent request
	p.suspendedMutex.RLock()
	suspended, exists := p.suspendedRequests[userID]
	p.suspendedMutex.RUnlock()

	if !exists {
		p.logger.Error("No suspended AI Agent request found for user", "user_id", userID)
		http.Error(backend_w, `{"error":"No suspended request found"}`, http.StatusNotFound)
		return
	}

	// Send OAuth parameters through channel to wake up waiting handler
	// The handler will do token exchange and forward the request
	suspended.CompletionChan <- &OAuthCompletionData{
		Code:         code,
		CodeVerifier: codeVerifier,
		MCPServerURL: mcpServerURL,
	}

	p.logger.Info("Signaled waiting handler with OAuth params", "user_id", userID)

	// DON'T send response here - the waiting handleOutbound will send the MCP response
	// through agent_w back to AI Agent, which forwards to Backend
}

func (p *SidecarProxy) handleOutbound(agent_w http.ResponseWriter, agent_r *http.Request) {
	// This handles outbound requests from AI Agent to MCP Server
	// agent_w and agent_r are the AI Agent's request/response

	p.logger.Info("Outbound AI Agent request intercepted",
		"host", agent_r.Host,
		"path", agent_r.URL.Path,
		"method", agent_r.Method)

	// Extract user context from request headers
	userID := agent_r.Header.Get("X-User-ID")
	if userID == "" {
		p.logger.Warn("No user ID in AI Agent request, forwarding without auth")
		p.mcpServerProxy.ServeHTTP(agent_w, agent_r)
		return
	}

	// Check if this is an MCP server request
	if !p.isMCPServerRequest(agent_r) {
		p.logger.Debug("Not an MCP request, forwarding as-is", "host", agent_r.Host)
		p.mcpServerProxy.ServeHTTP(agent_w, agent_r)
		return
	}

	// Construct MCP server base URL (without path) for token cache and OAuth discovery
	mcpServerBaseURL := fmt.Sprintf("http://%s", agent_r.Host)

	// Check token cache (using base URL as key)
	token, hasToken := p.authBridge.GetToken(userID, mcpServerBaseURL)

	if !hasToken {
		p.logger.Info("No token found, need OAuth",
			"user_id", userID,
			"mcp_server_base", mcpServerBaseURL)

		// Create completion channel to receive OAuth params
		completionChan := make(chan *OAuthCompletionData, 1)

		// SUSPEND the AI Agent request - store it for later resumption
		suspended := &SuspendedRequest{
			OriginalRequest: agent_r,
			ResponseWriter:  agent_w,
			UserID:          userID,
			MCPServerURL:    mcpServerBaseURL,
			Timestamp:       time.Now(),
			CompletionChan:  completionChan,
		}

		p.suspendedMutex.Lock()
		p.suspendedRequests[userID] = suspended
		p.suspendedMutex.Unlock()

		p.logger.Info("AI Agent request suspended, discovering OAuth requirements",
			"user_id", userID)

		// Send synthetic tools/list to discover OAuth
		authURL, codeVerifier, err := p.authBridge.DiscoverOAuthRequirements(userID, mcpServerBaseURL)
		if err != nil {
			p.logger.Error("OAuth discovery failed", "error", err)
			p.suspendedMutex.Lock()
			delete(p.suspendedRequests, userID)
			p.suspendedMutex.Unlock()

			// Get backend response writer and send error
			p.backendWritersMutex.RLock()
			backendW, exists := p.backendWriters[userID]
			p.backendWritersMutex.RUnlock()

			if exists {
				http.Error(backendW, `{"error":"Authentication required but OAuth discovery failed"}`,
					http.StatusUnauthorized)
			} else {
				// Fallback to agent_w if backend writer not found
				http.Error(agent_w, `{"error":"Authentication required but OAuth discovery failed"}`,
					http.StatusUnauthorized)
			}
			return
		}

		p.logger.Info("OAuth discovery completed, sending 401 to Backend",
			"user_id", userID,
			"auth_url", authURL,
			"code_verifier_len", len(codeVerifier))

		// Get the backend response writer that was stored in handleInbound
		p.backendWritersMutex.RLock()
		backendW, exists := p.backendWriters[userID]
		p.backendWritersMutex.RUnlock()

		if !exists {
			p.logger.Error("No backend response writer found for user", "user_id", userID)
			// Send error to AI Agent as fallback
			http.Error(agent_w, `{"error":"Backend connection lost"}`, http.StatusInternalServerError)
			return
		}

		// Send 401 OAuth requirement directly to Backend
		backendW.Header().Set("Content-Type", "application/json")
		backendW.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(backendW).Encode(map[string]string{
			"error":          "authentication_required",
			"auth_url":       authURL,
			"code_verifier":  codeVerifier,
			"mcp_server_url": mcpServerBaseURL,
		})

		p.logger.Info("OAuth requirement sent to Backend, now WAITING for OAuth completion",
			"user_id", userID)

		// WAIT for OAuth completion - receive OAuth params through channel
		oauthData := <-completionChan

		p.logger.Info("OAuth completion received, doing token exchange",
			"user_id", userID)

		// Clean up suspended request
		p.suspendedMutex.Lock()
		delete(p.suspendedRequests, userID)
		p.suspendedMutex.Unlock()

		// Do token exchange
		err = p.authBridge.ExchangeToken(userID, oauthData.MCPServerURL, oauthData.Code, oauthData.CodeVerifier)
		if err != nil {
			p.logger.Error("Token exchange failed", "error", err)
			http.Error(agent_w, fmt.Sprintf(`{"error":"Token exchange failed: %s"}`, err.Error()),
				http.StatusInternalServerError)
			return
		}

		p.logger.Info("Token exchange successful, getting cached token",
			"user_id", userID)

		// Get the cached token
		token, _ = p.authBridge.GetToken(userID, mcpServerBaseURL)
	}

	// Add Authorization header with cached token
	agent_r.Header.Set("Authorization", "Bearer "+token)
	p.logger.Info("Added cached token to AI Agent request", "user_id", userID)

	// Forward to MCP server with auth using reverse proxy
	p.mcpServerProxy.ServeHTTP(agent_w, agent_r)
}

// mcpDirector customizes the reverse proxy director to handle MCP requests
func (p *SidecarProxy) mcpDirector(target *url.URL) func(*http.Request) {
	defaultDirector := func(req *http.Request) {
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = target.Host

		// Preserve the original path
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
	}

	return defaultDirector
}

// mcpModifyResponse handles 401 responses from MCP server
func (p *SidecarProxy) mcpModifyResponse(resp *http.Response) error {
	if resp.StatusCode == http.StatusUnauthorized {
		userID := resp.Request.Header.Get("X-User-ID")
		mcpServerBaseURL := fmt.Sprintf("http://%s", resp.Request.Host)

		p.logger.Info("Received 401 from MCP server, triggering OAuth discovery",
			"user_id", userID,
			"mcp_server_base", mcpServerBaseURL)

		// Discover OAuth requirements
		authURL, codeVerifier, err := p.authBridge.DiscoverOAuthRequirements(userID, mcpServerBaseURL)
		if err != nil {
			p.logger.Error("OAuth discovery failed", "error", err)
			return nil // Let the 401 pass through
		}

		// Replace response body with OAuth requirement
		body := map[string]string{
			"error":          "authentication_required",
			"auth_url":       authURL,
			"code_verifier":  codeVerifier,
			"mcp_server_url": mcpServerBaseURL,
		}

		bodyBytes, _ := json.Marshal(body)
		resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		resp.Header.Set("Content-Type", "application/json")
		resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(bodyBytes)))
		resp.ContentLength = int64(len(bodyBytes))
	}

	return nil
}

func (p *SidecarProxy) isMCPServerRequest(req *http.Request) bool {
	// Check if request is to MCP server
	return strings.Contains(req.Host, "mcp-server") ||
		strings.Contains(req.Host, ":8184") ||
		strings.HasPrefix(req.URL.Path, "/mcp/")
}

// Made with Bob
