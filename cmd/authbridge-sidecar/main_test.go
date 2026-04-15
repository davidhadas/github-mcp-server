package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/github/github-mcp-server/internal/authbridge"
	"github.com/github/github-mcp-server/pkg/extendedreverseproxy"
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

	// Parse AI Agent URL for inbound proxy
	aiAgentURL, err := url.Parse(aiAgentServer.URL)
	if err != nil {
		t.Fatalf("Failed to parse AI Agent URL: %v", err)
	}

	// Parse MCP server URL
	mcpURL, err := url.Parse(mcpServer.URL)
	if err != nil {
		t.Fatalf("Failed to parse MCP server URL: %v", err)
	}

	proxy := &SidecarProxy{
		authBridge:       ab,
		logger:           logger,
		defaultMCPServer: mcpServer.URL,
		oauthSessions:    make(map[string]*OAuthSession),
		mcpServerProxy:   httputil.NewSingleHostReverseProxy(mcpURL),
	}

	// Initialize inbound proxy
	proxy.inboundProxy = &extendedreverseproxy.ExtendedReverseProxy{
		Classify:      proxy.classifyInboundRequest,
		Rewrite:       proxy.rewriteInboundRequest(aiAgentURL),
		Decide:        proxy.decideInboundRequest,
		Transport:     http.DefaultTransport,
		Store:         extendedreverseproxy.NewMemoryStore(),
		Timeout:       100 * time.Millisecond, // Short timeout for tests
		FlushInterval: -1,
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

	t.Run("Resume request with non-existent session", func(t *testing.T) {
		req := httptest.NewRequest("POST", "http://localhost:8186/task", nil)
		req.Header.Set("X-User-ID", "test-user-no-suspend")
		req.Header.Set("X-Authbridge-Resume", "non-existent-key")
		req.Header.Set("X-OAuth-Code", "test-code")
		req.Header.Set("X-Code-Verifier", "test-verifier")
		req.Header.Set("X-MCP-Server-URL", mcpServer.URL)
		w := httptest.NewRecorder()

		proxy.ServeHTTP(w, req)

		// Should return 404 for non-existent resume session
		if w.Code != http.StatusNotFound {
			t.Errorf("Expected status 404 for non-existent session, got %d", w.Code)
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

func TestParallelMCPCalls(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	// Track OAuth discovery calls per server
	oauthDiscoveryCalls := make(map[string]int)
	var discoveryMu sync.Mutex

	// Create two mock MCP servers (A and B)
	mcpServerA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/url" {
			discoveryMu.Lock()
			oauthDiscoveryCalls["serverA"]++
			discoveryMu.Unlock()

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"url":           "https://github.com/login/oauth/authorize?client_id=test&state=serverA",
				"code_verifier": "verifier-A",
			})
			return
		}
		if r.URL.Path == "/oauth/exchange-token" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token": "token-A",
				"expires_in":   3600,
			})
			return
		}
		// Regular MCP request
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"server": "A", "status": "success"})
	}))
	defer mcpServerA.Close()

	mcpServerB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/url" {
			discoveryMu.Lock()
			oauthDiscoveryCalls["serverB"]++
			discoveryMu.Unlock()

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{
				"url":           "https://github.com/login/oauth/authorize?client_id=test&state=serverB",
				"code_verifier": "verifier-B",
			})
			return
		}
		if r.URL.Path == "/oauth/exchange-token" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"access_token": "token-B",
				"expires_in":   3600,
			})
			return
		}
		// Regular MCP request
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"server": "B", "status": "success"})
	}))
	defer mcpServerB.Close()

	// Create AuthBridge and proxy
	ab := authbridge.NewAuthBridge("")

	proxy := &SidecarProxy{
		authBridge:       ab,
		logger:           logger,
		defaultMCPServer: mcpServerA.URL,
		oauthSessions:    make(map[string]*OAuthSession),
		correlationLocks: make(map[string]chan struct{}),
	}

	// Create a shared resume key for all parallel calls
	resumeKey := "test-correlation-123"
	userID := "test-user"

	// Create OAuth session
	session := &OAuthSession{
		UserID:            userID,
		CreatedAt:         time.Now(),
		OAuthRequiredChan: make(chan *OAuthDiscoveryData, 10), // Larger buffer for multiple servers
		OAuthCompleteChan: make(chan *OAuthCompletionData, 10),
	}
	proxy.putOAuthSession(resumeKey, session)

	t.Run("6 parallel calls: 3 to server A, 3 to server B", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make(chan string, 6)
		errors := make(chan error, 6)

		// Simulate OAuth completion in background
		go func() {
			// Wait for first OAuth discovery (server A)
			discovery1 := <-session.OAuthRequiredChan
			t.Logf("Received OAuth required for server A")

			// Simulate user completing OAuth for server A
			time.Sleep(50 * time.Millisecond)
			session.OAuthCompleteChan <- &OAuthCompletionData{
				Code:         "code-A",
				CodeVerifier: discovery1.CodeVerifier,
				Error:        "", // Success
			}

			// Wait for second OAuth discovery (server B)
			discovery2 := <-session.OAuthRequiredChan
			t.Logf("Received OAuth required for server B")

			// Simulate user completing OAuth for server B
			time.Sleep(50 * time.Millisecond)
			session.OAuthCompleteChan <- &OAuthCompletionData{
				Code:         "code-B",
				CodeVerifier: discovery2.CodeVerifier,
				Error:        "", // Success
			}
		}()

		// Launch 3 parallel calls to server A
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func(callNum int) {
				defer wg.Done()
				token, err := proxy.ensureTokenAvailable(context.Background(), userID, mcpServerA.URL, resumeKey, mcpServerA.URL)
				if err != nil {
					errors <- fmt.Errorf("call A%d failed: %w", callNum, err)
					return
				}
				results <- fmt.Sprintf("A%d:success:%s", callNum, token)
			}(i)
		}

		// Launch 3 parallel calls to server B
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func(callNum int) {
				defer wg.Done()
				token, err := proxy.ensureTokenAvailable(context.Background(), userID, mcpServerB.URL, resumeKey, mcpServerB.URL)
				if err != nil {
					errors <- fmt.Errorf("call B%d failed: %w", callNum, err)
					return
				}
				results <- fmt.Sprintf("B%d:success:%s", callNum, token)
			}(i)
		}

		// Wait for all calls to complete
		wg.Wait()
		close(results)
		close(errors)

		// Check for errors
		var errorList []error
		for err := range errors {
			errorList = append(errorList, err)
		}
		if len(errorList) > 0 {
			t.Errorf("Got %d errors:", len(errorList))
			for _, err := range errorList {
				t.Errorf("  - %v", err)
			}
		}

		// Verify all calls succeeded
		successCount := 0
		serverACount := 0
		serverBCount := 0
		for result := range results {
			successCount++
			if strings.HasPrefix(result, "A") {
				serverACount++
				if !strings.Contains(result, "token-A") {
					t.Errorf("Server A call got wrong token: %s", result)
				}
			} else if strings.HasPrefix(result, "B") {
				serverBCount++
				if !strings.Contains(result, "token-B") {
					t.Errorf("Server B call got wrong token: %s", result)
				}
			}
		}

		if successCount != 6 {
			t.Errorf("Expected 6 successful calls, got %d", successCount)
		}
		if serverACount != 3 {
			t.Errorf("Expected 3 calls to server A, got %d", serverACount)
		}
		if serverBCount != 3 {
			t.Errorf("Expected 3 calls to server B, got %d", serverBCount)
		}

		// Verify OAuth discovery was called exactly once per server
		discoveryMu.Lock()
		defer discoveryMu.Unlock()

		if oauthDiscoveryCalls["serverA"] != 1 {
			t.Errorf("Expected 1 OAuth discovery call to server A, got %d", oauthDiscoveryCalls["serverA"])
		}
		if oauthDiscoveryCalls["serverB"] != 1 {
			t.Errorf("Expected 1 OAuth discovery call to server B, got %d", oauthDiscoveryCalls["serverB"])
		}

		t.Logf("✅ All 6 parallel calls succeeded with correct tokens")
		t.Logf("✅ OAuth discovery called exactly once per server")
	})
}

