# AuthBridge Sidecar Architecture Plan

## Executive Summary

This document outlines the plan to refactor the Kagenti architecture to deploy the AI Agent as a Kubernetes pod with AuthBridge running as a **true sidecar proxy** that intercepts and handles all inbound and outbound traffic. AuthBridge will not expose any endpoints - it operates transparently as a network proxy.

## Current Architecture

### Current Deployment Model
```
┌─────────────────────────────────────────────────────────────┐
│                        Separate Processes                    │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌──────────┐      ┌──────────────┐      ┌──────────────┐  │
│  │ Backend  │─────▶│  AuthBridge  │─────▶│  AI Agent    │  │
│  │  :8187   │      │    :8185     │      │    :8186     │  │
│  └──────────┘      └──────────────┘      └──────────────┘  │
│                            │                      │          │
│                            │                      │          │
│                            └──────────────────────┘          │
│                                     │                        │
│                                     ▼                        │
│                            ┌──────────────┐                 │
│                            │  MCP Server  │                 │
│                            │    :8184     │                 │
│                            └──────────────┘                 │
└─────────────────────────────────────────────────────────────┘
```

**Issues with Current Architecture:**
- Three separate processes to manage
- AuthBridge acts as a separate service with endpoints
- AI Agent must explicitly call AuthBridge
- Complex routing and service discovery
- Not a true sidecar pattern

## Proposed Architecture: True Sidecar Proxy

### New Deployment Model
```
┌─────────────────────────────────────────────────────────────┐
│                      Kubernetes Cluster                      │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌──────────┐                                                │
│  │ Backend  │                                                │
│  │  Pod     │                                                │
│  │  :8187   │                                                │
│  └────┬─────┘                                                │
│       │                                                       │
│       │ HTTP Request                                          │
│       ▼                                                       │
│  ┌─────────────────────────────────────────────────────┐    │
│  │              AI Agent Pod                            │    │
│  │  ┌─────────────────────────────────────────────┐    │    │
│  │  │         Shared Network Namespace            │    │    │
│  │  │                                              │    │    │
│  │  │  ┌──────────────┐    ┌──────────────┐      │    │    │
│  │  │  │  AI Agent    │    │  AuthBridge  │      │    │    │
│  │  │  │  Container   │    │   Sidecar    │      │    │    │
│  │  │  │              │    │   (Proxy)    │      │    │    │
│  │  │  │  Listens on  │    │              │      │    │    │
│  │  │  │  127.0.0.1   │    │  Intercepts  │      │    │    │
│  │  │  │  :8186       │    │  all traffic │      │    │    │
│  │  │  └──────────────┘    └──────┬───────┘      │    │    │
│  │  │                              │              │    │    │
│  │  │         Traffic flows through sidecar      │    │    │
│  │  │         (iptables redirect)                 │    │    │
│  │  └─────────────────────────────────────────────┘    │    │
│  │                                  │                   │    │
│  └──────────────────────────────────┼───────────────────┘    │
│                                     │                        │
│                                     │ Outbound to MCP        │
│                                     ▼                        │
│                            ┌──────────────┐                 │
│                            │  MCP Server  │                 │
│                            │     Pod      │                 │
│                            │    :8184     │                 │
│                            └──────────────┘                 │
└─────────────────────────────────────────────────────────────┘
```

### Key Architectural Changes

#### 1. **True Sidecar Pattern**

**AuthBridge as Transparent Proxy:**
- No HTTP endpoints exposed by AuthBridge
- Uses iptables to intercept all pod traffic
- AI Agent is unaware of the proxy (transparent)
- Handles both inbound and outbound traffic

**Traffic Flow:**

**Inbound (Backend → AI Agent):**
```
Backend Request → Pod IP:8186 
    ↓
AuthBridge intercepts (iptables)
    ↓
Checks for OAuth code in request
    ↓
If OAuth code present: Exchange token, cache it
    ↓
Forward to AI Agent (127.0.0.1:8186)
    ↓
AI Agent processes task
    ↓
Response flows back through AuthBridge
```

**Outbound (AI Agent → MCP Server):**
```
AI Agent makes HTTP request to MCP Server
    ↓
AuthBridge intercepts (iptables)
    ↓
Check token cache for (user_id, mcp_server)
    ↓
If no token: Trigger OAuth discovery
    ↓
If token exists: Add Authorization header
    ↓
Forward to MCP Server
    ↓
Response flows back to AI Agent
```

#### 2. **Network Configuration**

