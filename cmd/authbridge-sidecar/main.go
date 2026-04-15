package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"github.com/github/github-mcp-server/pkg/extendedreverseproxy"
)

// Build-time variables
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// OAuthCompletionData contains OAuth parameters for token exchange.
type OAuthCompletionData struct {
	Code         string
	CodeVerifier string
	Error        string // Empty = success, non-empty = error message
}

// OAuthDiscoveryData contains OAuth discovery results from OUTBOUND to INBOUND.
type OAuthDiscoveryData struct {
	AuthURL      string
	CodeVerifier string
}

// OAuthSession coordinates the detached inbound flow with the blocked outbound flow.
// One session per user request (resume_key), can handle multiple OAuth flows.
type OAuthSession struct {
	UserID       string    // Token cache key
	MCPServerURL string    // Current MCP server needing auth (changes per OAuth)
	CreatedAt    time.Time // For monitoring OAuth duration

	// OUTBOUND → INBOUND: OAuth required (reused for each MCP server)
	OAuthRequiredChan chan *OAuthDiscoveryData

	// INBOUND → OUTBOUND: OAuth completion (reused for each MCP server)
	OAuthCompleteChan chan *OAuthCompletionData

	// Tracks whether this request has entered detached/resume mode.
	Detached bool
}

type contextKey string

const (
	resumeKeyContextKey     contextKey = "resume_key"
	defaultOAuthFlowTimeout            = 5 * time.Minute
	defaultLockWaitTimeout             = 15 * time.Second
)

