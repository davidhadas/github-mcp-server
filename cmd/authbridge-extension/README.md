# AuthBridge Extension

A provider-agnostic OAuth 2.0 coordinator with AI Agent support that manages OAuth flows for multiple MCP servers. This component acts as the "AuthBridge" layer in the KAgentI architecture, wrapping AI Agent interactions with MCP servers.

## Architecture

```
Browser → AuthBridge Extension (port 8185) → MCP Server (port 8184) → OAuth Provider
          ├── AI Agent (orchestrates tasks)
          └── Token Cache (manages authentication)
```

## Features

- **Provider-Agnostic**: Works with any OAuth 2.0 provider (GitHub, GitLab, etc.)
- **AI Agent Support**: Includes mock AI Agent component for task orchestration
- **Dynamic Discovery**: Discovers OAuth metadata from MCP servers
- **Token Caching**: Caches tokens per (user_id, mcp_server_url) for seamless retry
- **PKCE Support**: Implements RFC 7636 for enhanced security
- **Multi-Provider**: Supports multiple OAuth providers simultaneously
- **Session Management**: Manages OAuth sessions for multiple users

## Configuration

Edit `config.yaml`:

```yaml
port: 8185
redirect_uri: "http://localhost:8185/callback"
demo_page_path: "../../oauth-demo-kagenti.html"

mcp_servers:
  - name: "github"
    url: "http://localhost:8184"
```

## Building

```bash
cd cmd/authbridge-extension
go build -o authbridge-extension
```

## Running

```bash
./authbridge-extension
```

The AuthBridge Extension will:
1. Start on port 8185
2. Discover OAuth metadata from configured MCP servers
3. Handle OAuth flows for clients
4. Cache tokens for authenticated users
5. Provide AI Agent endpoints for task orchestration
6. Forward MCP requests with automatic token management

## Endpoints

### OAuth Endpoints
- `POST /auth/url` - Generate authorization URL
- `POST /oauth/exchange-token` - Exchange authorization code for token
- `GET /callback` - OAuth callback handler

### MCP Endpoints
- `POST /mcp/call` - MCP request with automatic token management

### Token Management
- `GET /tokens/status?user_id=<id>` - Get token cache status
- `DELETE /tokens/{user_id}/{mcp_server}` - Delete cached token

### AI Agent Endpoints (New)
- `POST /agent/task` - Submit task to AI agent
- `GET /agent/status?user_id=<id>` - Get agent status

### Utility
- `GET /demo` - Serve demo page (if configured)
- `GET /health` - Health check

## OAuth Flow with Token Caching

1. **Initial Request**: Agent makes MCP request without token
   ```
   POST /mcp/call
   {
     "user_id": "alice@example.com",
     "mcp_server_url": "http://localhost:8184",
     "method": "tools/call",
     "params": {...}
   }
   ```

2. **No Token**: AuthBridge returns 401 with login URL
   ```json
   {
     "error": "authentication_required",
     "login_url": "https://github.com/login/oauth/authorize?...",
     "code_verifier": "..."
   }
   ```

3. **User Authorization**: User authorizes on OAuth provider

4. **Token Exchange**: AuthBridge exchanges code for token and caches it
   ```
   POST /oauth/exchange-token
   {
     "code": "...",
     "code_verifier": "...",
     "user_id": "alice@example.com"
   }
   ```

5. **Retry**: Agent retries original request - AuthBridge uses cached token
   ```
   POST /mcp/call (same request as step 1)
   → Success! Token retrieved from cache
   ```

## AI Agent Component

The AuthBridge Extension includes a mock AI Agent component that demonstrates how KAgentI agents would interact with MCP servers:

```go
// AI Agent orchestrates tasks
agent := authBridge.GetOrCreateAgent(userID)

// Agent executes task through AuthBridge
result, err := agent.ExecuteTask("Create an issue", mcpServerURL)

// Agent makes MCP requests with automatic token management
result, err := agent.RequestMCPOperation(mcpServerURL, "tools/call", params)
```

## Security

- Client secrets stored in MCP servers (not in AuthBridge)
- PKCE (Proof Key for Code Exchange) for all flows
- State parameter for CSRF protection
- Token caching with expiration tracking
- No provider-specific code

## Adding New Providers

To add support for a new OAuth provider (e.g., GitLab):

1. Add MCP server to `config.yaml`:
   ```yaml
   mcp_servers:
     - name: "gitlab"
       url: "http://localhost:8186"
   ```

2. No code changes needed! The AuthBridge will automatically discover OAuth metadata from the MCP server.

## Differences from MCP Server

The AuthBridge Extension is separate from the MCP Server:

| Component | AuthBridge Extension | MCP Server |
|-----------|---------------------|------------|
| **Port** | 8185 | 8184 |
| **OAuth Discovery** | Calls MCP server | Provides metadata |
| **Token Exchange** | Forwards to MCP | ✅ Does this |
| **Client Secret** | ❌ Does not store | ✅ Stores |
| **Token Caching** | ✅ Caches | ❌ Stateless |
| **Session Management** | ✅ Manages | ❌ Stateless |
| **AI Agent** | ✅ Includes | ❌ Does not |
| **MCP Protocol** | Proxies | ✅ Implements |

## KAgentI Architecture

This component represents the "AuthBridge" layer in KAgentI:

```
┌─────────────┐
│  Browser    │
└──────┬──────┘
       │
       ▼
┌──────────────────────┐
│ AuthBridge Extension │ ← This component
│ ├── AI Agent         │   (Wraps agent interactions)
│ └── Token Cache      │
└──────┬───────────────┘
       │
       ▼
┌──────────────────────┐
│ MCP Server           │
│ (Has client_secret)  │
└──────────────────────┘
       │
       ▼
┌──────────────────────┐
│ OAuth Provider       │
│ (GitHub, GitLab...)  │
└──────────────────────┘
```

## Key Concepts

### Token Caching
- Tokens cached per (user_id, mcp_server_url)
- Automatic expiration tracking
- Seamless retry after authentication
- No token exposure to browser

### AI Agent Integration
- Mock agent component for demonstration
- Shows how KAgentI agents would interact
- Task orchestration through AuthBridge
- Automatic token management

### Provider-Agnostic Design
- No hardcoded OAuth provider URLs
- Discovers metadata from MCP servers
- Works with any OAuth 2.0 provider
- Easy to add new providers

## Made with Bob