**iptables Rules (in AuthBridge init container):**
```bash
# Redirect all outbound traffic to AuthBridge proxy
iptables -t nat -A OUTPUT -p tcp -j REDIRECT --to-port 15001

# Redirect all inbound traffic to AuthBridge proxy  
iptables -t nat -A PREROUTING -p tcp -j REDIRECT --to-port 15001

# Exclude AuthBridge's own traffic
iptables -t nat -A OUTPUT -m owner --uid-owner 1337 -j RETURN
```

**Port Allocation:**
- AI Agent: Listens on 127.0.0.1:8186 (internal)
- AuthBridge Proxy: Listens on 0.0.0.0:15001 (intercept port)
- Pod Service: Exposes 8186 (external access)

#### 3. **Benefits of True Sidecar**

**Transparency:**
- AI Agent code unchanged (no explicit AuthBridge calls)
- No service discovery needed
- Works with any HTTP client

**Security:**
- All traffic flows through AuthBridge
- Centralized OAuth token management
- No way to bypass authentication

**Simplicity:**
- Single point of traffic control
- Easier to debug (all traffic visible)
- Standard sidecar pattern

**Performance:**
- No additional network hops
- Shared network namespace
- Minimal overhead

## Implementation Plan

### Phase 1: AuthBridge Sidecar Proxy

#### 1.1 AuthBridge as Pure Transparent Proxy (NO HTTP Endpoints)

**File:** `cmd/authbridge-sidecar/main.go` (new)

**Key Design Principles:**
- **NO HTTP endpoints** - Pure TCP proxy
- Intercepts all inbound and outbound traffic via iptables
- Extracts OAuth code from query parameters in inbound requests
- Adds Authorization headers to outbound MCP requests
- Completely transparent to AI Agent

