# Kagenti Envoy + AuthBridge Demo

This directory contains the production-ready implementation of the Kagenti MCP OAuth system using **Envoy Proxy + AuthBridge (ext_proc)**, implementing the design described in `docs/envoy_sidecar_claude.md`.

## Overview

This demo uses the **AuthBridge** sidecar from `kagenti-extensions` with Envoy's ext_proc protocol for transparent traffic interception and OAuth token injection.

### Architecture

| Component | Implementation |
|-----------|----------------|
| **Sidecar** | Envoy Proxy + AuthBridge (ext_proc) |
| **Token Management** | Centralized Token Broker service |
| **Token Injection** | AuthBridge → Token Broker REST API |
| **Session Management** | REST API with long-polling |
| **Architecture** | Production-ready, Kagenti-compatible |

## Architecture Diagram

```
┌─────────────┐         ┌──────────────┐         ┌─────────────┐
│   Backend   │◄────────┤ Token Broker ├────────►│ MCP Server  │
│  (Phase 3)  │  Events │   Service    │Discovery│             │
└──────┬──────┘         └──────┬───────┘         └─────────────┘
       │                       │
       │ X-OAuth-Session-Key   │ Token Cache
       │                       │ Session Store
       ▼                       ▼
┌──────────────────────────────────────────────┐
│              AI Agent Pod                    │
│  ┌─────────┐  ┌───────────┐  ┌───────────┐ │
│  │  Envoy  │  │AuthBridge │  │ AI Agent  │ │
│  │ Sidecar │  │  Sidecar  │  │ Container │ │
│  │         │  │           │  │           │ │
│  │Inbound: │  │ ext_proc  │  │Listens on │ │
│  │ 15124   │◄─┤  gRPC     │  │127.0.0.1: │ │
│  │         │  │  :9090    │  │  8186     │ │
│  │Outbound:│  │           │  │           │ │
│  │ 15123   │◄─┤ Calls     │  │           │ │
│  │         │  │ Token     │  │           │ │
│  │         │  │ Broker    │  │           │ │
│  └─────────┘  └───────────┘  └───────────┘ │
└──────────────────────────────────────────────┘
```

**Traffic Flow:**
1. Envoy intercepts outbound traffic via iptables
2. Envoy calls AuthBridge via ext_proc (gRPC)
3. AuthBridge calls Token Broker REST API for token
4. AuthBridge returns token to Envoy
5. Envoy injects token and forwards to MCP server

## Components

### 1. Token Broker (`01-token-broker.yaml`)
- **Purpose**: Centralized OAuth session and token management
- **Port**: 8190
- **Endpoints**:
  - `POST /sessions` - Create session
  - `POST /sessions/{key}/token` - Get token (blocking)
  - `POST /sessions/{key}/events` - Long-poll for events / OAuth completion
  - `POST /sessions/{key}/end` - End session
  - `POST /ext_authz` - Envoy ext_authz compatibility
  - `GET /health` - Health check

### 2. MCP Server (`02-mcp-server.yaml`)
- **Purpose**: GitHub MCP server requiring OAuth
- **Container Port**: 8082
- **Service Port**: 8184
- **NodePort**: 30184
- **Endpoints**:
  - `POST /mcp` - MCP JSON-RPC (requires OAuth token)
  - `GET /.well-known/oauth-protected-resource` - OAuth discovery
  - `POST /auth/url` - Get authorization URL (generates PKCE)
  - `POST /oauth/exchange-token` - Exchange code for token

### 3. Envoy Configuration (`03-envoy-config.yaml`)
- **Purpose**: ConfigMap containing Envoy bootstrap YAML
- **Listeners**:
  - Inbound (15124): Backend → Agent (ext_proc for JWT validation)
  - Outbound (15123): Agent → MCP Server (ext_proc for token exchange)
- **Clusters**:
  - `authbridge_cluster`: AuthBridge ext_proc (127.0.0.1:9090)
  - `token_broker`: Token Broker service
  - `mcp_server_1`: MCP server
  - `original_dst`: Passthrough for non-MCP traffic

### 4. AI Agent with Envoy + AuthBridge Sidecars (`04-aiagent.yaml`)
- **Purpose**: AI Agent with Envoy and AuthBridge sidecars
- **Containers**:
  - `envoy`: Envoy Proxy (UID 1337)
  - `authbridge`: AuthBridge ext_proc server (UID 1337)
  - `aiagent`: AI Agent application
