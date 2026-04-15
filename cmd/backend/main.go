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
	"runtime/debug"
	"time"

	"github.com/github/github-mcp-server/internal/aiagent"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/viper"
)

// Build-time variables
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// Logger for Backend
var backendLogger *slog.Logger

func init() {
	// Log to stdout for Kubernetes
	backendLogger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
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
	// Log version to verify code deployment
	backendLogger.Info("Backend starting",
		"version", version,
		"commit", commit,
		"build_time", date)

	// Load configuration
	viper.SetConfigName("backend-config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/config") // Kubernetes ConfigMap mount
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

	// Setup HTTP server with access log to stdout
	r := chi.NewRouter()
	r.Use(middleware.RequestLogger(&middleware.DefaultLogFormatter{Logger: log.New(os.Stdout, "", log.LstdFlags)}))
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// Task endpoint - Primary interface for browser (may include OAuth code)
	r.Post("/task", handleTask(&config))

	// OAuth callback endpoints (both paths for compatibility)
	r.Get("/callback", handleCallback(&config))
	r.Get("/oauth/callback", handleCallback(&config))

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

// handleTask handles task requests from browser and forwards to AI Agent
func handleTask(config *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if r := recover(); r != nil {
				backendLogger.Error("PANIC in handleTask", "panic", r, "stack", string(debug.Stack()))
				http.Error(w, "Internal server error", http.StatusInternalServerError)
			}
		}()

		var taskReq struct {
			UserID string                 `json:"user_id"`
			Task   string                 `json:"task"`
			Params map[string]interface{} `json:"params,omitempty"`
			// OAuth fields for sidecar to detect and handle
			OAuthCode    string `json:"oauth_code,omitempty"`
			CodeVerifier string `json:"code_verifier,omitempty"`
			ResumeKey    string `json:"resume_key,omitempty"`
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

		// Check if this is an OAuth completion request (has OAuth code)
		if taskReq.OAuthCode != "" && taskReq.CodeVerifier != "" {
			backendLogger.Info("OAuth completion request - sending to sidecar",
				"user_id", taskReq.UserID,
				"resume_key", taskReq.ResumeKey)

			// Send OAuth completion request to sidecar with headers
			// This will resume the suspended request
			targetURL := config.AuthBridgeURL + "/task"
			req, err := http.NewRequest("POST", targetURL, nil)
			if err != nil {
				backendLogger.Error("Failed to create OAuth completion request", "error", err.Error())
				http.Error(w, fmt.Sprintf("Failed to create request: %v", err), http.StatusInternalServerError)
				return
			}

			// Set OAuth parameters as headers
			req.Header.Set("X-OAuth-Code", taskReq.OAuthCode)
			req.Header.Set("X-Code-Verifier", taskReq.CodeVerifier)
			req.Header.Set("X-User-ID", taskReq.UserID)
			req.Header.Set("X-Authbridge-Resume", taskReq.ResumeKey)
			req.Header.Set("Content-Type", "application/json")

			backendLogger.Info("Sending OAuth completion request",
				"target_url", targetURL,
				"user_id", taskReq.UserID)

			client := &http.Client{}
			resp, err := client.Do(req)
			if err != nil {
				backendLogger.Error("Failed to send OAuth completion request",
					"error", err.Error())
				http.Error(w, fmt.Sprintf("Failed to complete OAuth: %v", err), http.StatusInternalServerError)
				return
			}
			defer resp.Body.Close()

			// The sidecar will have resumed the suspended request and returned the MCP response
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				backendLogger.Error("Failed to read OAuth completion response", "error", err.Error())
				http.Error(w, "Failed to read response", http.StatusInternalServerError)
				return
			}

			backendLogger.Info("OAuth completion successful, returning result",
				"user_id", taskReq.UserID,
				"status_code", resp.StatusCode)

			// Return the result to browser
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(body)
			return
		}

		// Normal task request (no OAuth code) - forward to AI Agent
		backendLogger.Info("Forwarding task to AI Agent",
			"user_id", taskReq.UserID,
			"aiagent_url", config.AuthBridgeURL)

		// Create simple request body with just user_id and task
		simpleReq := struct {
			UserID string `json:"user_id"`
			Task   string `json:"task"`
		}{
			UserID: taskReq.UserID,
			Task:   taskReq.Task,
		}

		reqBody, err := json.Marshal(simpleReq)
		if err != nil {
			backendLogger.Error("Failed to marshal task request", "error", err.Error())
			http.Error(w, fmt.Sprintf("Failed to marshal request: %v", err), http.StatusInternalServerError)
			return
		}

		targetURL := config.AuthBridgeURL + "/task"
		req, err := http.NewRequest("POST", targetURL, bytes.NewReader(reqBody))
		if err != nil {
			backendLogger.Error("Failed to create request", "error", err.Error())
			http.Error(w, fmt.Sprintf("Failed to create request: %v", err), http.StatusInternalServerError)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-ID", taskReq.UserID)

		backendLogger.Info("Sending task request",
			"target_url", targetURL,
			"user_id", taskReq.UserID)

		client := &http.Client{}
		resp, err := client.Do(req)

		backendLogger.Info("Task request returned",
			"has_error", err != nil,
			"timestamp", time.Now().Format(time.RFC3339Nano))

		if err != nil {
			backendLogger.Error("Failed to forward task to AI Agent",
				"error", err.Error(),
				"error_type", fmt.Sprintf("%T", err),
				"aiagent_url", config.AuthBridgeURL,
				"timestamp", time.Now().Format(time.RFC3339Nano))
			http.Error(w, fmt.Sprintf("Failed to forward task: %v", err), http.StatusInternalServerError)
			return
		}
		defer resp.Body.Close()

		backendLogger.Info("Received response from AI Agent",
			"status_code", resp.StatusCode,
			"content_type", resp.Header.Get("Content-Type"),
			"timestamp", time.Now().Format(time.RFC3339Nano))

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			backendLogger.Error("Failed to read AI Agent response",
				"error", err.Error(),
				"error_type", fmt.Sprintf("%T", err),
				"status_code", resp.StatusCode)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to read response from AI Agent"})
			return
		}

		backendLogger.Info("Successfully read response body",
			"body_length", len(body),
			"status_code", resp.StatusCode)

		// Check if this is a 401 (OAuth required) response from sidecar
		backendLogger.Info("Checking response status",
			"status_code", resp.StatusCode,
			"is_401", resp.StatusCode == http.StatusUnauthorized,
			"StatusUnauthorized_const", http.StatusUnauthorized)

		if resp.StatusCode == http.StatusUnauthorized {
			backendLogger.Info("OAuth required, forwarding to browser",
				"user_id", taskReq.UserID,
				"response_body", string(body))

			// Forward the OAuth response directly to browser
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write(body)
			return
		}

		var taskResp aiagent.TaskResponse
		if err := json.Unmarshal(body, &taskResp); err != nil {
			backendLogger.Error("Failed to parse AI Agent response", "error", err.Error())
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": "Failed to parse response from AI Agent"})
			return
		}

		backendLogger.Info("Received result from AI Agent",
			"user_id", taskReq.UserID,
			"status", taskResp.Status)

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