```go
package main

import (
    "bufio"
    "fmt"
    "io"
    "log/slog"
    "net"
    "net/http"
    "net/url"
    "os"
    "strings"
    
    "github.com/github/github-mcp-server/internal/authbridge"
)

// SidecarProxy intercepts all pod traffic - NO HTTP ENDPOINTS
type SidecarProxy struct {
    authBridge *authbridge.AuthBridge
    logger     *slog.Logger
}

func main() {
    logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
    
    // Create AuthBridge instance (no callback URL needed - frontend handles OAuth)
    ab := authbridge.NewAuthBridge("")
    
    proxy := &SidecarProxy{
        authBridge: ab,
        logger:     logger,
    }
    
    // Listen on intercept port (iptables redirects ALL traffic here)
    listener, err := net.Listen("tcp", ":15001")
    if err != nil {
        logger.Error("Failed to start proxy", "error", err)
        os.Exit(1)
    }
    
    logger.Info("AuthBridge sidecar proxy started - NO HTTP ENDPOINTS",
        "port", 15001,
        "mode", "transparent_proxy")
    
    // Accept connections and handle them
    for {
        conn, err := listener.Accept()
        if err != nil {
            logger.Error("Failed to accept connection", "error", err)
            continue
        }
        
        go proxy.handleConnection(conn)
    }
}

func (p *SidecarProxy) handleConnection(clientConn net.Conn) {
    defer clientConn.Close()
    
    // Parse HTTP request from raw TCP
    reader := bufio.NewReader(clientConn)
    req, err := http.ReadRequest(reader)
    if err != nil {
        p.logger.Error("Failed to read request", "error", err)
        return
    }
    
    // Determine traffic direction
    if p.isInboundRequest(req) {
        p.handleInbound(clientConn, req)
    } else {
        p.handleOutbound(clientConn, req)
    }
}

func (p *SidecarProxy) isInboundRequest(req *http.Request) bool {
    // Inbound: requests TO AI Agent (port 8186)
    // Outbound: requests FROM AI Agent to external services
    return strings.Contains(req.Host, ":8186") ||
           req.URL.Port() == "8186" ||
           req.RequestURI == "/task" // AI Agent's task endpoint
}

func (p *SidecarProxy) handleInbound(clientConn net.Conn, req *http.Request) {
    p.logger.Info("Inbound request intercepted",
        "path", req.URL.Path,
        "from", clientConn.RemoteAddr())
    
    // Extract OAuth parameters from query string (if present)
    query := req.URL.Query()
    code := query.Get("code")
    codeVerifier := query.Get("code_verifier")
    
    if code != "" && codeVerifier != "" {
        // Frontend included OAuth code in retry request
        userID := req.Header.Get("X-User-ID")
        if userID == "" {
            userID = query.Get("user_id")
        }
        
        mcpServerURL := query.Get("mcp_server_url")
        if mcpServerURL == "" {
            mcpServerURL = os.Getenv("DEFAULT_MCP_SERVER_URL")
        }
        
        p.logger.Info("OAuth code detected in request, exchanging for token",
            "user_id", userID,
            "mcp_server", mcpServerURL)
        
        // Exchange code for token via MCP server
        err := p.authBridge.ExchangeToken(userID, mcpServerURL, code, codeVerifier)
        if err != nil {
            p.logger.Error("Token exchange failed", "error", err)
            p.sendErrorResponse(clientConn, http.StatusInternalServerError,
                "Token exchange failed: "+err.Error())
            return
        }
        
        p.logger.Info("Token exchange successful, token cached",
            "user_id", userID,
            "mcp_server", mcpServerURL)
        
        // Remove OAuth parameters from URL before forwarding
        cleanQuery := url.Values{}
        for k, v := range query {
            if k != "code" && k != "code_verifier" && k != "mcp_server_url" {
                cleanQuery[k] = v
            }
        }
        req.URL.RawQuery = cleanQuery.Encode()
    }
    
    // Forward clean request to AI Agent (127.0.0.1:8186)
    p.forwardToAIAgent(clientConn, req)
}

func (p *SidecarProxy) handleOutbound(clientConn net.Conn, req *http.Request) {
    p.logger.Info("Outbound request intercepted",
        "host", req.Host,
        "path", req.URL.Path)
    
    // Extract user context from request headers
    userID := req.Header.Get("X-User-ID")
    if userID == "" {
        p.logger.Warn("No user ID in outbound request, forwarding without auth")
        p.forwardToUpstream(clientConn, req)
        return
    }
    
    // Check if this is an MCP server request
    if !p.isMCPServerRequest(req) {
        p.logger.Debug("Not an MCP request, forwarding as-is", "host", req.Host)
        p.forwardToUpstream(clientConn, req)
        return
    }
    
    mcpServerURL := fmt.Sprintf("http://%s", req.Host)
    
    // Check token cache
    token, hasToken := p.authBridge.GetToken(userID, mcpServerURL)
    
    if !hasToken {
        p.logger.Info("No token found, triggering OAuth discovery",
            "user_id", userID,
            "mcp_server", mcpServerURL)
        
        // Discover OAuth requirements from MCP server
        authURL, codeVerifier, err := p.authBridge.DiscoverOAuthRequirements(userID, mcpServerURL)
        if err != nil {
            p.logger.Error("OAuth discovery failed", "error", err)
            p.sendErrorResponse(clientConn, http.StatusUnauthorized,
                "Authentication required but OAuth discovery failed")
            return
        }
        
        // Return 401 with OAuth URL to frontend
        // Frontend will open OAuth URL, get code, and retry with code in query params
        p.sendOAuthRequiredResponse(clientConn, authURL, codeVerifier, mcpServerURL)
        return
    }
    
    // Add Authorization header with cached token
    req.Header.Set("Authorization", "Bearer "+token)
    p.logger.Info("Added cached token to request", "user_id", userID)
    
    // Forward to MCP server with auth
    p.forwardToUpstream(clientConn, req)
}

func (p *SidecarProxy) forwardToAIAgent(clientConn net.Conn, req *http.Request) {
    // Connect to AI Agent on localhost
    agentConn, err := net.Dial("tcp", "127.0.0.1:8186")
    if err != nil {
        p.logger.Error("Failed to connect to AI Agent", "error", err)
        p.sendErrorResponse(clientConn, http.StatusServiceUnavailable,
            "AI Agent unavailable")
        return
    }
    defer agentConn.Close()
    
    // Write request to AI Agent
    if err := req.Write(agentConn); err != nil {
        p.logger.Error("Failed to write request to AI Agent", "error", err)
        return
    }
    
    // Copy response back to client
    io.Copy(clientConn, agentConn)
}

func (p *SidecarProxy) forwardToUpstream(clientConn net.Conn, req *http.Request) {
    // Connect to upstream service
    upstreamConn, err := net.Dial("tcp", req.Host)
    if err != nil {
        p.logger.Error("Failed to connect to upstream",
            "error", err,
            "host", req.Host)
        p.sendErrorResponse(clientConn, http.StatusServiceUnavailable,
            "Upstream service unavailable")
        return
    }
    defer upstreamConn.Close()
    
    // Write request to upstream
    if err := req.Write(upstreamConn); err != nil {
        p.logger.Error("Failed to write request to upstream", "error", err)
        return
    }
    
    // Copy response back to client
    io.Copy(clientConn, upstreamConn)
}

func (p *SidecarProxy) isMCPServerRequest(req *http.Request) bool {
    // Check if request is to MCP server
    return strings.Contains(req.Host, "mcp-server") ||
           strings.Contains(req.Host, ":8184") ||
           strings.Contains(req.URL.Path, "/mcp/")
}

func (p *SidecarProxy) sendErrorResponse(conn net.Conn, statusCode int, message string) {
    resp := &http.Response{
        StatusCode: statusCode,
        ProtoMajor: 1,
        ProtoMinor: 1,
        Header: http.Header{
            "Content-Type": []string{"application/json"},
        },
        Body: io.NopCloser(strings.NewReader(
            fmt.Sprintf(`{"error":"%s"}`, message))),
    }
    resp.Write(conn)
}

func (p *SidecarProxy) sendOAuthRequiredResponse(conn net.Conn, authURL, codeVerifier, mcpServerURL string) {
    resp := &http.Response{
        StatusCode: http.StatusUnauthorized,
        ProtoMajor: 1,
        ProtoMinor: 1,
        Header: http.Header{
            "Content-Type": []string{"application/json"},
        },
        Body: io.NopCloser(strings.NewReader(fmt.Sprintf(
            `{"error":"authentication_required","auth_url":"%s","code_verifier":"%s","mcp_server_url":"%s"}`,
            authURL, codeVerifier, mcpServerURL))),
    }
    resp.Write(conn)
}
```

