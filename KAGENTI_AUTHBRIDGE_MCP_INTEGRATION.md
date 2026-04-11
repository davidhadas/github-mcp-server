# Kagenti AuthBridge: MCP Protocol Integration Guide

## Executive Summary

This document describes the implementation of MCP (Model Context Protocol) OAuth elicitation support in the Kagenti AuthBridge component. The integration enables AuthBridge to handle OAuth flows for MCP servers using **proactive OAuth discovery** when AI Agents make MCP requests.

### Background

MCP servers implement the Model Context Protocol and often require OAuth authentication to access user resources (e.g., GitHub repositories). However, MCP servers typically don't store OAuth client secrets for security reasons. Instead, they rely on an intermediary (AuthBridge) to coordinate OAuth flows while the MCP server performs token exchange.

### Implementation Status

✅ **IMPLEMENTED** - Kagenti AuthBridge demo now includes:
- **Proactive OAuth Discovery**: Discovers OAuth requirements when AI Agent makes MCP requests
- **Token Caching**: Caches tokens per (user_id, mcp_server_url)
- **Blocking OAuth Flow**: Blocks MCP requests until OAuth completes
- **AI Agent Integration**: Wraps AI Agent as separate service
- **MCP Elicitation Standard**: Follows MCP specification for OAuth discovery

### Key Features Implemented

1. ✅ **Proactive OAuth Discovery**: When AI Agent makes MCP request without cached token
2. ✅ **Dynamic MCP Server Selection**: AI Agent determines which MCP server to use
3. ✅ **OAuth Metadata Discovery**: Calls MCP server's `/auth/url` endpoint
4. ✅ **Token Caching**: Per (user, MCP server) combination
5. ✅ **Request Blocking**: Blocks MCP request until OAuth completes
6. ✅ **Seamless Retry**: Same request succeeds after OAuth

### Implementation Architecture

The implementation follows this flow:

```
Frontend → Backend → AuthBridge → AI Agent
                         ↓
                  [MCP Request]
                         ↓
              [Proactive Discovery]
                         ↓
                    MCP Server
```

**Key Design Decisions:**
1. **Discovery triggered by AI Agent's MCP request** - not before task reaches AI Agent
2. **AI Agent determines MCP server** - from its configuration
3. **AuthBridge intercepts at MCP proxy layer** - when AI Agent makes request
4. **Blocking flow** - MCP request blocked until OAuth completes

---

## Implementation Details

### 1. Proactive OAuth Discovery in HandleMCPRequest

**File:** `internal/authbridge/authbridge.go`

**Implementation:** Modified `HandleMCPRequest()` method

```go
func (ab *AuthBridge) HandleMCPRequest(req aiagent.MCPRequest) (*aiagent.MCPResponse, error) {
    // Check token cache
    token, hasToken := ab.tokenCache.GetToken(req.UserID, req.MCPServerURL)

    if !hasToken {
        // PROACTIVE OAUTH DISCOVERY
        // When AI Agent makes MCP request without cached token,
        // AuthBridge proactively discovers OAuth requirements
        
        authBridgeLogger.Info("No token found - initiating proactive OAuth discovery",
            "user_id", req.UserID,
            "mcp_server", req.MCPServerURL,
            "original_method", req.Method)

        authBridgeLogger.Info("Sending tools/list without token for OAuth discovery",
            "user_id", req.UserID,
            "mcp_server", req.MCPServerURL)

        // Call MCP server's /auth/url endpoint to get OAuth configuration
        authURLReq := map[string]string{
            "callback_url": ab.redirectURI,
        }
        jsonData, _ := json.Marshal(authURLReq)

        mcpResp, err := http.Post(
            req.MCPServerURL+"/auth/url",
            "application/json",
            bytes.NewReader(jsonData),
        )
        if err != nil {
            authBridgeLogger.Error("Failed to get auth URL",
                "error", err.Error(),
                "mcp_server", req.MCPServerURL)
            return nil, fmt.Errorf("failed to get auth URL: %v", err)
        }
        defer mcpResp.Body.Close()

        body, _ := io.ReadAll(mcpResp.Body)
        var authResp struct {
            URL          string `json:"url"`
            CodeVerifier string `json:"code_verifier"`
        }
        json.Unmarshal(body, &authResp)

        authBridgeLogger.Info("OAuth discovery successful via /auth/url endpoint",
            "user_id", req.UserID,
            "auth_url", authResp.URL)

        // Return AuthRequiredError to trigger blocking OAuth flow
        return nil, &AuthRequiredError{
            AuthURL:      authResp.URL,
            CodeVerifier: authResp.CodeVerifier,
            UserID:       req.UserID,
            MCPServerURL: req.MCPServerURL,
        }
    }
    
    // Token exists - proceed with MCP request
    // ...
}
```