// SidecarProxy intercepts all pod traffic using reverse proxy.
type SidecarProxy struct {
	authBridge       *authbridge.AuthBridge
	logger           *slog.Logger
	defaultMCPServer string

	inboundProxy   *extendedreverseproxy.ExtendedReverseProxy
	mcpServerProxy *httputil.ReverseProxy

	oauthSessions map[string]*OAuthSession
	sessionMu     sync.RWMutex

	// Per-correlation OAuth locks to serialize OAuth flows within a single user request
	// Key is the correlation_id (resume_key) - ensures only one OAuth flow at a time per request
	correlationLocks   map[string]chan struct{}
	correlationLocksMu sync.RWMutex
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
		authBridge:       ab,
		logger:           logger,
		defaultMCPServer: defaultMCPServer,
		mcpServerProxy:   httputil.NewSingleHostReverseProxy(mcpURL),
		oauthSessions:    make(map[string]*OAuthSession),
		correlationLocks: make(map[string]chan struct{}),
	}

	// Customize the MCP server proxy to add auth headers and cleanup.
	proxy.mcpServerProxy.Director = proxy.mcpDirector(mcpURL)
	proxy.mcpServerProxy.ModifyResponse = proxy.cleanupAfterResponse

	proxy.inboundProxy = &extendedreverseproxy.ExtendedReverseProxy{
		Classify:       proxy.classifyInboundRequest,
		Rewrite:        proxy.rewriteInboundRequest(aiAgentURL),
		Decide:         proxy.decideInboundRequest,
		ModifyResponse: proxy.cleanupAfterResponse,
		Transport:      http.DefaultTransport,
		Store:          extendedreverseproxy.NewMemoryStore(),
		Timeout:        defaultOAuthFlowTimeout,
		FlushInterval:  -1,
	}

	// Create HTTP server on intercept port
	server := &http.Server{
		Addr:    ":15001",
		Handler: proxy,
	}

	logger.Info("AuthBridge sidecar proxy started",
		"port", 15001,
		"mode", "extended_inbound_proxy",
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

	// Determine traffic direction.
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

func (p *SidecarProxy) handleInbound(w http.ResponseWriter, r *http.Request) {
	p.logger.Info("Inbound request from Backend",
		"path", r.URL.Path,
		"method", r.Method)

	// Check if this is a resume request with OAuth completion data
	resumeKey := r.Header.Get("X-Authbridge-Resume")
	if resumeKey != "" {
		// This is a resume request - handle OAuth completion before proxying
		session, ok := p.getOAuthSession(resumeKey)
		if ok {
			// Extract OAuth completion data from headers
			oauthCode := r.Header.Get("X-OAuth-Code")
			codeVerifier := r.Header.Get("X-Code-Verifier")

			if oauthCode != "" && codeVerifier != "" {
				// Send OAuth completion data to blocked OUTBOUND handler
				oauthData := &OAuthCompletionData{
					Code:         oauthCode,
					CodeVerifier: codeVerifier,
					Error:        "",
				}

				select {
				case session.OAuthCompleteChan <- oauthData:
					p.logger.Info("OAuth completion data sent to OUTBOUND handler",
						"resume_key", resumeKey)
				default:
					p.logger.Warn("OAuth completion channel full or already processed",
						"resume_key", resumeKey)
				}
			}
		}
	}

	p.inboundProxy.ServeHTTP(w, r)
}

func (p *SidecarProxy) handleOutbound(agent_w http.ResponseWriter, agent_r *http.Request) {
	// This handles outbound requests from AI Agent to MCP Server
	// agent_w and agent_r are the AI Agent's request/response

	p.logger.Info("Outbound AI Agent request intercepted",
		"host", agent_r.Host,
		"path", agent_r.URL.Path,
		"method", agent_r.Method)

	// Extract user context and resume key from request headers
	userID := agent_r.Header.Get("X-User-ID")
	resumeKey := agent_r.Header.Get("X-Authbridge-Resume")

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

	// Construct the full MCP URL (with scheme, host, and path) for OAuth discovery
	mcpFullURL := fmt.Sprintf("http://%s%s", agent_r.Host, agent_r.URL.Path)
	if agent_r.URL.RawQuery != "" {
		mcpFullURL += "?" + agent_r.URL.RawQuery
	}

	// Ensure we have a resume key for correlation locking
	if resumeKey == "" {
		p.logger.Error("Missing resume key for MCP request", "user_id", userID)
		http.Error(agent_w, `{"error":"missing_resume_key"}`, http.StatusUnauthorized)
		return
	}

	// Ensure token is available (handles OAuth flow if needed with semaphore)
	token, err := p.ensureTokenAvailable(agent_r.Context(), userID, mcpServerBaseURL, resumeKey, mcpFullURL)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			p.logger.Warn("Stopped outbound processing due to canceled context",
				"error", err,
				"user_id", userID,
				"mcp_server", mcpServerBaseURL,
				"resume_key", resumeKey)
			return
		}
		p.logger.Error("Failed to ensure token availability",
			"error", err,
			"user_id", userID,
			"mcp_server", mcpServerBaseURL,
			"resume_key", resumeKey)
		http.Error(agent_w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Add Authorization header with token
	agent_r.Header.Set("Authorization", "Bearer "+token)
	p.logger.Info("Added token to AI Agent request", "user_id", userID, "mcp_server", mcpServerBaseURL)

	// Add resume_key to request context for cleanup after response
	ctx := context.WithValue(agent_r.Context(), resumeKeyContextKey, resumeKey)
	agent_r = agent_r.WithContext(ctx)

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

		// The Director receives a shallow copy of the request.
		// We need to ensure the context with resume_key is preserved.
		// Extract from header and re-add to context if not already there.
		if req.Context().Value(resumeKeyContextKey) == nil {
			resumeKey := req.Header.Get("X-Authbridge-Resume")
			if resumeKey != "" {
				ctx := context.WithValue(req.Context(), resumeKeyContextKey, resumeKey)
				*req = *req.WithContext(ctx)
				p.logger.Debug("Re-added resume_key to request context in Director", "resume_key", resumeKey)
			}
		}
	}

	return defaultDirector
}

func (p *SidecarProxy) isMCPServerRequest(req *http.Request) bool {
	// Check if request is to MCP server
	return strings.Contains(req.Host, "mcp-server") ||
		strings.Contains(req.Host, ":8184") ||
		strings.HasPrefix(req.URL.Path, "/mcp/")
}

func (p *SidecarProxy) classifyInboundRequest(r *http.Request) (extendedreverseproxy.InboundRequest, error) {
	resumeKey := r.Header.Get("X-Authbridge-Resume")
	if resumeKey != "" {
		p.logger.Info("Classified as resume request",
			"resume_key", resumeKey,
			"path", r.URL.Path,
			"method", r.Method)
		return extendedreverseproxy.InboundRequest{
			Kind: extendedreverseproxy.RequestKindResume,
			In:   r,
			Key:  resumeKey,
		}, nil
	}

	newKey := generateResumeKey()
	p.logger.Info("Classified as forward request",
		"resume_key", newKey,
		"path", r.URL.Path,
		"method", r.Method)
	return extendedreverseproxy.InboundRequest{
		Kind: extendedreverseproxy.RequestKindForward,
		In:   r,
		Key:  newKey,
	}, nil
}

