# AuthBridge: Adding MCP Elicitation URL Mode Support

## Context

This document describes the changes needed to the [kagenti-extensions AuthBridge](../kagenti-extensions/) to support a new outbound route type: **MCP Elicitation URL Mode**. This is in addition to the existing RFC 8693 token exchange mode.

The high-level design is defined in [envoy_sidecar.md](envoy_sidecar.md). This document maps that design onto the AuthBridge codebase and specifies what to change.

## What Changes and What Stays the Same

### Stays the Same

- **Inbound path**: no changes. JWT validation works as before.
- **Outbound path for RFC 8693 routes**: no changes. Existing `exchange` and `passthrough` route actions continue to work.
- **Envoy listeners, iptables, ext_proc protocol**: no changes. AuthBridge still runs as an ext_proc gRPC server on :9090 inside the Envoy sidecar.
- **authlib structure**: `validation/`, `cache/`, `bypass/`, `spiffe/`, `config/` — no changes.
- **Deployment modes**: all three modes (envoy-sidecar, waypoint, proxy-sidecar) can support the new route action, though the POC targets envoy-sidecar.

### Changes

| Area | Change |
|------|--------|
| **Route config** | New route action `"mcp_elicitation"` alongside `"exchange"` and `"passthrough"` |
| **routing package** | `Route` struct gets a `TokenBrokerURL` field; `ResolvedRoute` gains `MCPElicitation bool` and `TokenBrokerURL` |
| **auth/auth.go** | `HandleOutbound` gains a new branch for MCP elicitation routes that calls the Token Broker instead of the exchange client |
| **New: tokenbroker client** | A new `authlib/tokenbroker/` package — HTTP client for the Token Broker's `POST /sessions/{key}/token` endpoint |
| **auth/result.go** | No change — `ActionReplaceToken` is reused (the token comes from the Token Broker instead of Keycloak) |
| **ext_proc listener** | Must extract and forward `X-OAuth-Session-Key` and `X-User-ID` headers from the original request |

## 1. Route Configuration

### Current format (`routes.yaml`)

```yaml
- host: "github-tool-mcp"
  target_audience: "github-tool"
  token_scopes: "openid github-tool-aud github-full-access"
  action: "exchange"       # default if omitted
```

### New format: adding `mcp_elicitation` action

```yaml
# Existing RFC 8693 route — unchanged
- host: "keycloak-protected-service"
  target_audience: "my-service"
  token_scopes: "openid my-service-aud"
  action: "exchange"

# New: MCP Elicitation URL Mode route
- host: "mcp-server-service"
  action: "mcp_elicitation"
  token_broker_url: "http://token-broker-service:8190"
```

For `mcp_elicitation` routes:
- `target_audience` and `token_scopes` are **not used** (the Token Broker handles OAuth discovery and scoping).
- `token_broker_url` is **required** — the base URL of the Token Broker service.
- `token_url` (per-route token endpoint override) is **not used**.

## 2. Routing Package Changes

### `routing/router.go` — Route struct

```go
type Route struct {
    Host           string `yaml:"host"`
    Audience       string `yaml:"target_audience,omitempty"`
    Scopes         string `yaml:"token_scopes,omitempty"`
    TokenEndpoint  string `yaml:"token_url,omitempty"`
    Action         string `yaml:"action,omitempty"`           // "exchange", "passthrough", or "mcp_elicitation"
    TokenBrokerURL string `yaml:"token_broker_url,omitempty"` // required when action == "mcp_elicitation"
}
```

### `routing/router.go` — ResolvedRoute struct

```go
type ResolvedRoute struct {
    Matched        bool
    Audience       string
    Scopes         string
    TokenEndpoint  string
    Passthrough    bool
    MCPElicitation bool   // true when action == "mcp_elicitation"
    TokenBrokerURL string // Token Broker base URL for MCP elicitation
}
```

### `routing/router.go` — Resolve method

Add handling for the new action in the match branch:

```go
case "mcp_elicitation":
    return &ResolvedRoute{
        Matched:        true,
        MCPElicitation: true,
        TokenBrokerURL: entry.route.TokenBrokerURL,
    }
```

