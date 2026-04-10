package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/github/github-mcp-server/internal/aiagent"
	"github.com/github/github-mcp-server/internal/authbridge"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/viper"
)

// Config holds the service configuration
type Config struct {
	Port        int    `mapstructure:"port"`
	RedirectURI string `mapstructure:"redirect_uri"`
	AIAgentURL  string `mapstructure:"ai_agent_url"` // URL of separate AI Agent service
}

func main() {
	// Load configuration
	viper.SetConfigName("authbridge-config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./cmd/authbridge-extension")

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

	// Create AuthBridge instance
	authBridge := authbridge.NewAuthBridge(config.RedirectURI)

	// Setup HTTP server
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// Task endpoint - Receives tasks from Backend
	r.Post("/task", handleTask(authBridge, config.AIAgentURL))

	// MCP Proxy endpoint - For AI Agent to make MCP requests
	r.Post("/mcp", authBridge.HandleMCPProxyHTTP)

	// OAuth token exchange endpoint - Called by Backend after OAuth
	r.Post("/oauth/exchange-token", handleTokenExchange(authBridge))

	// Test endpoint for mocking token exchange (only for testing)
	r.Post("/test/mock-token-exchange", handleMockTokenExchange(authBridge))

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	addr := fmt.Sprintf(":%d", config.Port)
	log.Printf("AuthBridge Service starting on port %d", config.Port)

	if config.AIAgentURL != "" {
		log.Printf("Architecture: Backend → AuthBridge:%d → AI Agent:%s → AuthBridge MCP Proxy → MCP Server", config.Port, config.AIAgentURL)
		log.Printf("AI Agent is a separate process")
	} else {
		log.Printf("Architecture: Backend → AuthBridge:%d → AI Agent (in-process) → MCP Server", config.Port)
		log.Printf("AI Agent is in-process")
	}

	log.Printf("Logs: AuthBridge (/tmp/authbridge.log), AIAgent (/tmp/aiagent.log)")

	if err := http.ListenAndServe(addr, r); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// corsMiddleware adds CORS headers
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// handleTask handles task requests from Backend and forwards to AI Agent
func handleTask(ab *authbridge.AuthBridge, aiAgentURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var taskReq aiagent.TaskRequest

		if err := json.NewDecoder(r.Body).Decode(&taskReq); err != nil {
			log.Printf("Invalid task request: %v", err)
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		log.Printf("Received task from Backend: user_id=%s, task=%s", taskReq.UserID, taskReq.Task)

		var result *aiagent.TaskResponse
		var err error

		if aiAgentURL != "" {
			// AI Agent is a separate process - use HTTP
			result, err = ab.ExecuteTaskViaHTTP(taskReq, aiAgentURL)
		} else {
			// AI Agent is in-process - use direct call
			result, err = ab.ExecuteTask(taskReq)
		}

		if err != nil {
			log.Printf("Task execution failed: %v", err)
			http.Error(w, fmt.Sprintf("Task execution failed: %v", err), http.StatusInternalServerError)
			return
		}

		log.Printf("Returning result to Backend: status=%s", result.Status)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// handleTokenExchange handles OAuth token exchange from Backend
func handleTokenExchange(ab *authbridge.AuthBridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Code         string `json:"code"`
			CodeVerifier string `json:"code_verifier"`
			UserID       string `json:"user_id"`
			MCPServerURL string `json:"mcp_server_url"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("Invalid token exchange request: %v", err)
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}

		log.Printf("Token exchange request from Backend: user_id=%s", req.UserID)

		// Forward to AuthBridge (token is cached internally, never returned)
		err := ab.ExchangeToken(req.UserID, req.MCPServerURL, req.Code, req.CodeVerifier)
		if err != nil {
			log.Printf("Token exchange failed: %v", err)
			http.Error(w, fmt.Sprintf("Token exchange failed: %v", err), http.StatusInternalServerError)
			return
		}

		log.Printf("Token exchange successful: user_id=%s", req.UserID)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})
	}
}

// Made with Bob

// handleMockTokenExchange returns a handler for testing token exchange
// This endpoint mocks the MCP server's token exchange for testing purposes
func handleMockTokenExchange(ab *authbridge.AuthBridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UserID       string `json:"user_id"`
			MCPServerURL string `json:"mcp_server_url"`
			Code         string `json:"code"`
			CodeVerifier string `json:"code_verifier"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		// For testing: accept any code that starts with "test_"
		if !strings.HasPrefix(req.Code, "test_") {
			http.Error(w, "Invalid test code", http.StatusBadRequest)
			return
		}

		// Mock token - directly cache it in AuthBridge
		mockToken := "gho_test_mock_token_" + req.Code
		ab.SetTokenForTesting(req.UserID, req.MCPServerURL, mockToken, 3600)

		log.Printf("Mock token exchange successful for user: %s", req.UserID)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Mock token cached successfully",
		})
	}
}