// Store resume request flag in context to avoid header pollution
type contextKeyType string

const isResumeRequestKey contextKeyType = "is_resume_request"

func (p *SidecarProxy) rewriteInboundRequest(target *url.URL) func(*extendedreverseproxy.UpstreamRequest) {
	return func(ur *extendedreverseproxy.UpstreamRequest) {
		// For resume requests, Rewrite should not be called by ExtendedReverseProxy
		// But we'll handle it gracefully just in case
		isResumeRequest := ur.In.Header.Get("X-Authbridge-Resume") != ""

		if isResumeRequest {
			// Resume requests should not be rewritten - they're handled by handleResume
			p.logger.Warn("Rewrite called for resume request - this should not happen",
				"resume_key", ur.Key)
			return
		}

		// Rewrite to AI Agent target (only for forward requests)
		ur.Out.URL.Scheme = target.Scheme
		ur.Out.URL.Host = target.Host
		ur.Out.Host = target.Host

		// Clone headers to avoid modifying original
		ur.Out.Header = ur.Out.Header.Clone()

		// Store resume_key in request context
		ctx := context.WithValue(ur.Out.Context(), resumeKeyContextKey, ur.Key)
		ur.Out = ur.Out.WithContext(ctx)

		// Add resume_key as header for AI Agent to forward to MCP server
		ur.Out.Header.Set("X-Authbridge-Resume", ur.Key)

		p.logger.Info("Rewrote inbound request",
			"resume_key", ur.Key,
			"path", ur.In.URL.Path)
	}
}

func (p *SidecarProxy) decideInboundRequest(ur *extendedreverseproxy.UpstreamRequest) (extendedreverseproxy.ProxyAction, error) {
	p.logger.Info("Deciding inbound request action",
		"resume_key", ur.Key,
		"path", ur.In.URL.Path)

	// Decide should ONLY be called for forward requests, not resume requests.
	// Resume requests are handled by ExtendedReverseProxy.handleResume() directly.

	// Handle initial forward request
	userID := ur.In.Header.Get("X-User-ID")
	if userID == "" {
		// No user ID, just forward normally
		p.logger.Info("No user ID, forwarding without OAuth support", "resume_key", ur.Key)
		return extendedreverseproxy.ForwardNow{}, nil
	}

	// Create OAuth session
	session := &OAuthSession{
		UserID:            userID,
		MCPServerURL:      p.defaultMCPServer,
		CreatedAt:         time.Now(),
		OAuthRequiredChan: make(chan *OAuthDiscoveryData, 1),
		OAuthCompleteChan: make(chan *OAuthCompletionData, 1),
	}
	p.putOAuthSession(ur.Key, session)

	p.logger.Info("Created OAuth session for initial request",
		"resume_key", ur.Key,
		"user_id", userID)

	// Create a detach signal channel
	detachSignalChan := make(chan extendedreverseproxy.DetachAndWait, 1)

	// Start goroutine to convert OAuth discovery to detach signal
	go func() {
		select {
		case discovery := <-session.OAuthRequiredChan:
			p.logger.Info("OAuth discovery received, signaling detach",
				"resume_key", ur.Key,
				"auth_url", discovery.AuthURL)

			// Mark session as detached
			p.sessionMu.Lock()
			session.Detached = true
			p.sessionMu.Unlock()

			// Prepare 401 response with OAuth details
			body := fmt.Sprintf(`{"error":"authentication_required","auth_url":"%s","code_verifier":"%s","mcp_server_url":"%s","resume_key":"%s"}`,
				discovery.AuthURL,
				discovery.CodeVerifier,
				session.MCPServerURL,
				ur.Key)

			// Signal ExtendedReverseProxy to detach
			detachSignalChan <- extendedreverseproxy.DetachAndWait{
				InterimStatusCode: http.StatusUnauthorized,
				InterimHeader:     http.Header{"Content-Type": []string{"application/json"}},
				InterimBody:       []byte(body),
			}

		case <-time.After(defaultOAuthFlowTimeout):
			// Timeout - no OAuth needed, normal flow completed
			p.logger.Debug("No OAuth discovery signal received (normal flow)",
				"resume_key", ur.Key)
		}
	}()

	// Use ForwardWithDetachOption to allow dynamic switching to detached mode
	return extendedreverseproxy.ForwardWithDetachOption{
		DetachSignal: detachSignalChan,
	}, nil
}