**Key Points:**
- **NO HTTP server** - Only TCP listener on port 15001
- **NO endpoints** - All traffic intercepted via iptables
- OAuth code comes from frontend in query parameters
- Sidecar extracts code, exchanges for token, removes from URL
- Completely transparent to AI Agent

#### 1.2 Update AI Agent

**File:** `cmd/aiagent/main.go`

**Key Changes:**
- Remove explicit AuthBridge URL configuration
- Make direct HTTP requests to MCP server
- Sidecar intercepts and handles auth transparently

```go
// AI Agent now makes direct requests to MCP server
// AuthBridge sidecar intercepts and adds authentication

func (agent *AIAgent) makeMCPRequest(mcpReq MCPRequest) (*MCPResponse, error) {
    // Build request to MCP server directly
    reqURL := fmt.Sprintf("%s/mcp/%s", agent.mcpServerURL, mcpReq.Method)
    
    // Add user context header (for sidecar)
    req, err := http.NewRequest("POST", reqURL, bytes.NewReader(reqBody))
    req.Header.Set("X-User-ID", mcpReq.UserID)
    req.Header.Set("Content-Type", "application/json")
    
    // Sidecar will intercept and add Authorization header
    resp, err := http.DefaultClient.Do(req)
    // ... handle response
}
```

### Phase 2: Kubernetes Deployment

#### 2.1 Init Container for iptables

**Purpose:** Set up traffic interception before main containers start

```yaml
initContainers:
- name: init-iptables
  image: alpine:latest
  securityContext:
    capabilities:
      add:
      - NET_ADMIN
    privileged: true
  command:
  - sh
  - -c
  - |
    # Install iptables
    apk add --no-cache iptables
    
    # Redirect all outbound traffic to sidecar proxy
    iptables -t nat -A OUTPUT -p tcp -j REDIRECT --to-port 15001
    
    # Redirect all inbound traffic to sidecar proxy
    iptables -t nat -A PREROUTING -p tcp -j REDIRECT --to-port 15001
    
    # Exclude sidecar's own traffic (UID 1337)
    iptables -t nat -A OUTPUT -m owner --uid-owner 1337 -j RETURN
    
    echo "iptables rules configured"
```

#### 2.2 Pod Deployment

**File:** `k8s/aiagent-deployment.yaml`

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: aiagent
  namespace: kagenti
spec:
  replicas: 3
  selector:
    matchLabels:
      app: aiagent
  template:
    metadata:
      labels:
        app: aiagent
    spec:
      # Init container to set up iptables
      initContainers:
      - name: init-iptables
        image: alpine:latest
        securityContext:
          capabilities:
            add:
            - NET_ADMIN
          privileged: true
        command:
        - sh
        - -c
        - |
          apk add --no-cache iptables
          iptables -t nat -A OUTPUT -p tcp -j REDIRECT --to-port 15001
          iptables -t nat -A PREROUTING -p tcp -j REDIRECT --to-port 15001
          iptables -t nat -A OUTPUT -m owner --uid-owner 1337 -j RETURN
          echo "Traffic interception configured"
      
      containers:
      # Main container: AI Agent
      - name: aiagent
        image: ghcr.io/kagenti/aiagent:latest
        ports:
        - name: http
          containerPort: 8186
        env:
        - name: MCP_SERVER_URL
          value: "http://mcp-server-service:8184"
        volumeMounts:
        - name: logs
          mountPath: /tmp
        resources:
          requests:
            memory: "256Mi"
            cpu: "250m"
          limits:
            memory: "512Mi"
            cpu: "500m"
      
      # Sidecar: AuthBridge Proxy
      - name: authbridge-sidecar
        image: ghcr.io/kagenti/authbridge-sidecar:latest
        ports:
        - name: proxy
          containerPort: 15001
        env:
        - name: REDIRECT_URI
          value: "http://backend-service:8187/callback"
        - name: LOG_LEVEL
          value: "info"
        securityContext:
          runAsUser: 1337  # Special UID for iptables exclusion
        volumeMounts:
        - name: logs
          mountPath: /tmp
        resources:
          requests:
            memory: "128Mi"
            cpu: "100m"
          limits:
            memory: "256Mi"
            cpu: "200m"
      
      volumes:
      - name: logs
        emptyDir: {}
      
      # Required for iptables
      securityContext:
        fsGroup: 1337