- **Init Container**: Sets up iptables for traffic interception
- **Service Port**: 8186 (maps to Envoy inbound 15124)
- **NodePort**: 30186
- **ConfigMaps**:
  - `authbridge-config`: AuthBridge routes with `broker` action
  - `aiagent-config`: AI Agent configuration

### 5. Backend (`05-backend.yaml`)
- **Purpose**: User-facing application (Phase 3 implementation)
- **Port**: 8187
- **NodePort**: 30187
- **Status**: Placeholder for Phase 3

## Implementation Phases

### ✅ Phase 1: Token Broker (Complete)
- Standalone Token Broker service
- Session management with timeout
- Token caching with JWT expiry parsing
- OAuth discovery and exchange
- REST API for token acquisition

### ✅ Phase 2: AuthBridge Integration (Complete)
- AuthBridge ext_proc implementation
- Broker route action support (Token Broker integration)
- Envoy bootstrap configuration
- Kubernetes manifests with AuthBridge
- iptables setup for transparent proxying
- **Status**: Implementation complete, ready for testing

### ⏳ Phase 3: Backend Implementation (Next)
- Session creation with Token Broker
- Long-polling for OAuth events
- OAuth redirect handling
- Request forwarding to Agent with session key

### ⏳ Phase 4: End-to-End Integration
- Complete OAuth flow testing
- Token injection verification
- Error handling validation

### ⏳ Phase 5: Production Hardening
- Metrics and observability
- Redis-backed token cache
- Token refresh support
- Security hardening

## Deployment

### Prerequisites

1. **Kubernetes cluster** (kind, minikube, or cloud)
2. **GitHub OAuth App** configured with:
   - Client ID
   - Client Secret
   - Redirect URI: `http://localhost:8187/oauth/callback`

### Step 1: Update OAuth Credentials

Edit `02-mcp-server.yaml` and set your GitHub OAuth credentials:

```yaml
env:
  - name: OAUTH_CLIENT_ID
    value: "Ov23liXXXXXXXXXXXXXX"  # Your GitHub OAuth App Client ID
  - name: OAUTH_CLIENT_SECRET
    value: "your-client-secret-here"  # Your GitHub OAuth App Client Secret
```

### Step 2: Deploy All Components

```bash
# Create namespace
kubectl apply -f 00-namespace.yaml

# Deploy Token Broker
kubectl apply -f 01-token-broker.yaml

# Deploy MCP Server
kubectl apply -f 02-mcp-server.yaml

# Deploy Envoy ConfigMap
kubectl apply -f 03-envoy-config.yaml

# Deploy AI Agent with Envoy sidecar
kubectl apply -f 04-aiagent.yaml

# Deploy Backend (placeholder for Phase 3)
kubectl apply -f 05-backend.yaml
```

### Step 3: Verify Deployment

```bash
# Check all pods are running
kubectl get pods -n kagenti-envoy-demo

# Check services
kubectl get svc -n kagenti-envoy-demo

# Check Token Broker logs
kubectl logs -n kagenti-envoy-demo -l app=token-broker -f

# Check Envoy logs
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c envoy -f

# Check AI Agent logs
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c aiagent -f
```

## Testing Phase 2 (Transparent Proxy)

Phase 2 focuses on verifying Envoy works as a transparent proxy **without OAuth** (ext_authz disabled for testing).

### Test 1: Token Broker Health Check

```bash
# Port-forward Token Broker
kubectl port-forward -n kagenti-envoy-demo svc/token-broker-service 8190:8190

# In another terminal, test health endpoint
curl http://localhost:8190/health
# Expected: OK
```

### Test 2: Create Session

```bash
# Create a session
curl -X POST http://localhost:8190/sessions \
  -H "X-User-ID: test-user" \
  -v

# Expected response:
# {"oauth_session_key":"550e8400-e29b-41d4-a716-446655440000"}
```

### Test 3: AuthBridge Health Check

```bash
# Check AuthBridge logs
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c authbridge -f

# AuthBridge should show:
# - "authbridge starting" with mode=envoy-sidecar
# - Routes loaded with broker action
# - ext_proc gRPC listening on 0.0.0.0:9090
```

