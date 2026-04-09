# KAgentI Architecture Implementation

This repository now includes a **KAgentI-aligned architecture** that demonstrates how OAuth should work in a multi-tier system where an AI Agent Backend coordinates OAuth flows between browsers and MCP servers.

## 🏗️ Architecture Overview

### Original Architecture (oauth-demo.html)
```
Browser → MCP Server (port 8184)
          ├── OAuth Discovery
          ├── Token Exchange ✅
          └── MCP Protocol
```

### KAgentI Architecture (oauth-demo-kagenti.html)
```
Browser → OAuth Coordinator (port 8185) → MCP Server (port 8184)
          ├── OAuth Discovery              ├── OAuth Discovery
          ├── Token Exchange ✅            ├── Token Validation
          └── Session Management           └── MCP Protocol
```

## 📊 Component Responsibilities

| Component | Port | OAuth Discovery | Token Exchange | Client Secret | MCP Protocol |
|-----------|------|----------------|----------------|---------------|--------------|
| **Browser** | N/A | ❌ | ❌ | ❌ | ❌ |
| **OAuth Coordinator** | 8185 | ✅ Calls MCP | ✅ **Does This** | ✅ Stores | ✅ Proxies |
| **MCP Server** | 8184 | ✅ Provides | ❌ Does Not | ❌ Does Not | ✅ Implements |

## 🚀 Quick Start

### 1. Build Components

```bash
# Build MCP Server
go build -o github-mcp-server ./cmd/github-mcp-server

# Build OAuth Coordinator
cd cmd/oauth-coordinator
go build -o oauth-coordinator
cd ../..
```

### 2. Configure OAuth Coordinator

Edit `cmd/oauth-coordinator/config.yaml`:

```yaml
port: 8185
redirect_uri: "http://localhost:8185/callback"
demo_page_path: "../../oauth-demo-kagenti.html"

mcp_servers:
  - name: "github"
    url: "http://localhost:8184"

oauth_providers:
  "github.com":
    client_id: "YOUR_GITHUB_CLIENT_ID"
    client_secret: "YOUR_GITHUB_CLIENT_SECRET"
```

### 3. Start Services

```bash
./start-kagenti-demo.sh
```

This will start:
1. **MCP Server** on port 8184 (OAuth discovery only)
2. **OAuth Coordinator** on port 8185 (handles token exchange)

### 4. Open Demo

Open in browser: http://localhost:8185/demo

### 5. Stop Services

```bash
./stop-kagenti-demo.sh
```

## 🔄 OAuth Flow Sequence

### Step 1: User Initiates Login

```
Browser → OAuth Coordinator
POST /auth/url
```

### Step 2: OAuth Coordinator Discovers Metadata

```
OAuth Coordinator → MCP Server
GET /.well-known/oauth-protected-resource/mcp

Response:
{
  "authorization_servers": ["https://github.com/login/oauth"],
  "scopes_supported": ["repo", "user", ...],
  "issuer": "github.com"
}
```

### Step 3: OAuth Coordinator Generates Auth URL

```
OAuth Coordinator:
1. Generates PKCE (code_verifier, code_challenge)
2. Generates state (CSRF protection)
3. Looks up credentials for "github.com"
4. Builds authorization URL
5. Stores session (code_verifier, state)
6. Returns URL to browser
```

### Step 4: User Authorizes on GitHub

```
Browser → GitHub OAuth
User authorizes application

GitHub → Browser
Redirect with code and state
```

### Step 5: Token Exchange (OAuth Coordinator)

```
Browser → OAuth Coordinator
POST /oauth/exchange-token
{
  "code": "authorization_code",
  "code_verifier": "from_session"
}

OAuth Coordinator:
1. Validates session
2. Discovers token endpoint from MCP server
3. Looks up client_secret for issuer
4. Exchanges code for token with GitHub
5. Returns token to browser
```

### Step 6: Using Token

```
Browser → GitHub API
Authorization: Bearer <token>

(Future: Browser → OAuth Coordinator → MCP Server → GitHub API)
```

## 🔐 Security Features

### 1. Provider-Agnostic Design

- **No hardcoded GitHub URLs** in OAuth Coordinator
- **Discovers** OAuth metadata from MCP servers
- **Supports multiple providers** (GitHub, GitLab, etc.)
- **No code changes** needed for new providers

### 2. Secure Credential Management

```
OAuth Coordinator:
├── Stores client_secret securely
├── Never exposes to browser
└── Keyed by issuer (discovered from MCP)

MCP Server:
├── NO client_secret
├── Only provides OAuth discovery
└── Validates tokens
```

### 3. PKCE (Proof Key for Code Exchange)

```
OAuth Coordinator generates:
├── code_verifier (random, 43-128 chars)
├── code_challenge (SHA256 hash)
└── Stores verifier in session

GitHub validates:
└── SHA256(code_verifier) == code_challenge
```