```

#### 2.3 Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: aiagent-service
  namespace: kagenti
spec:
  type: ClusterIP
  ports:
  - name: http
    port: 8186
    targetPort: 8186
  selector:
    app: aiagent
```

### Phase 3: Dockerfiles

#### 3.1 AI Agent Dockerfile

**File:** `Dockerfile.aiagent`

```dockerfile
FROM golang:1.25.8-alpine AS build
ARG VERSION="dev"

WORKDIR /build
RUN apk add --no-cache git
COPY . .

RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /bin/aiagent \
    ./cmd/aiagent

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /bin/aiagent .
EXPOSE 8186
USER nonroot:nonroot
ENTRYPOINT ["/app/aiagent"]
```

#### 3.2 AuthBridge Sidecar Dockerfile

**File:** `Dockerfile.authbridge-sidecar`

```dockerfile
FROM golang:1.25.8-alpine AS build
ARG VERSION="dev"

WORKDIR /build
RUN apk add --no-cache git
COPY . .

RUN CGO_ENABLED=0 go build \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /bin/authbridge-sidecar \
    ./cmd/authbridge-sidecar

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /bin/authbridge-sidecar .
EXPOSE 15001
USER 1337:1337
ENTRYPOINT ["/app/authbridge-sidecar"]
```

### Phase 4: Testing

#### 4.1 Local Testing with Docker

**File:** `docker-compose.sidecar.yaml`

```yaml
version: '3.8'

services:
  aiagent-pod:
    build:
      context: .
      dockerfile: Dockerfile.aiagent
    cap_add:
      - NET_ADMIN
    privileged: true
    ports:
      - "8186:8186"
    environment:
      - MCP_SERVER_URL=http://mcp-server:8184
    networks:
      - kagenti-net
    command:
      - sh
      - -c
      - |
        # Set up iptables
        iptables -t nat -A OUTPUT -p tcp -j REDIRECT --to-port 15001
        iptables -t nat -A OUTPUT -m owner --uid-owner 1337 -j RETURN
        # Start AI Agent
        /app/aiagent &
        # Start AuthBridge sidecar
        /app/authbridge-sidecar
  
  mcp-server:
    build:
      context: .
      dockerfile: Dockerfile
    ports:
      - "8184:8082"
    networks:
      - kagenti-net

networks:
  kagenti-net:
    driver: bridge
```

## Benefits Summary

### 1. **True Sidecar Pattern**
- AuthBridge has no exposed endpoints
- Transparent to AI Agent
- Standard Kubernetes pattern

### 2. **Simplified Architecture**
- AI Agent code simpler (no AuthBridge awareness)
- Single traffic control point
- Easier to reason about

### 3. **Enhanced Security**
- All traffic flows through proxy
- Impossible to bypass authentication
- Centralized token management

### 4. **Better Performance**
- No additional HTTP calls
- Shared network namespace
- Minimal overhead

### 5. **Operational Excellence**
- Standard sidecar deployment
- Easy to monitor (all traffic visible)
- Simple scaling model

## Migration Path

1. **Develop sidecar proxy** (Week 1)
2. **Test locally with Docker** (Week 1-2)
3. **Deploy to test cluster** (Week 2)
4. **Gradual rollout** (Week 3)
5. **Full migration** (Week 4)

## Success Criteria

- [ ] All traffic flows through sidecar
- [ ] AI Agent unaware of proxy
- [ ] OAuth flow works transparently
- [ ] Token caching per pod
- [ ] Performance improved
- [ ] No exposed AuthBridge endpoints

---

**Document Version:** 2.0  
**Date:** 2026-04-11  
**Author:** Bob (AI Assistant)  
**Branch:** authbridge_sidecar  
**Pattern:** True Sidecar Proxy