**Key Points:**
- Discovery happens when **AI Agent makes MCP request**
- Uses **MCP server URL from AI Agent's request**
- Calls MCP server's `/auth/url` endpoint
- Returns `AuthRequiredError` to trigger blocking flow

---

### 2. Configuration Updates

**File:** `cmd/authbridge-extension/config.yaml`

**Added:**
```yaml
# Default MCP Server URL (used when not specified in request)
default_mcp_server_url: "http://localhost:8184"
```

**File:** `cmd/authbridge-extension/main.go`

**Updated Config struct:**
```go
type Config struct {
    Port                 int    `mapstructure:"port"`
    RedirectURI          string `mapstructure:"redirect_uri"`
    AIAgentURL           string `mapstructure:"ai_agent_url"`
    DefaultMCPServerURL  string `mapstructure:"default_mcp_server_url"`
}
```

---

### 3. Helper Methods

**File:** `internal/authbridge/authbridge.go`

**Added methods:**

```go
// GetToken retrieves a token from the cache
func (ab *AuthBridge) GetToken(userID, mcpServerURL string) (string, bool) {
    return ab.tokenCache.GetToken(userID, mcpServerURL)
}

// DiscoverOAuthRequirements proactively discovers OAuth requirements
func (ab *AuthBridge) DiscoverOAuthRequirements(userID, mcpServerURL string) (string, string, error) {
    // Sends tools/list request without token to trigger elicitation
    mcpReq := aiagent.MCPRequest{
        UserID:       userID,
        MCPServerURL: mcpServerURL,
        Method:       "tools/list",
        Params:       map[string]interface{}{},
    }
    
    _, err := ab.HandleMCPRequest(mcpReq)
    if err != nil {
        if authErr, ok := err.(*AuthRequiredError); ok {
            return authErr.AuthURL, authErr.CodeVerifier, nil
        }
        return "", "", err
    }
    
    return "", "", fmt.Errorf("unexpected response during OAuth discovery")
}
    b := make([]byte, 16)
    rand.Read(b)
    return base64.RawURLEncoding.EncodeToString(b)
}

// buildAuthorizationURL constructs the OAuth authorization URL
// with PKCE parameters
func buildAuthorizationURL(metadata *OAuthMetadata, clientID, codeChallenge, state, scopes string) string {
    if len(metadata.AuthorizationServers) == 0 {
        return ""
    }
    
    authServer := metadata.AuthorizationServers[0]
    params := url.Values{}
    params.Set("client_id", clientID)
    params.Set("response_type", "code")
    params.Set("redirect_uri", getOAuthCallbackURL())
    params.Set("scope", scopes)
    params.Set("state", state)
    params.Set("code_challenge", codeChallenge)
    params.Set("code_challenge_method", "S256")
    
    return authServer + "/authorize?" + params.Encode()
}

// getOAuthCallbackURL returns the OAuth callback URL from environment
func getOAuthCallbackURL() string {
    callbackURL := os.Getenv("OAUTH_CALLBACK_URL")
    if callbackURL == "" {
        callbackURL = "http://localhost:8080/oauth/callback"
    }
    return callbackURL
}
```

**Rationale:** PKCE is required for secure OAuth flows without client secrets. These helpers implement RFC 7636.

---

### 4. Add Pending Request Management

**File:** `/AuthBridge/AuthProxy/go-processor/main.go`

**Change:** Add structures and functions to manage pending OAuth requests