func TestEnsureTokenAvailable_ContextCancelledWhileWaitingForLock(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	proxy := &SidecarProxy{
		logger:           logger,
		correlationLocks: make(map[string]chan struct{}),
		oauthSessions:    make(map[string]*OAuthSession),
	}

	resumeKey := "lock-cancel-key"
	lock := proxy.getCorrelationLock(resumeKey)
	<-lock

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := proxy.acquireCorrelationLock(ctx, resumeKey)
	if err == nil {
		t.Fatalf("expected cancellation error")
	}
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	lock <- struct{}{}
}

func TestEnsureTokenAvailable_FastPath(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	ab := authbridge.NewAuthBridge("")
	proxy := &SidecarProxy{
		authBridge:       ab,
		logger:           logger,
		correlationLocks: make(map[string]chan struct{}),
	}

	userID := "test-user"
	mcpServer := "http://mcp-server:8184"
	resumeKey := "test-key"

	// Pre-cache a token
	ab.SetTokenForTesting(userID, mcpServer, "cached-token", 3600)

	t.Run("fast path with cached token", func(t *testing.T) {
		token, err := proxy.ensureTokenAvailable(context.Background(), userID, mcpServer, resumeKey, mcpServer)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}
		if token != "cached-token" {
			t.Errorf("Expected 'cached-token', got: %s", token)
		}
	})

	t.Run("multiple parallel calls with cached token", func(t *testing.T) {
		var wg sync.WaitGroup
		results := make(chan string, 10)

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				token, err := proxy.ensureTokenAvailable(context.Background(), userID, mcpServer, resumeKey, mcpServer)
				if err != nil {
					results <- "error"
					return
				}
				results <- token
			}()
		}

		wg.Wait()
		close(results)

		count := 0
		for token := range results {
			count++
			if token != "cached-token" {
				t.Errorf("Expected 'cached-token', got: %s", token)
			}
		}

		if count != 10 {
			t.Errorf("Expected 10 results, got %d", count)
		}
	})
}