func (p *SidecarProxy) putOAuthSession(key string, session *OAuthSession) {
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	p.oauthSessions[key] = session
}

func (p *SidecarProxy) getOAuthSession(key string) (*OAuthSession, bool) {
	p.sessionMu.RLock()
	defer p.sessionMu.RUnlock()
	session, ok := p.oauthSessions[key]
	return session, ok
}

func generateResumeKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("resume-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// cleanupAfterResponse is called after AI Agent response is received and before streaming to client.
// For detached flows we keep the OAuth session alive until the resume request consumes the
// pending exchange; for direct flows we can clean up immediately.
func (p *SidecarProxy) cleanupAfterResponse(resp *http.Response) error {
	if resp.Request == nil {
		return nil
	}

	resumeKeyVal := resp.Request.Context().Value(resumeKeyContextKey)
	if resumeKeyVal == nil {
		p.logger.Warn("No resume_key in response context, skipping session cleanup")
		return nil
	}

	resumeKey, ok := resumeKeyVal.(string)
	if !ok {
		p.logger.Error("Invalid resume_key type in context")
		return nil
	}

	session, exists := p.getOAuthSession(resumeKey)
	if !exists {
		return nil
	}

	sessionDuration := time.Since(session.CreatedAt)
	if session.Detached && resp.Request.Header.Get("X-Authbridge-Resume") == "" {
		p.logger.Info("Preserving OAuth session for future resume request",
			"resume_key", resumeKey,
			"session_duration_ms", sessionDuration.Milliseconds())
		return nil
	}

	p.logger.Info("Cleaning up OAuth session after AI Agent response",
		"resume_key", resumeKey,
		"session_duration_ms", sessionDuration.Milliseconds())
	p.deleteOAuthSession(resumeKey)

	return nil
}

// deleteOAuthSession removes an OAuth session from the map.
func (p *SidecarProxy) deleteOAuthSession(key string) {
	p.sessionMu.Lock()
	defer p.sessionMu.Unlock()
	delete(p.oauthSessions, key)
}

// getCorrelationLock returns or creates a lock channel for the given correlation_id (resume_key).
// This ensures only one OAuth flow happens at a time per user request while allowing context cancellation.
func (p *SidecarProxy) getCorrelationLock(correlationID string) chan struct{} {
	p.correlationLocksMu.Lock()
	defer p.correlationLocksMu.Unlock()

	if p.correlationLocks[correlationID] == nil {
		lockCh := make(chan struct{}, 1)
		lockCh <- struct{}{}
		p.correlationLocks[correlationID] = lockCh
	}
	return p.correlationLocks[correlationID]
}

func (p *SidecarProxy) acquireCorrelationLock(ctx context.Context, correlationID string) (func(), error) {
	lockCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		lockCtx, cancel = context.WithTimeout(ctx, defaultLockWaitTimeout)
		defer cancel()
	}

	lock := p.getCorrelationLock(correlationID)

	select {
	case <-lockCtx.Done():
		return nil, lockCtx.Err()
	case <-lock:
		return func() {
			lock <- struct{}{}
		}, nil
	}
}