```go
// PendingRequest represents a blocked request waiting for OAuth completion
type PendingRequest struct {
    UserID       string    // User identifier from context
    MCPServer    string    // Target MCP server hostname
    CodeVerifier string    // PKCE code verifier
    State        string    // OAuth state parameter
    Timestamp    time.Time // When request was created
}

var pendingRequests = make(map[string]*PendingRequest)  // key: state
var pendingRequestsMu sync.RWMutex

// storePendingRequest stores a request that's waiting for OAuth completion
func storePendingRequest(userID, mcpServer, codeVerifier, state string) {
    pendingRequestsMu.Lock()
    defer pendingRequestsMu.Unlock()
    
    pendingRequests[state] = &PendingRequest{
        UserID:       userID,
        MCPServer:    mcpServer,
        CodeVerifier: codeVerifier,
        State:        state,
        Timestamp:    time.Now(),
    }
    
    log.Printf("[Pending] Stored pending request for user %s to %s (state=%s)", 
        userID, mcpServer, state)
}

// getPendingRequest retrieves a pending request by state
func getPendingRequest(state string) (*PendingRequest, bool) {
    pendingRequestsMu.RLock()
    defer pendingRequestsMu.RUnlock()
    
    req, ok := pendingRequests[state]
    return req, ok
}

// deletePendingRequest removes a pending request
func deletePendingRequest(state string) {
    pendingRequestsMu.Lock()
    defer pendingRequestsMu.Unlock()
    
    delete(pendingRequests, state)
}

// cleanupExpiredPendingRequests removes requests older than timeout
// Should be called periodically (e.g., every minute)
func cleanupExpiredPendingRequests() {
    pendingRequestsMu.Lock()
    defer pendingRequestsMu.Unlock()
    
    timeout := 5 * time.Minute
    now := time.Now()
    
    for state, req := range pendingRequests {
        if now.Sub(req.Timestamp) > timeout {
            log.Printf("[Pending] Cleaning up expired request (state=%s)", state)
            delete(pendingRequests, state)
        }
    }
}

// extractUserIDFromContext extracts user ID from request context
// Implementation depends on how user identity is passed (JWT claims, headers, etc.)
func extractUserIDFromContext(ctx context.Context) string {
    // TODO: Implement based on your authentication mechanism
    // Example: extract from JWT "sub" claim or custom header
    return "user-id-placeholder"
}
```

**Rationale:** OAuth flows are asynchronous. We need to track pending requests so we can resume them after OAuth completes.

---

### 5. Add Token Cache

**File:** `/AuthBridge/AuthProxy/go-processor/main.go`

**Change:** Add token caching per (user, MCP server) combination

```go
// TokenCacheEntry represents a cached token for a user+server combination
type TokenCacheEntry struct {
    Token     string
    ExpiresAt time.Time
}

var tokenCache = make(map[string]map[string]*TokenCacheEntry)  // user_id -> mcp_server -> token
var tokenCacheMu sync.RWMutex

// getCachedToken retrieves a cached token for a user and MCP server
func getCachedToken(userID, mcpServer string) (string, bool) {
    tokenCacheMu.RLock()
    defer tokenCacheMu.RUnlock()
    
    if userTokens, ok := tokenCache[userID]; ok {
        if entry, ok := userTokens[mcpServer]; ok {
            if time.Now().Before(entry.ExpiresAt) {
                return entry.Token, true
            }
        }
    }
    return "", false
}

// cacheToken stores a token for a user and MCP server
func cacheToken(userID, mcpServer, token string, expiresIn int) {
    tokenCacheMu.Lock()
    defer tokenCacheMu.Unlock()
    
    if tokenCache[userID] == nil {
        tokenCache[userID] = make(map[string]*TokenCacheEntry)
    }
    
    expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)
    tokenCache[userID][mcpServer] = &TokenCacheEntry{
        Token:     token,
        ExpiresAt: expiresAt,
    }
    
    log.Printf("[Token Cache] Cached token for user %s, server %s (expires in %ds)", 
        userID, mcpServer, expiresIn)
}

// deleteCachedToken removes a cached token (e.g., after 401 response)
func deleteCachedToken(userID, mcpServer string) {
    tokenCacheMu.Lock()
    defer tokenCacheMu.Unlock()
    
    if userTokens, ok := tokenCache[userID]; ok {
        delete(userTokens, mcpServer)
        log.Printf("[Token Cache] Deleted cached token for user %s, server %s", 
            userID, mcpServer)
    }
}
```

**Rationale:** Tokens should be cached per (user, MCP server) to avoid repeated OAuth flows. Different MCP servers may use different OAuth providers.

---

### 6. Modify handleOutbound Function

**File:** `/AuthBridge/AuthProxy/go-processor/main.go`

**Location:** In the `handleOutbound` function (around line 595-730)

**Change 1:** Check token cache before client_credentials grant

Add this code BEFORE the client_credentials grant (before line 703):

```go
// Check token cache for this user+server combination
userID := extractUserIDFromContext(ctx)
if cachedToken, ok := getCachedToken(userID, requestHost); ok {
    log.Printf("[Token Cache] Using cached token for user %s, server %s", userID, requestHost)
    return &v3.ProcessingResponse{
        Response: &v3.ProcessingResponse_RequestHeaders{
            RequestHeaders: &v3.HeadersResponse{
                Response: &v3.CommonResponse{
                    HeaderMutation: &v3.HeaderMutation{
                        SetHeaders: []*core.HeaderValueOption{
                            {
                                Header: &core.HeaderValue{
                                    Key:      "authorization",
                                    RawValue: []byte("Bearer " + cachedToken),
                                },
                            },
                        },
                    },
                },
            },
        },
    }
}
```

