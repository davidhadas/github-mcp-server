package backend

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"

	"github.com/github/github-mcp-server/internal/aiagent"
	"github.com/github/github-mcp-server/internal/authbridge"
)

// Logger for Backend with separate log file
var backendLogger *slog.Logger

func init() {
	// Create separate log file for Backend
	logFile, err := os.OpenFile("/tmp/backend.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic(fmt.Sprintf("Failed to open backend log file: %v", err))
	}

	backendLogger = slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// MCPServer represents an MCP server configuration
type MCPServer struct {
	Name string
	URL  string
}

// Config holds the Backend configuration
type Config struct {
	Port         int
	RedirectURI  string
	MCPServers   []MCPServer
	DemoPagePath string
	AIAgentURL   string // URL of AI Agent service (e.g., "http://localhost:8186")
}

// Backend handles frontend requests and coordinates with AuthBridge
type Backend struct {
	config     *Config
	authBridge *authbridge.AuthBridge
	mu         sync.RWMutex
}

// NewBackend creates a new Backend instance
func NewBackend(config *Config, authBridge *authbridge.AuthBridge) *Backend {
	backendLogger.Info("Backend initialized",
		"port", config.Port,
		"servers", len(config.MCPServers))
	return &Backend{
		config:     config,
		authBridge: authBridge,
	}
}

// HandleTask handles task requests from browser
// Flow: Browser → Backend (this) → AuthBridge → AIAgent → AuthBridge → MCP Server
func (b *Backend) HandleTask(w http.ResponseWriter, r *http.Request) {
	var taskReq aiagent.TaskRequest

	if err := json.NewDecoder(r.Body).Decode(&taskReq); err != nil {
		backendLogger.Error("Invalid task request", "error", err.Error())
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if taskReq.UserID == "" || taskReq.Task == "" {
		backendLogger.Error("Missing required fields", "user_id", taskReq.UserID, "task", taskReq.Task)
		http.Error(w, "Missing user_id or task", http.StatusBadRequest)
		return
	}

	// Default to first MCP server if not specified
	if taskReq.MCPServerURL == "" && len(b.config.MCPServers) > 0 {
		taskReq.MCPServerURL = b.config.MCPServers[0].URL
	}

	backendLogger.Info("Received task from browser",
		"user_id", taskReq.UserID,
		"task", taskReq.Task)

	// Forward to AuthBridge which will forward to AIAgent
	backendLogger.Info("→ Forwarding task to AuthBridge",
		"user_id", taskReq.UserID,
		"task", taskReq.Task)

	var result *aiagent.TaskResponse
	var err error

	if b.config.AIAgentURL != "" {
		// AI Agent is a separate process - use HTTP
		backendLogger.Info("Using separate AI Agent process",
			"ai_agent_url", b.config.AIAgentURL)
		result, err = b.authBridge.ExecuteTaskViaHTTP(taskReq, b.config.AIAgentURL)
	} else {
		// AI Agent is in-process - use direct call
		backendLogger.Info("Using in-process AI Agent")
		result, err = b.authBridge.ExecuteTask(taskReq)
	}

	if err != nil {
		backendLogger.Error("← AuthBridge returned error",
			"error", err.Error(),
			"user_id", taskReq.UserID)
		http.Error(w, fmt.Sprintf("Task execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	backendLogger.Info("← Received result from AuthBridge",
		"user_id", taskReq.UserID,
		"status", result.Status)

	// Check if result indicates auth is needed
	if result.Status == "auth_required" {
		backendLogger.Info("Auth required, sending redirect to browser",
			"user_id", taskReq.UserID)

		// Return 401 with auth URL
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(result)
		return
	}

	// Return task result to browser
	backendLogger.Info("Returning result to browser",
		"user_id", taskReq.UserID,
		"status", result.Status)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// HandleTokenExchange handles OAuth token exchange
func (b *Backend) HandleTokenExchange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
		UserID       string `json:"user_id"`
		MCPServerURL string `json:"mcp_server_url"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		backendLogger.Error("Invalid token exchange request", "error", err.Error())
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Default to first MCP server if not specified
	if req.MCPServerURL == "" && len(b.config.MCPServers) > 0 {
		req.MCPServerURL = b.config.MCPServers[0].URL
	}

	backendLogger.Info("Token exchange request",
		"user_id", req.UserID)

	// Forward to AuthBridge (token is cached internally, never returned)
	err := b.authBridge.ExchangeToken(req.UserID, req.MCPServerURL, req.Code, req.CodeVerifier)
	if err != nil {
		backendLogger.Error("Token exchange failed",
			"error", err.Error(),
			"user_id", req.UserID)
		http.Error(w, fmt.Sprintf("Token exchange failed: %v", err), http.StatusInternalServerError)
		return
	}

	backendLogger.Info("Token exchange successful",
		"user_id", req.UserID)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// HandleAuthURL handles auth URL requests
func (b *Backend) HandleAuthURL(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MCPServerURL string `json:"mcp_server_url"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		backendLogger.Error("Invalid auth URL request", "error", err.Error())
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Default to first MCP server if not specified
	if req.MCPServerURL == "" && len(b.config.MCPServers) > 0 {
		req.MCPServerURL = b.config.MCPServers[0].URL
	}

	backendLogger.Info("Auth URL request")

	authURL, codeVerifier, err := b.authBridge.GetAuthURL(req.MCPServerURL)
	if err != nil {
		backendLogger.Error("Failed to get auth URL",
			"error", err.Error())
		http.Error(w, fmt.Sprintf("Failed to get auth URL: %v", err), http.StatusInternalServerError)
		return
	}

	backendLogger.Info("Auth URL obtained")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"url":           authURL,
		"code_verifier": codeVerifier,
	})
}

// HandleTokenStatus returns token status for a user
func (b *Backend) HandleTokenStatus(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		http.Error(w, "Missing user_id parameter", http.StatusBadRequest)
		return
	}

	servers := b.authBridge.GetTokenCache().ListUserTokens(userID)

	backendLogger.Info("Token status request", "user_id", userID, "token_count", len(servers))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"user_id": userID,
		"servers": servers,
	})
}

// HandleDeleteToken deletes a token for a user
func (b *Backend) HandleDeleteToken(w http.ResponseWriter, r *http.Request) {
	// Extract from URL path or query params
	userID := r.URL.Query().Get("user_id")
	mcpServer := r.URL.Query().Get("mcp_server")

	if userID == "" || mcpServer == "" {
		http.Error(w, "Missing user_id or mcp_server parameter", http.StatusBadRequest)
		return
	}

	backendLogger.Info("Delete token request",
		"user_id", userID)

	b.authBridge.GetTokenCache().DeleteToken(userID, mcpServer)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

// Made with Bob