## 3. Token Broker Client

New package: `authlib/tokenbroker/`

This is a thin HTTP client that calls `POST /sessions/{key}/token` on the Token Broker. It blocks until a token is available (the Token Broker performs long-poll internally) or returns an error.

### Interface

```go
package tokenbroker

type Client struct {
    httpClient *http.Client
}

func NewClient() *Client

// AcquireToken calls the Token Broker to get a token for the given session, user, and MCP server.
// Blocks until a token is available or the context is cancelled.
func (c *Client) AcquireToken(ctx context.Context, tokenBrokerURL, sessionKey, userID, mcpServerURL string) (string, error)
```

### Implementation

`AcquireToken` sends:

```
POST {tokenBrokerURL}/sessions/{sessionKey}/token
Headers:
  X-User-ID: {userID}
  X-Mcp-Server-Url: {mcpServerURL}
```

On success (200), the Token Broker returns `{"token": "gho_xxxx"}`. The client extracts and returns the token.

On failure, the Token Broker returns an error JSON:
- 401: session expired or user mismatch
- 408: OAuth flow timed out
- 503: internal error

The client translates these into Go errors.

**Timeout**: The HTTP client timeout should be **310 seconds** (longer than the Token Broker's 300s internal timeout) to avoid premature disconnection.

### Why not reuse `exchange.Client`?

The existing `exchange.Client` implements RFC 8693 token exchange against a standard OAuth token endpoint. The Token Broker has a completely different API (session-based, long-poll, custom endpoints). A separate client is cleaner than overloading the exchange client.

## 4. HandleOutbound Changes

### Current flow (simplified)

```
HandleOutbound(ctx, authHeader, host):
  1. Resolve route
  2. If no route or passthrough → allow
  3. Extract bearer token
  4. Cache check
  5. Token exchange (RFC 8693)
  6. Cache result
  7. Return ActionReplaceToken
```

### New flow: add MCP elicitation branch

```
HandleOutbound(ctx, authHeader, host, headers):
  1. Resolve route
  2. If no route or passthrough → allow
  3. If MCPElicitation → handleMCPElicitation(ctx, headers, resolved)
  4. (existing) Extract bearer, cache check, token exchange, cache, return
```

The MCP elicitation branch:

```go
func (a *Auth) handleMCPElicitation(ctx context.Context, headers map[string]string, resolved *routing.ResolvedRoute) *OutboundResult {
    sessionKey := headers["x-oauth-session-key"]
    userID := headers["x-user-id"]
    mcpServerURL := headers["x-mcp-server-url"]

    if sessionKey == "" || userID == "" {
        return &OutboundResult{
            Action:     ActionDeny,
            DenyStatus: http.StatusForbidden,
            DenyReason: "missing oauth session headers",
        }
    }

    // mcpServerURL can be derived from host if not in headers
    if mcpServerURL == "" {
        mcpServerURL = "http://" + resolved.Host
    }

    token, err := a.tokenBrokerClient.AcquireToken(ctx, resolved.TokenBrokerURL, sessionKey, userID, mcpServerURL)
    if err != nil {
        return &OutboundResult{
            Action:     ActionDeny,
            DenyStatus: http.StatusForbidden,
            DenyReason: "Failed to obtain user authorization for this MCP server.",
        }
    }

    return &OutboundResult{Action: ActionReplaceToken, Token: token}
}
```

**Key difference from RFC 8693 path**: no caller token exchange. The Token Broker manages the full OAuth lifecycle (discovery, PKCE, user authorization, token exchange). AuthBridge just asks for a token and injects it.

**Error response**: On failure, AuthBridge returns a JSON-RPC error to the Agent:
```json
{
  "jsonrpc": "2.0",
  "id": null,
  "error": {
    "code": -32001,
    "message": "Failed to obtain user authorization for this MCP server."
  }
}
```

### Header access in HandleOutbound

Currently, `HandleOutbound(ctx, authHeader, host)` only receives the Authorization header and Host. For MCP elicitation, it also needs `X-OAuth-Session-Key` and `X-User-ID`.

Two options:

**Option A**: Add a `headers map[string]string` parameter to `HandleOutbound`. This changes the interface for all callers (ext_proc, ext_authz, forward/reverse proxy listeners).

**Option B**: Pass extra headers via context. The ext_proc listener already has access to all request headers — it can inject them into the context before calling `HandleOutbound`.

**Recommended: Option B** — it avoids changing the `HandleOutbound` signature and only requires changes in the ext_proc listener to populate the context.

```go
// In authlib/auth/context.go (new file)
type contextKey string
const headersKey contextKey = "extra-headers"

func ContextWithHeaders(ctx context.Context, h map[string]string) context.Context
func HeadersFromContext(ctx context.Context) map[string]string
```

The ext_proc listener extracts `x-oauth-session-key` and `x-user-id` from the request headers and puts them in the context. `handleMCPElicitation` reads them from the context.

## 5. ext_proc Listener Changes

The ext_proc listener (`cmd/authbridge/listener/extproc/`) receives all request headers from Envoy. Currently it extracts:
- `authorization` header → passed to `HandleOutbound` as `authHeader`
- `:authority` (host) → passed to `HandleOutbound` as `host`
- `x-authbridge-direction` → determines inbound vs outbound

For MCP elicitation support, the ext_proc listener must additionally extract:
- `x-oauth-session-key`
- `x-user-id`

These are injected into the context via `ContextWithHeaders` before calling `HandleOutbound`.

No changes to the Envoy configuration are needed — Envoy already sends all request headers to ext_proc.

## 6. Auth Struct Changes

The `Auth` struct in `auth/auth.go` needs a new field:

```go
type Config struct {
    // ... existing fields ...
    TokenBrokerClient *tokenbroker.Client // optional, for MCP elicitation routes
}

type Auth struct {
    // ... existing fields ...
    tokenBrokerClient *tokenbroker.Client
}
```

If `TokenBrokerClient` is nil and a route resolves to `mcp_elicitation`, `HandleOutbound` returns an error (misconfiguration).

The `tokenbroker.Client` is stateless (no connection pooling beyond Go's default HTTP transport), so it can be created once at startup.

## 7. Token Broker Interaction

AuthBridge calls the Token Broker's `/sessions/{key}/token` endpoint, which **blocks** until a token is available. The full flow:

1. Agent sends MCP request to `mcp-server-service:8184`
2. Envoy intercepts (iptables), sends to ext_proc (AuthBridge)
3. AuthBridge resolves route → `mcp_elicitation`
4. AuthBridge calls `POST /sessions/{sessionKey}/token` on Token Broker with `X-User-ID` and `X-Mcp-Server-Url`
5. Token Broker checks cache → miss → acquires semaphore → starts OAuth flow
6. Token Broker sends `oauth_required` event to Backend (via long-poll)
7. User completes OAuth in browser
8. Backend sends authorization code back to Token Broker
9. Token Broker exchanges code for token, caches it, returns to AuthBridge
10. AuthBridge injects `Authorization: Bearer {token}` into the MCP request
11. Envoy forwards the authenticated request to the MCP server

On subsequent requests for the same user + MCP server, step 5 returns immediately from cache.

### Why AuthBridge calls /sessions/{key}/token and not /ext_authz

The POC implementation in this repository uses a dedicated `/ext_authz` endpoint because the demo's Envoy uses HTTP ext_authz (not ext_proc). In the real AuthBridge, ext_proc gives us full header access and programmatic control — we call the Token Broker's standard `/sessions/{key}/token` endpoint directly. No need for the `/ext_authz` adapter.

## 8. What the Agent Needs to Do

The Agent must propagate the `X-OAuth-Session-Key` header from incoming requests to its outbound MCP requests. This is the only change to the Agent. The Agent never sees tokens, 401s, or OAuth URLs.

The Backend sets `X-OAuth-Session-Key` when forwarding tasks to the Agent. If an Agent calls another Agent, it forwards the same header.

## 9. Configuration Example

### routes.yaml

```yaml
# RFC 8693 token exchange route (existing pattern)
- host: "keycloak-protected-service"
  target_audience: "my-service"
  token_scopes: "openid my-service-aud"

# MCP Elicitation URL Mode route (new)
- host: "mcp-server-service"
  action: "mcp_elicitation"
  token_broker_url: "http://token-broker-service:8190"

# Another MCP server with a different Token Broker
- host: "other-mcp-*"
  action: "mcp_elicitation"
  token_broker_url: "http://other-token-broker:8190"
```

### K8s ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: authproxy-routes
data:
  routes.yaml: |
    - host: "mcp-server-service"
      action: "mcp_elicitation"
      token_broker_url: "http://token-broker-service:8190"
```

## 10. Summary of Code Changes

| File/Package | Change |
|---|---|
| `authlib/routing/router.go` | Add `TokenBrokerURL` to `Route`, `MCPElicitation` + `TokenBrokerURL` to `ResolvedRoute`, handle `"mcp_elicitation"` action in `Resolve` |
| `authlib/tokenbroker/` (new) | HTTP client for Token Broker `/sessions/{key}/token` |
| `authlib/auth/auth.go` | Add `tokenBrokerClient` field, add `handleMCPElicitation` method, add MCP elicitation branch in `HandleOutbound` |
| `authlib/auth/context.go` (new) | Context helpers for passing extra headers (`x-oauth-session-key`, `x-user-id`) |
| `authlib/auth/result.go` | No change (reuse `ActionReplaceToken`) |
| `cmd/authbridge/listener/extproc/` | Extract `x-oauth-session-key` and `x-user-id` from request headers, inject into context |
| `authlib/config/` | Wire `tokenbroker.Client` creation into config resolution |

## 11. Token Broker Design

The Token Broker is a shared Kubernetes service. Its design, API, session lifecycle, and OAuth flow are fully described in [envoy_sidecar.md](envoy_sidecar.md).

Key endpoints used by AuthBridge:

| Endpoint | Caller | Purpose |
|---|---|---|
| `POST /sessions/{key}/token` | AuthBridge | Get token for user + MCP server (blocks until available) |

Key endpoints used by Backend:

| Endpoint | Caller | Purpose |
|---|---|---|
| `POST /sessions` | Backend | Create session, get `oauth_session_key` |
| `POST /sessions/{key}/events` | Backend | Long-poll for events / send OAuth completion |
| `POST /sessions/{key}/end` | Backend | End session |

## 12. PKCE Flow Ownership

The Token Broker does **not** generate PKCE. The MCP server generates PKCE:

1. Token Broker calls `POST /auth/url` on the **MCP server** with `{"callback_url": "..."}`
2. MCP server generates `code_verifier` + `code_challenge`, builds auth URL, returns `{url, code_verifier}`
3. Token Broker stores `code_verifier`, sends `auth_url` to Backend via events
4. After user completes OAuth, Token Broker calls `POST /oauth/exchange-token` on the **MCP server** with `{code, code_verifier}`
5. MCP server uses its `client_secret` to exchange with the OAuth provider

The `code_verifier` never leaves the Token Broker + MCP server boundary. The Backend only sees `auth_url`, `code`, and `state`.

## 13. Differences from the POC in This Repository

This repository contains a working POC (`cmd/token-broker/`, `cmd/aiagent/`, `cmd/backend-envoy/`, `k8s/envoy-demo/`) that uses Envoy HTTP ext_authz for token injection. The production AuthBridge integration differs:

| Aspect | POC | AuthBridge Integration |
|---|---|---|
| Token injection | Envoy ext_authz (HTTP) → `/ext_authz` endpoint | ext_proc (gRPC) → `HandleOutbound` → Token Broker client |
| Header forwarding | ext_authz `allowed_headers` + `headers_to_add` | ext_proc has full header access natively |
| MCP server URL | Injected by Envoy route config or ext_authz `headers_to_add` | Derived from route config (`host` field) or request Host header |
| Agent changes | Must propagate `X-OAuth-Session-Key` header | Same — must propagate `X-OAuth-Session-Key` header |
| Token Broker API | Same `/sessions/{key}/token` endpoint | Same |
| Error format | MCP JSON-RPC `{"jsonrpc":"2.0","error":{"code":-32001,...}}` | Same (AuthBridge formats the error body) |