func TestCleanupAfterResponseDeletesSession(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	proxy := &SidecarProxy{
		logger:        logger,
		oauthSessions: make(map[string]*OAuthSession),
	}

	resumeKey := "cleanup-key"
	proxy.putOAuthSession(resumeKey, &OAuthSession{
		UserID:            "test-user",
		CreatedAt:         time.Now(),
		OAuthRequiredChan: make(chan *OAuthDiscoveryData, 1),
		OAuthCompleteChan: make(chan *OAuthCompletionData, 1),
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/task", nil)
	req = req.WithContext(context.WithValue(req.Context(), resumeKeyContextKey, resumeKey))
	resp := &http.Response{Request: req}

	if err := proxy.cleanupAfterResponse(resp); err != nil {
		t.Fatalf("cleanupAfterResponse() error = %v", err)
	}

	if _, ok := proxy.getOAuthSession(resumeKey); ok {
		t.Fatalf("expected session to be deleted")
	}
}

func TestCleanupAfterResponsePreservesDetachedSessionUntilResume(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	proxy := &SidecarProxy{
		logger:        logger,
		oauthSessions: make(map[string]*OAuthSession),
	}

	resumeKey := "detached-cleanup-key"
	proxy.putOAuthSession(resumeKey, &OAuthSession{
		UserID:            "test-user",
		CreatedAt:         time.Now(),
		OAuthRequiredChan: make(chan *OAuthDiscoveryData, 1),
		OAuthCompleteChan: make(chan *OAuthCompletionData, 1),
		Detached:          true,
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/task", nil)
	req = req.WithContext(context.WithValue(req.Context(), resumeKeyContextKey, resumeKey))
	resp := &http.Response{Request: req}

	if err := proxy.cleanupAfterResponse(resp); err != nil {
		t.Fatalf("cleanupAfterResponse() error = %v", err)
	}

	if _, ok := proxy.getOAuthSession(resumeKey); !ok {
		t.Fatalf("expected detached session to be preserved before resume")
	}

	resumeReq := httptest.NewRequest(http.MethodPost, "http://example.com/task", nil)
	resumeReq.Header.Set("X-Authbridge-Resume", resumeKey)
	resumeReq = resumeReq.WithContext(context.WithValue(resumeReq.Context(), resumeKeyContextKey, resumeKey))
	resumeResp := &http.Response{Request: resumeReq}

	if err := proxy.cleanupAfterResponse(resumeResp); err != nil {
		t.Fatalf("cleanupAfterResponse() on resume error = %v", err)
	}

	if _, ok := proxy.getOAuthSession(resumeKey); ok {
		t.Fatalf("expected detached session to be deleted after resume response")
	}
}

// Made with Bob
