package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/github/github-mcp-server/internal/authbridge"
	"github.com/github/github-mcp-server/internal/backend"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/viper"
)

// MCPServer represents an MCP server configuration
type MCPServer struct {
	Name string `mapstructure:"name"`
	URL  string `mapstructure:"url"`
}

// Config holds the service configuration
type Config struct {
	Port         int         `mapstructure:"port"`
	RedirectURI  string      `mapstructure:"redirect_uri"`
	MCPServers   []MCPServer `mapstructure:"mcp_servers"`
	DemoPagePath string      `mapstructure:"demo_page_path"`
}

func main() {
	// Load configuration
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./cmd/authbridge-extension")
	viper.AddConfigPath("./cmd/oauth-coordinator") // Backward compatibility

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

	// Convert MCPServers to backend.MCPServer
	backendMCPServers := make([]backend.MCPServer, len(config.MCPServers))
	for i, mcp := range config.MCPServers {
		backendMCPServers[i] = backend.MCPServer{
			Name: mcp.Name,
			URL:  mcp.URL,
		}
	}

	// Create Backend instance
	backendConfig := &backend.Config{
		Port:         config.Port,
		RedirectURI:  config.RedirectURI,
		MCPServers:   backendMCPServers,
		DemoPagePath: config.DemoPagePath,
	}
	backendService := backend.NewBackend(backendConfig, authBridge)

	// Setup HTTP server
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// Task endpoint - Primary interface for browser
	r.Post("/task", backendService.HandleTask)

	// OAuth endpoints
	r.Post("/auth/url", backendService.HandleAuthURL)
	r.Post("/oauth/exchange-token", backendService.HandleTokenExchange)
	r.Get("/callback", handleCallback(backendConfig))

	// Token cache management endpoints
	r.Get("/tokens/status", backendService.HandleTokenStatus)
	r.Delete("/tokens", backendService.HandleDeleteToken)

	// Serve demo page if configured
	if config.DemoPagePath != "" {
		r.Get("/demo", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			http.ServeFile(w, r, config.DemoPagePath)
		})
		log.Printf("Demo page registered: %s", config.DemoPagePath)
	}

	// Test endpoint for mocking token exchange (only for testing)
	r.Post("/test/mock-token-exchange", handleMockTokenExchange(authBridge))

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	addr := fmt.Sprintf(":%d", config.Port)
	log.Printf("KAgentI Service starting on port %d", config.Port)
	log.Printf("Architecture: Browser → Backend → AIAgent → AuthBridge → MCP Server")
	log.Printf("Logs: Backend (/tmp/backend.log), AuthBridge (/tmp/authbridge.log), AIAgent (/tmp/aiagent.log)")

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

// handleCallback returns a handler for OAuth callbacks
func handleCallback(config *backend.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		state := r.URL.Query().Get("state")

		if code == "" {
			http.Error(w, "Missing code parameter", http.StatusBadRequest)
			return
		}

		// Redirect to demo page with code and state
		redirectURL := fmt.Sprintf("/demo?code=%s&state=%s", code, state)
		http.Redirect(w, r, redirectURL, http.StatusFound)
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
