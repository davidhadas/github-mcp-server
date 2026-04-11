package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/github/github-mcp-server/internal/aiagent"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/viper"
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

// Config holds the service configuration
type Config struct {
	Port          int    `mapstructure:"port"`
	AuthBridgeURL string `mapstructure:"authbridge_url"`
	DemoPagePath  string `mapstructure:"demo_page_path"`
}

func main() {
	// Load configuration
	viper.SetConfigName("backend-config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./cmd/backend")

	if err := viper.ReadInConfig(); err != nil {
		backendLogger.Error("Error reading config file", "error", err)
		os.Exit(1)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		backendLogger.Error("Error unmarshaling config", "error", err)
		os.Exit(1)
	}

	// Validate configuration
	if config.Port == 0 {
		config.Port = 8187
	}
	if config.AuthBridgeURL == "" {
		config.AuthBridgeURL = "http://localhost:8185"
	}

	backendLogger.Info("Backend service starting",
		"port", config.Port,
		"authbridge_url", config.AuthBridgeURL)

	// Setup HTTP server with separate access log
	accessLog, err := os.OpenFile("/tmp/backend-access.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		backendLogger.Error("Failed to open access log file", "error", err)
		os.Exit(1)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestLogger(&middleware.DefaultLogFormatter{Logger: log.New(accessLog, "", log.LstdFlags)}))
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// Task endpoint - Primary interface for browser (may include OAuth code)
	r.Post("/task", handleTask(&config))

	// OAuth callback endpoint
	r.Get("/callback", handleCallback(&config))

	// Serve demo page if configured
	if config.DemoPagePath != "" {
		r.Get("/demo", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			http.ServeFile(w, r, config.DemoPagePath)
		})
		backendLogger.Info("Demo page registered", "path", config.DemoPagePath)
	}

	// Test endpoint for mocking token exchange (only for testing)
	r.Post("/test/mock-token-exchange", func(w http.ResponseWriter, r *http.Request) {
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

		backendLogger.Info("Mock token exchange request", "user_id", req.UserID)

		// Forward to AuthBridge's token exchange endpoint
		tokenReq := map[string]string{
			"code":           req.Code,
			"code_verifier":  req.CodeVerifier,
			"user_id":        req.UserID,
			"mcp_server_url": req.MCPServerURL,
		}
		reqBody, _ := json.Marshal(tokenReq)

		resp, err := http.Post(config.AuthBridgeURL+"/test/mock-token-exchange", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			backendLogger.Error("Failed to forward mock token exchange", "error", err.Error())
			http.Error(w, fmt.Sprintf("Failed to exchange token: %v", err), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		backendLogger.Info("Mock token exchange successful", "user_id", req.UserID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
	})

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	addr := fmt.Sprintf(":%d", config.Port)
	backendLogger.Info("Backend service ready",
		"address", addr,
		"demo_url", fmt.Sprintf("http://localhost:%d/demo", config.Port))

	backendLogger.Info("Backend Service starting",
		"port", config.Port,
		"architecture", fmt.Sprintf("Browser → Backend:%d → AuthBridge:%s → AI Agent → MCP Server", config.Port, config.AuthBridgeURL),
		"demo_url", fmt.Sprintf("http://localhost:%d/demo", config.Port))

	if err := http.ListenAndServe(addr, r); err != nil {
		backendLogger.Error("Server failed", "error", err)
		os.Exit(1)
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

// handleTask handles task requests from browser and forwards to AuthBridge
func handleTask(config *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var taskReq struct {
			aiagent.TaskRequest
			// OAuth fields for token exchange (handled by AuthBridge, not forwarded to AI Agent)
			OAuthCode    string `json:"oauth_code,omitempty"`
			CodeVerifier string `json:"code_verifier,omitempty"`
			MCPServerURL string `json:"mcp_server_url,omitempty"`
		}

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

		backendLogger.Info("Received task from browser",
			"user_id", taskReq.UserID,
			"task", taskReq.Task,
			"has_oauth_code", taskReq.OAuthCode != "")

		// Forward entire request to AuthBridge (including OAuth fields if present)
		// AuthBridge will handle token exchange and resume blocked MCP request
		backendLogger.Info("Forwarding task to AuthBridge",
			"user_id", taskReq.UserID,
			"authbridge_url", config.AuthBridgeURL)

		reqBody, err := json.Marshal(taskReq)
		if err != nil {
			backendLogger.Error("Failed to marshal task request", "error", err.Error())
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to marshal task request"})
			return
		}

		resp, err := http.Post(config.AuthBridgeURL+"/task", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			backendLogger.Error("Failed to forward task to AuthBridge",
				"error", err.Error(),
				"authbridge_url", config.AuthBridgeURL)
			http.Error(w, fmt.Sprintf("Failed to forward task: %v", err), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			backendLogger.Error("Failed to read AuthBridge response", "error", err.Error())
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read response from AuthBridge"})
			return
		}

		var taskResp aiagent.TaskResponse
		if err := json.Unmarshal(body, &taskResp); err != nil {
			backendLogger.Error("Failed to parse AuthBridge response", "error", err.Error())
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to parse response from AuthBridge"})
			return
		}

		backendLogger.Info("Received result from AuthBridge",
			"user_id", taskReq.UserID,
			"status", taskResp.Status)

		// Check if result indicates auth is needed
		if taskResp.Status == "auth_required" {
			backendLogger.Info("Auth required, sending redirect to browser",
				"user_id", taskReq.UserID)

			// Return 401 with auth URL
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(taskResp)
			return
		}

		// Return task result to browser
		backendLogger.Info("Returning result to browser",
			"user_id", taskReq.UserID,
			"status", taskResp.Status)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(body)
	}
}

// handleCallback handles OAuth callbacks from GitHub
func handleCallback(config *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")

		if code == "" {
			backendLogger.Error("OAuth callback missing code")
			http.Error(w, "Missing code parameter", http.StatusBadRequest)
			return
		}

		backendLogger.Info("OAuth callback received", "has_code", code != "", "has_state", state != "")

		// Redirect to demo page with code and state
		redirectURL := fmt.Sprintf("/demo?code=%s&state=%s", code, state)
		http.Redirect(w, r, redirectURL, http.StatusFound)
	}
}

// handleTokenExchange handles OAuth token exchange requests from browser
func handleTokenExchange(config *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		if req.Code == "" || req.CodeVerifier == "" || req.UserID == "" {
			backendLogger.Error("Missing required fields in token exchange")
			http.Error(w, "Missing required fields", http.StatusBadRequest)
			return
		}

		backendLogger.Info("Token exchange request",
			"user_id", req.UserID,
			"mcp_server", req.MCPServerURL)

		// Forward to AuthBridge's token exchange endpoint
		reqBody, _ := json.Marshal(req)
		resp, err := http.Post(config.AuthBridgeURL+"/oauth/exchange-token", "application/json", bytes.NewReader(reqBody))
		if err != nil {
			backendLogger.Error("Failed to forward token exchange to AuthBridge", "error", err.Error())
			http.Error(w, fmt.Sprintf("Token exchange failed: %v", err), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != http.StatusOK {
			backendLogger.Error("AuthBridge token exchange failed",
				"status", resp.StatusCode,
				"user_id", req.UserID)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(body)
			return
		}

		backendLogger.Info("Token exchange successful", "user_id", req.UserID)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}
}

// Made with Bob