// ensureTokenAvailable ensures a token exists for the given user and MCP server.
// It uses a semaphore (correlation lock) to serialize OAuth flows within a single request.
// Returns the token if successful, or an error if OAuth fails.
func (p *SidecarProxy) ensureTokenAvailable(ctx context.Context, userID, mcpServerBaseURL, resumeKey, mcpFullURL string) (string, error) {
	// First check: Do we already have a token?
	if token, hasToken := p.authBridge.GetToken(userID, mcpServerBaseURL); hasToken {
		p.logger.Info("Token found in cache (fast path)",
			"user_id", userID,
			"mcp_server", mcpServerBaseURL)
		return token, nil
	}

	// No token - acquire correlation lock to serialize OAuth flows
	p.logger.Info("No token found, acquiring correlation lock",
		"user_id", userID,
		"mcp_server", mcpServerBaseURL,
		"resume_key", resumeKey)

	releaseLock, err := p.acquireCorrelationLock(ctx, resumeKey)
	if err != nil {
		p.deleteOAuthSession(resumeKey)
		return "", err
	}
	defer releaseLock()

	p.logger.Info("Correlation lock acquired, double-checking token cache",
		"resume_key", resumeKey,
		"mcp_server", mcpServerBaseURL)

	// Double-check: Token might have been added while waiting for lock
	if token, hasToken := p.authBridge.GetToken(userID, mcpServerBaseURL); hasToken {
		p.logger.Info("Token found after lock acquisition (added by another call)",
			"resume_key", resumeKey,
			"mcp_server", mcpServerBaseURL)
		return token, nil
	}

	if err := ctx.Err(); err != nil {
		p.deleteOAuthSession(resumeKey)
		return "", err
	}

	// Still no token - we're the first call for this server, do OAuth flow
	p.logger.Info("Still no token after lock, initiating OAuth discovery",
		"user_id", userID,
		"mcp_server", mcpServerBaseURL,
		"resume_key", resumeKey)

	// Send the same MCP request to the server WITHOUT Authorization header to trigger OAuth elicitation
	// Send a synthetic tools/list JSON-RPC request to trigger OAuth elicitation
	mcpJSONRPC := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	}

	reqBody, err := json.Marshal(mcpJSONRPC)
	if err != nil {
		p.deleteOAuthSession(resumeKey)
		return "", fmt.Errorf("failed to marshal synthetic MCP request: %w", err)
	}

	// Use the full MCP URL from the original request
	mcpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpFullURL, bytes.NewReader(reqBody))
	if err != nil {
		p.deleteOAuthSession(resumeKey)
		return "", fmt.Errorf("failed to create MCP discovery request: %w", err)
	}

	mcpReq.Header.Set("Content-Type", "application/json")
	mcpReq.Header.Set("Accept", "application/json, text/event-stream")

	p.logger.Info("Sending MCP request without token to trigger OAuth elicitation",
		"url", mcpReq.URL.String(),
		"method", mcpReq.Method)

	mcpResp, err := http.DefaultClient.Do(mcpReq)
	if err != nil {
		p.deleteOAuthSession(resumeKey)
		p.logger.Error("Failed to send MCP discovery request", "error", err)
		return "", fmt.Errorf("failed to send MCP discovery request: %w", err)
	}
	defer mcpResp.Body.Close()

	// Check if we got 401 (OAuth elicitation)
	if mcpResp.StatusCode != http.StatusUnauthorized {
		p.deleteOAuthSession(resumeKey)
		p.logger.Error("Expected 401 from MCP server but got different status",
			"status", mcpResp.StatusCode,
			"mcp_server", mcpServerBaseURL)
		return "", fmt.Errorf("unexpected response from MCP server: status %d", mcpResp.StatusCode)
	}

	p.logger.Info("Received 401 from MCP server - OAuth elicitation triggered",
		"www_authenticate", mcpResp.Header.Get("WWW-Authenticate"))

	// Call /auth/url endpoint to get OAuth URL and code verifier
	authURL, codeVerifier, err := p.getAuthURL(ctx, mcpServerBaseURL)
	if err != nil {
		p.deleteOAuthSession(resumeKey)
		p.logger.Error("Failed to get auth URL", "error", err)
		return "", fmt.Errorf("failed to get auth URL: %w", err)
	}

	// Get the OAuth session
	session, ok := p.getOAuthSession(resumeKey)
	if !ok {
		return "", fmt.Errorf("oauth_session_not_found")
	}
	defer func() {
		if ctx.Err() != nil {
			p.deleteOAuthSession(resumeKey)
		}
	}()

	// Update session with current MCP server and create discovery data
	p.sessionMu.Lock()
	session.MCPServerURL = mcpServerBaseURL
	oauthStartTime := time.Now()
	p.sessionMu.Unlock()

	discovery := &OAuthDiscoveryData{
		AuthURL:      authURL,
		CodeVerifier: codeVerifier,
	}

	// Signal INBOUND with discovery data (non-blocking)
	select {
	case session.OAuthRequiredChan <- discovery:
		p.logger.Info("Sent OAuth required signal to INBOUND", "resume_key", resumeKey)
	default:
		p.logger.Warn("Failed to send OAuth required signal to INBOUND (channel full)", "resume_key", resumeKey)
	}

	// Block waiting for OAuth completion from INBOUND
	oauthWaitCtx, cancelOAuthWait := context.WithTimeout(ctx, defaultOAuthFlowTimeout)
	defer cancelOAuthWait()

	select {
	case <-ctx.Done():
		p.deleteOAuthSession(resumeKey)
		return "", ctx.Err()
	case oauthData := <-session.OAuthCompleteChan:
		oauthDuration := time.Since(oauthStartTime)
		p.logger.Info("OAuth completion received",
			"user_id", userID,
			"resume_key", resumeKey,
			"has_error", oauthData.Error != "",
			"oauth_duration_ms", oauthDuration.Milliseconds())

		// Check if OAuth failed
		if oauthData.Error != "" {
			p.logger.Error("OAuth failed",
				"error", oauthData.Error,
				"resume_key", resumeKey,
				"oauth_duration_ms", oauthDuration.Milliseconds())
			p.deleteOAuthSession(resumeKey)
			return "", fmt.Errorf("oauth failed: %s", oauthData.Error)
		}

		// Validate code verifier matches what we sent
		if oauthData.CodeVerifier != codeVerifier {
			p.logger.Error("Code verifier mismatch",
				"resume_key", resumeKey,
				"expected_len", len(codeVerifier),
				"received_len", len(oauthData.CodeVerifier))
			p.deleteOAuthSession(resumeKey)
			return "", fmt.Errorf("code verifier mismatch")
		}

		if err := ctx.Err(); err != nil {
			p.deleteOAuthSession(resumeKey)
			return "", err
		}

		// Use session.MCPServerURL (not from oauthData)
		if err := p.authBridge.ExchangeToken(ctx, userID, session.MCPServerURL, oauthData.Code, oauthData.CodeVerifier); err != nil {
			p.deleteOAuthSession(resumeKey)
			p.logger.Error("Token exchange failed",
				"error", err,
				"resume_key", resumeKey,
				"oauth_duration_ms", oauthDuration.Milliseconds())
			return "", fmt.Errorf("token exchange failed: %w", err)
		}

		p.logger.Info("Token exchange successful, token cached",
			"user_id", userID,
			"resume_key", resumeKey,
			"mcp_server", session.MCPServerURL,
			"oauth_duration_ms", oauthDuration.Milliseconds())

		// Get the newly cached token
		token, hasToken := p.authBridge.GetToken(userID, mcpServerBaseURL)
		if !hasToken {
			p.deleteOAuthSession(resumeKey)
			return "", fmt.Errorf("token_not_cached_after_exchange")
		}
		return token, nil

	case <-oauthWaitCtx.Done():
		p.deleteOAuthSession(resumeKey)
		if errors.Is(oauthWaitCtx.Err(), context.DeadlineExceeded) {
			p.logger.Error("OAuth timeout", "resume_key", resumeKey)
			return "", fmt.Errorf("oauth timeout")
		}
		return "", oauthWaitCtx.Err()
	}
}