### Test 4: Envoy Admin Interface

```bash
# Port-forward to Envoy admin
kubectl port-forward -n kagenti-envoy-demo -l app=aiagent 9901:9901

# Check Envoy stats
curl http://localhost:9901/stats | grep authbridge

# Check Envoy config
curl http://localhost:9901/config_dump | grep authbridge_cluster
```

### Test 5: Transparent Proxy (Inbound)

```bash
# Port-forward to AI Agent via Envoy
kubectl port-forward -n kagenti-envoy-demo svc/aiagent-service 8186:8186

# Test inbound traffic (Backend → Envoy → Agent)
curl http://localhost:8186/health
# Expected: Response from AI Agent
```

## Traffic Flow

### Inbound (Backend → Agent)
```
Backend → aiagent-service:8186 → Envoy:15124 → ext_proc (AuthBridge) → Agent:8186
```

### Outbound (Agent → MCP Server)
```
Agent → iptables → Envoy:15123 → ext_proc (AuthBridge) → Token Broker → MCP Server:8184
```

**Key Points:**
- AuthBridge handles both inbound JWT validation and outbound token exchange
- AuthBridge calls Token Broker's `/sessions/{key}/token` REST API
- Token Broker performs OAuth discovery and token caching
- Envoy uses ext_proc (not ext_authz) for full header access

## Troubleshooting

### Pod Not Starting

```bash
# Check pod status
kubectl describe pod -n kagenti-envoy-demo -l app=aiagent

# Check init container logs (iptables setup)
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c iptables-init

# Check Envoy logs
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c envoy
```

### AuthBridge Not Starting

```bash
# Check AuthBridge logs
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c authbridge

# Common issues:
# - Missing routes configuration
# - Invalid route action
# - Token Broker URL not reachable
```

### Envoy Configuration Errors

```bash
# Validate Envoy config
kubectl exec -n kagenti-envoy-demo -l app=aiagent -c envoy -- \
  /usr/local/bin/envoy --mode validate -c /etc/envoy/envoy.yaml

# Check ext_proc connection
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c envoy | grep ext_proc
```

### iptables Rules

```bash
# Check iptables rules in the pod
kubectl exec -n kagenti-envoy-demo -l app=aiagent -c envoy -- \
  iptables -t nat -L -n -v
```

### Token Broker Not Reachable

```bash
# Test Token Broker from within the cluster
kubectl run -n kagenti-envoy-demo test-pod --rm -it --image=curlimages/curl -- \
  curl http://token-broker-service:8190/health
```

## Configuration Files

- `00-namespace.yaml` - Namespace definition
- `01-token-broker.yaml` - Token Broker deployment + service
- `02-mcp-server.yaml` - MCP server deployment + service
- `03-envoy-config.yaml` - Envoy ConfigMap (ext_proc configuration)
- `04-aiagent.yaml` - AI Agent + Envoy + AuthBridge sidecars
- `05-backend.yaml` - Backend placeholder

## Next Steps

1. **Build AuthBridge Image**: Build and push AuthBridge container image
2. **Complete Phase 2 Testing**: Verify Envoy + AuthBridge transparent proxy
3. **Implement Phase 3**: Backend with session management and OAuth handling
4. **End-to-End Testing**: Full OAuth flow with token injection
5. **Production Hardening**: Metrics, persistence, security

## Key Features

| Feature | Implementation |
|---------|----------------|
| **Sidecar Type** | Envoy Proxy + AuthBridge (ext_proc) |
| **Token Storage** | Centralized (Token Broker) |
| **Token Injection** | AuthBridge → Token Broker REST API |
| **Session Management** | REST + long-polling |
| **OAuth Flow** | Delegated to Token Broker |
| **Route Configuration** | AuthBridge routes.yaml with `broker` action |
| **Scalability** | Horizontal (with Redis) |
| **Production Ready** | Yes (AuthBridge from kagenti-extensions) |

## References

- **Design**: `docs/envoy_sidecar_claude.md`
- **Implementation Plan**: `docs/authbridge_changes_plan.md`
- **Token Broker**: `cmd/token-broker/README.md`
- **AuthBridge**: `kagenti-extensions/authbridge/`
- **Broker Route Action**: `kagenti-extensions/authbridge/docs/broker-route.md`