### 4. Session Management

```
OAuth Coordinator:
├── Per-user sessions
├── Temporary PKCE storage
├── State validation (CSRF protection)
└── Token storage (future: encrypted)
```

## 📁 File Structure

```
github-mcp-server/
├── cmd/
│   ├── oauth-coordinator/          # NEW: OAuth Coordinator
│   │   ├── main.go                 # Provider-agnostic OAuth logic
│   │   ├── config.yaml             # OAuth credentials
│   │   └── README.md               # Documentation
│   │
│   └── github-mcp-server/          # EXISTING: MCP Server
│       └── main.go                 # OAuth discovery only
│
├── oauth-demo-kagenti.html         # NEW: KAgentI demo page
├── oauth-demo.html                 # EXISTING: Original demo
├── start-kagenti-demo.sh           # NEW: Start KAgentI architecture
├── stop-kagenti-demo.sh            # NEW: Stop services
└── KAGENTI-ARCHITECTURE.md         # This file
```

## 🆚 Comparison: Original vs KAgentI

### Original Demo (oauth-demo.html)

**Pros:**
- ✅ Simple, single server
- ✅ Easy to understand
- ✅ Good for demos

**Cons:**
- ❌ MCP server handles token exchange (not MCP spec)
- ❌ MCP server stores client_secret
- ❌ Not suitable for multi-user systems
- ❌ Doesn't match KAgentI architecture

### KAgentI Architecture (oauth-demo-kagenti.html)

**Pros:**
- ✅ Separates concerns (OAuth vs MCP)
- ✅ MCP server is stateless
- ✅ OAuth Coordinator is provider-agnostic
- ✅ Supports multi-user sessions
- ✅ Matches production architecture
- ✅ MCP server can be reused by different clients

**Cons:**
- ⚠️ More complex (two servers)
- ⚠️ Requires coordination between components

## 🎯 Key Insights

### 1. OAuth Coordinator is Provider-Agnostic

The OAuth Coordinator has **ZERO GitHub-specific code**:

```go
// NO GitHub imports
// NO hardcoded GitHub URLs
// ONLY generic OAuth 2.0

// Discovers OAuth metadata from MCP server
metadata := discoverOAuthMetadata(mcpServerURL)

// Looks up credentials by issuer (discovered)
creds := credentialStore[metadata.Issuer]

// Generic token exchange
token := exchangeToken(metadata.TokenEndpoint, code, verifier, creds)
```

### 2. MCP Server Focuses on MCP Protocol

The MCP Server:
- ✅ Provides OAuth discovery
- ✅ Validates tokens
- ✅ Implements MCP protocol
- ✅ Calls GitHub API
- ❌ Does NOT exchange tokens
- ❌ Does NOT store client_secret

### 3. Matches KAgentI Production Architecture

```
Production KAgentI:
Browser → Web Server → AI Agent Backend → MCP Gateway → MCP Server

This Demo:
Browser → (Web Server) → OAuth Coordinator → (MCP Gateway) → MCP Server
                         ↑
                         Acts as AI Agent Backend
```

## 🔧 Adding New OAuth Providers

To add GitLab support:

### 1. Start GitLab MCP Server (port 8186)

```bash
./gitlab-mcp-server http --port 8186
```

### 2. Update OAuth Coordinator Config

```yaml
mcp_servers:
  - name: "github"
    url: "http://localhost:8184"
  - name: "gitlab"
    url: "http://localhost:8186"

oauth_providers:
  "github.com":
    client_id: "github_client_id"
    client_secret: "github_secret"
  "gitlab.com":
    client_id: "gitlab_client_id"
    client_secret: "gitlab_secret"
```

### 3. No Code Changes Needed!

The OAuth Coordinator will:
1. Discover OAuth metadata from GitLab MCP server
2. Look up credentials for "gitlab.com"
3. Handle OAuth flow generically

## 📝 Summary

The KAgentI architecture demonstrates:

1. **Separation of Concerns**
   - OAuth Coordinator: Handles OAuth flows
   - MCP Server: Provides MCP protocol

2. **Provider-Agnostic Design**
   - No hardcoded provider URLs
   - Discovers metadata from MCP servers
   - Works with any OAuth 2.0 provider

3. **Security Best Practices**
   - Client secrets in OAuth Coordinator only
   - PKCE for all flows
   - Session-based token management

4. **Production-Ready Architecture**
   - Matches KAgentI multi-tier design
   - Supports multiple users
   - Scalable and maintainable

## 🚀 Next Steps

1. **Test the KAgentI demo**: `./start-kagenti-demo.sh`
2. **Compare with original**: See how token exchange moved
3. **Add new providers**: Try adding GitLab or other OAuth providers
4. **Integrate with KAgentI**: Use OAuth Coordinator as AI Agent Backend

## Made with Bob