// Made with Bob

// getAuthURL calls the MCP server's /auth/url endpoint to get OAuth URL and code verifier
func (p *SidecarProxy) getAuthURL(ctx context.Context, mcpServerURL string) (string, string, error) {
	authURLEndpoint := mcpServerURL + "/auth/url"
	authURLReq := map[string]string{
		"callback_url": "", // Empty callback URL - MCP server will use its configured redirect URI
	}
	jsonData, err := json.Marshal(authURLReq)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal auth URL request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, authURLEndpoint, bytes.NewReader(jsonData))
	if err != nil {
		return "", "", fmt.Errorf("failed to create auth URL request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	authResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return "", "", fmt.Errorf("failed to get auth URL: %w", err)
	}
	defer authResp.Body.Close()

	authBody, err := io.ReadAll(authResp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read auth response: %w", err)
	}

	if authResp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("auth URL endpoint returned status %d: %s", authResp.StatusCode, string(authBody))
	}

	var authURLResp struct {
		URL          string `json:"url"`
		CodeVerifier string `json:"code_verifier"`
	}
	if err := json.Unmarshal(authBody, &authURLResp); err != nil {
		return "", "", fmt.Errorf("failed to unmarshal auth response: %w", err)
	}

	p.logger.Info("OAuth URL obtained from MCP server",
		"auth_url", authURLResp.URL,
		"code_verifier_len", len(authURLResp.CodeVerifier))

	return authURLResp.URL, authURLResp.CodeVerifier, nil
}