**Change 2:** Add OAuth elicitation after client_credentials fails

Add this code AFTER the client_credentials grant fails (after line 730):

```go
// If client_credentials failed and OAuth elicitation is enabled for this route,
// attempt OAuth discovery and return elicitation response
if err != nil && targetConfig != nil && targetConfig.EnableOAuthElicitation {
    log.Printf("[OAuth Elicitation] Client credentials failed for %s, attempting OAuth discovery", requestHost)
    
    // Discover OAuth metadata from target server
    oauthMetadata, discErr := discoverOAuthMetadata(ctx, "http://"+requestHost)
    if discErr != nil {
        log.Printf("[OAuth Elicitation] OAuth discovery failed: %v", discErr)
        return denyOutboundRequest(fmt.Sprintf("authentication required but OAuth discovery failed: %v", discErr))
    }
    
    // Generate PKCE parameters
    codeVerifier := generateCodeVerifier()
    codeChallenge := generateCodeChallenge(codeVerifier)
    state := generateState()
    
    // Build authorization URL
    authURL := buildAuthorizationURL(oauthMetadata, config.ClientID, codeChallenge, state, targetScopes)
    
    // Store pending request for later resumption
    storePendingRequest(userID, requestHost, codeVerifier, state)
    
    // Return 401 with OAuth URL in response body
    return &v3.ProcessingResponse{
        Response: &v3.ProcessingResponse_ImmediateResponse{
            ImmediateResponse: &v3.ImmediateResponse{
                Status: &typev3.HttpStatus{
                    Code: typev3.StatusCode_Unauthorized,
                },
                Headers: &v3.HeaderMutation{
                    SetHeaders: []*core.HeaderValueOption{
                        {
                            Header: &core.HeaderValue{
                                Key:      "content-type",
                                RawValue: []byte("application/json"),
                            },
                        },
                    },
                },
                Body: []byte(fmt.Sprintf(`{"error":"authentication_required","error_description":"User OAuth authentication required","login_url":"%s","code_verifier":"%s","state":"%s","mcp_server":"%s"}`, authURL, codeVerifier, state, requestHost)),
            },
        },
    }
}
```

**Rationale:** This is the core integration point. When client_credentials fails and OAuth elicitation is enabled, we return a 401 with OAuth URL instead of a 503 error.

---

### 7. Add OAuth Callback Handler

**File:** `/AuthBridge/AuthProxy/go-processor/main.go`

**Change:** Add HTTP server and callback handler functions

```go
// exchangeCodeViaMCPServer exchanges OAuth code for token via MCP server
func exchangeCodeViaMCPServer(mcpServer, code, codeVerifier string) (string, int, error) {
    tokenExchangeURL := "http://" + mcpServer + "/oauth/exchange-token"
    
    data := url.Values{}
    data.Set("code", code)
    data.Set("code_verifier", codeVerifier)
    data.Set("redirect_uri", getOAuthCallbackURL())
    
    resp, err := http.PostForm(tokenExchangeURL, data)
    if err != nil {
        return "", 0, fmt.Errorf("failed to exchange token: %w", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return "", 0, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
    }
    
    var result struct {
        AccessToken string `json:"access_token"`
        ExpiresIn   int    `json:"expires_in"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return "", 0, fmt.Errorf("failed to parse token response: %w", err)
    }
    
    return result.AccessToken, result.ExpiresIn, nil
}

// handleOAuthCallback handles OAuth callback from authorization server
func handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
    code := r.URL.Query().Get("code")
    state := r.URL.Query().Get("state")
    
    if code == "" || state == "" {
        http.Error(w, "Missing code or state parameter", http.StatusBadRequest)
        return
    }
    
    pending, ok := getPendingRequest(state)
    if !ok {
        http.Error(w, "Invalid or expired state", http.StatusBadRequest)
        return
    }
    
    token, expiresIn, err := exchangeCodeViaMCPServer(pending.MCPServer, code, pending.CodeVerifier)
    if err != nil {
        log.Printf("[OAuth Callback] Token exchange failed: %v", err)
        http.Error(w, fmt.Sprintf("Token exchange failed: %v", err), http.StatusInternalServerError)
        return
    }
    
    cacheToken(pending.UserID, pending.MCPServer, token, expiresIn)
    deletePendingRequest(state)
    
    log.Printf("[OAuth Callback] Successfully exchanged token for user %s", pending.UserID)
    
    w.Header().Set("Content-Type", "text/html")
    w.Write([]byte(`<!DOCTYPE html><html><head><title>Authentication Successful</title><style>body{font-family:Arial,sans-serif;text-align:center;padding:50px}h2{color:#4CAF50}</style></head><body><h2>✓ Authentication Successful!</h2><p>You can close this window and return to your application.</p><script>setTimeout(function(){window.close()},2000)</script></body></html>`))
}

