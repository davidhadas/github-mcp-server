# OAuth Coordinator

A provider-agnostic OAuth 2.0 coordinator that manages OAuth flows for multiple MCP servers. This component acts as the "AI Agent Backend" in the KAgentI architecture.

## Architecture

```
Browser → OAuth Coordinator (port 8185) → MCP Server (port 8184) → OAuth Provider
```

## Features

- **Provider-Agnostic**: Works with any OAuth 2.0 provider (GitHub, GitLab, etc.)
- **Dynamic Discovery**: Discovers OAuth metadata from MCP servers
- **PKCE Support**: Implements RFC 7636 for enhanced security
- **Multi-Provider**: Supports multiple OAuth providers simultaneously
- **Session Management**: Manages OAuth sessions for multiple users

## Configuration

Edit `config.yaml`:

```yaml
port: 8185
redirect_uri: "http://localhost:8185/callback"
demo_page_path: "./oauth-demo.html"

mcp_servers:
  - name: "github"
    url: "http://localhost:8184"

oauth_providers:
  "github.com":
    client_id: "your_client_id"
    client_secret: "your_client_secret"
```

## Building

```bash
cd cmd/oauth-coordinator
go build -o oauth-coordinator
```

## Running

```bash
./oauth-coordinator
```

The coordinator will:
1. Start on port 8185
2. Discover OAuth metadata from configured MCP servers
3. Handle OAuth flows for the demo page
4. Exchange authorization codes for tokens
5. Forward MCP requests with tokens

## Endpoints

- `POST /auth/url` - Generate authorization URL
- `POST /oauth/exchange-token` - Exchange authorization code for token
- `GET /callback` - OAuth callback handler
- `GET /demo` - Serve demo page (if configured)
- `GET /health` - Health check

## OAuth Flow

1. **Discovery**: Coordinator discovers OAuth metadata from MCP server
   ```
   GET http://localhost:8184/.well-known/oauth-protected-resource/mcp
   ```

2. **Authorization**: Coordinator generates authorization URL with PKCE
   ```
   POST http://localhost:8185/auth/url
   ```

3. **User Authorization**: User authorizes on OAuth provider (GitHub, etc.)

4. **Token Exchange**: Coordinator exchanges code for token
   ```
   POST http://localhost:8185/oauth/exchange-token
   ```

5. **MCP Requests**: Coordinator forwards requests to MCP server with token

## Security

- Client secrets stored securely in configuration
- PKCE (Proof Key for Code Exchange) for all flows
- State parameter for CSRF protection
- Session-based token management
- No provider-specific code

## Adding New Providers

To add support for a new OAuth provider (e.g., GitLab):

1. Add MCP server to `config.yaml`:
   ```yaml
   mcp_servers:
     - name: "gitlab"
       url: "http://localhost:8186"
   ```

2. Add provider credentials:
   ```yaml
   oauth_providers:
     "gitlab.com":
       client_id: "your_gitlab_client_id"
       client_secret: "your_gitlab_client_secret"
   ```

3. No code changes needed! The coordinator will automatically discover OAuth metadata from the MCP server.

## Differences from MCP Server

The OAuth Coordinator is separate from the MCP Server:

| Component | OAuth Coordinator | MCP Server |
|-----------|------------------|------------|
| **Port** | 8185 | 8184 |
| **OAuth Discovery** | Calls MCP server | Provides metadata |
| **Token Exchange** | ✅ Does this | ❌ Does not |
| **Client Secret** | ✅ Stores | ❌ Does not store |
| **Session Management** | ✅ Manages | ❌ Stateless |
| **MCP Protocol** | Proxies | ✅ Implements |

## KAgentI Architecture

This component represents the "AI Agent Backend" layer in KAgentI:

```
┌─────────────┐
│  Browser    │
└──────┬──────┘
       │
       ▼
┌──────────────────────┐
│ OAuth Coordinator    │ ← This component
│ (AI Agent Backend)   │
└──────┬───────────────┘
       │
       ▼
┌──────────────────────┐
│ MCP Server           │
└──────────────────────┘
```

## Made with Bob