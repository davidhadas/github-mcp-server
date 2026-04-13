package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/github/github-mcp-server/internal/authbridge"
)

func TestSidecarProxy_ServeHTTP(t *testing.T) {
	// Create a test MCP server
	mcpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if Authorization header is present
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success"})
	}))
	defer mcpServer.Close()

	// Create a test AI Agent server
	aiAgentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"agent": "ready"})
	}))
	defer aiAgentServer.Close()

	// Create proxy instance with a proper logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError, // Only show errors in tests
	}))
	ab := authbridge.NewAuthBridge("")
	proxy := &SidecarProxy{
		authBridge:        ab,
		logger:            logger,
		defaultMCPServer:  mcpServer.URL,
		suspendedRequests: make(map[string]*SuspendedRequest),
	}

	t.Run("inbound request to AI agent", func(t *testing.T) {
		req := httptest.NewRequest("POST", "http://localhost:8186/task", nil)
		req.Header.Set("X-User-ID", "test-user")

		// This should be recognized as inbound
		if !proxy.isInboundRequest(req) {
			t.Error("Expected request to be recognized as inbound")
		}
	})

	t.Run("outbound request to MCP server without token", func(t *testing.T) {
		req := httptest.NewRequest("POST", mcpServer.URL+"/mcp/tools/list", nil)
		req.Host = "mcp-server-service:8184"
		req.Header.Set("X-User-ID", "test-user")

		// This should be recognized as MCP request
		if !proxy.isMCPServerRequest(req) {
			t.Error("Expected request to be recognized as MCP request")
		}
	})

	t.Run("OAuth completion endpoint without suspended request", func(t *testing.T) {
		req := httptest.NewRequest("POST", "http://localhost:15001/oauth-complete", nil)
		req.Header.Set("X-User-ID", "test-user-no-suspend")
		req.Header.Set("X-OAuth-Code", "test-code")
		req.Header.Set("X-Code-Verifier", "test-verifier")
		req.Header.Set("X-MCP-Server-URL", mcpServer.URL)
		w := httptest.NewRecorder()

		proxy.ServeHTTP(w, req)

		// Should return error (either 404 for no suspended request or 500 for token exchange failure)
		if w.Code != http.StatusNotFound && w.Code != http.StatusInternalServerError {
			t.Errorf("Expected status 404 or 500, got %d", w.Code)
		}
	})
}

func TestIsInboundRequest(t *testing.T) {
	proxy := &SidecarProxy{}

	tests := []struct {
		name     string
		host     string
		path     string
		expected bool
	}{
		{"AI Agent port", "localhost:8186", "/task", true},
		{"Task endpoint", "localhost:8080", "/task", true},
		{"OAuth complete", "localhost:8080", "/oauth-complete", true},
		{"Health check", "localhost:8080", "/health", true},
		{"MCP request", "mcp-server:8184", "/mcp/tools/list", false},
		{"External request", "example.com:443", "/api", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tt.host+tt.path, nil)
			req.Host = tt.host
			result := proxy.isInboundRequest(req)
			if result != tt.expected {
				t.Errorf("isInboundRequest() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestIsMCPServerRequest(t *testing.T) {
	proxy := &SidecarProxy{}

	tests := []struct {
		name     string
		host     string
		path     string
		expected bool
	}{
		{"MCP server by name", "mcp-server-service:8184", "/tools/list", true},
		{"MCP server by port", "localhost:8184", "/tools/list", true},
		{"MCP path prefix", "example.com:8080", "/mcp/tools/list", true},
		{"Non-MCP request", "example.com:443", "/api/data", false},
		{"AI Agent", "localhost:8186", "/task", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://"+tt.host+tt.path, nil)
			req.Host = tt.host
			result := proxy.isMCPServerRequest(req)
			if result != tt.expected {
				t.Errorf("isMCPServerRequest() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// Made with Bob