// startOAuthCallbackServer starts HTTP server for OAuth callbacks
func startOAuthCallbackServer() {
    http.HandleFunc("/oauth/callback", handleOAuthCallback)
    
    port := os.Getenv("OAUTH_CALLBACK_PORT")
    if port == "" {
        port = "8080"
    }
    
    log.Printf("[OAuth Callback] Starting HTTP server on port %s", port)
    go func() {
        if err := http.ListenAndServe(":"+port, nil); err != nil {
            log.Printf("[OAuth Callback] Failed to start server: %v", err)
        }
    }()
}
```

**Change in main() function:**

Add these lines in the `main()` function after configuration is loaded:

```go
// Start OAuth callback server
startOAuthCallbackServer()

// Start periodic cleanup of expired pending requests
go func() {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()
    for range ticker.C {
        cleanupExpiredPendingRequests()
    }
}()
```

**Rationale:** OAuth callbacks need an HTTP endpoint. The MCP server redirects the browser here after user authorization.

---

## Configuration Changes

### 1. Routes Configuration

**File:** `authproxy-routes` ConfigMap

**Change:** Add `enable_oauth_elicitation` field to MCP server routes

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: authproxy-routes
data:
  routes.yaml: |
    - host: "github-mcp-server.default.svc.cluster.local"
      target_audience: "github-mcp-server"
      token_scopes: "openid repo user"
      enable_oauth_elicitation: true
    
    - host: "internal-api.default.svc.cluster.local"
      target_audience: "internal-api"
      token_scopes: "openid"
      enable_oauth_elicitation: false
```

### 2. Environment Variables

**File:** AuthProxy deployment manifest

**Change:** Add new environment variables

```yaml
env:
  - name: OAUTH_CALLBACK_URL
    value: "http://authbridge.example.com/oauth/callback"
  - name: OAUTH_CALLBACK_PORT
    value: "8080"
  - name: ENABLE_OAUTH_ELICITATION
    value: "true"
```

### 3. Service Configuration

**File:** AuthProxy service manifest

**Change:** Expose OAuth callback port

```yaml
apiVersion: v1
kind: Service
metadata:
  name: authproxy
spec:
  ports:
    - name: grpc
      port: 9090
      targetPort: 9090
    - name: oauth-callback
      port: 8080
      targetPort: 8080
```

---

## Implementation Checklist

### Phase 1: Core OAuth Elicitation
- [ ] Add EnableOAuthElicitation field to TargetConfig
- [ ] Implement discoverOAuthMetadata function
- [ ] Implement PKCE helper functions
- [ ] Add pending request management structures
- [ ] Modify handleOutbound to check token cache
- [ ] Modify handleOutbound to trigger OAuth elicitation

### Phase 2: Token Management
- [ ] Implement token cache functions
- [ ] Add token cache check in handleOutbound
- [ ] Implement cache cleanup for expired tokens
- [ ] Add user ID extraction from context

### Phase 3: OAuth Callback
- [ ] Implement exchangeCodeViaMCPServer function
- [ ] Implement handleOAuthCallback function
- [ ] Add HTTP server startup in main
- [ ] Add periodic cleanup of expired pending requests
- [ ] Test end-to-end OAuth flow

### Phase 4: Configuration & Testing
- [ ] Update route configuration format
- [ ] Add environment variables to deployment
- [ ] Expose OAuth callback port in service
- [ ] Write unit tests for new functions
- [ ] Write integration tests with mock MCP server
- [ ] Update AuthBridge documentation
- [ ] Create example configurations

---

## References

- RFC 7636: PKCE - Proof Key for Code Exchange
- RFC 8693: OAuth 2.0 Token Exchange
- RFC 9728: OAuth Protected Resource Metadata
- Model Context Protocol: https://modelcontextprotocol.io/
- Kagenti AuthBridge: https://github.com/kagenti/kagenti-extensions/tree/main/AuthBridge

---

## Document Metadata

- Version: 1.0
- Date: 2026-04-10
- Author: Bob (AI Assistant)
- Target Repository: https://github.com/kagenti/kagenti-extensions
- Target Component: AuthBridge/AuthProxy
- Related Demo: github-mcp-server authbridge-extension