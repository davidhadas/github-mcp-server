# OAuth 2.0 + PKCE Login Flow - Complete Diagram

This document explains how the GitHub OAuth login works in the MCP demo, including all components and the exact order of execution.

## Architecture Overview

```
┌─────────────────┐         ┌──────────────────┐         ┌─────────────────┐
│                 │         │                  │         │                 │
│  User Browser   │◄───────►│  MCP Server      │◄───────►│  GitHub OAuth   │
│  (oauth-demo.   │         │  (localhost:8184)│         │  API            │
│   html)         │         │                  │         │                 │
└─────────────────┘         └──────────────────┘         └─────────────────┘
```

## Complete OAuth Flow - Step by Step

### Phase 1: Initialization (User Clicks "Login with GitHub")

```
┌─────────────┐
│   Browser   │
│             │
│  1. User    │
│  clicks     │
│  "Login"    │
│  button     │
└──────┬──────┘
       │
       │ JavaScript: loginWithGitHub()
       │
       ▼
┌─────────────────────────────────────────────────────────┐
│  Step 1: Request Authorization URL                      │
│  ─────────────────────────────────────────────────────  │
│  Browser → MCP Server                                   │
│  GET /auth/url                                          │
│                                                          │
│  Purpose: Get GitHub authorization URL with PKCE params │
└─────────────────────────────────────────────────────────┘
```

**Code Location**: `oauth-demo.html` - `loginWithGitHub()` function
```javascript
async function loginWithGitHub() {
    const response = await fetch('/auth/url');
    const data = await response.json();
    // Store PKCE verifier for later use
    sessionStorage.setItem('code_verifier', data.code_verifier);
    // Redirect to GitHub
    window.location.href = data.url;
}
```

### Phase 2: Server Generates Authorization URL with PKCE

```
┌──────────────────────────────────────────────────────────┐
│  Step 2: MCP Server Generates PKCE Parameters           │
│  ──────────────────────────────────────────────────────  │
│  Server Side (pkg/oauth/elicitation.go)                 │
│                                                           │
│  1. Generate random code_verifier (43-128 chars)        │
│     Example: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk" │
│                                                           │
│  2. Create code_challenge from verifier                  │
│     code_challenge = BASE64URL(SHA256(code_verifier))   │
│     Example: "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" │
│                                                           │
│  3. Generate random state (security token)               │
│     Example: "xyzABC123"                                 │
│                                                           │
│  4. Build GitHub authorization URL                       │
└──────────────────────────────────────────────────────────┘
       │
       │ Returns JSON
       ▼
┌─────────────────────────────────────────────────────────┐
│  Response to Browser:                                    │
│  {                                                       │
│    "url": "https://github.com/login/oauth/authorize?    │
│            client_id=Ov23lipjfVegsSqldX5H&              │
│            redirect_uri=http://localhost:8184/callback& │
│            scope=repo,user&                             │
│            state=xyzABC123&                             │
│            code_challenge=E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM& │
│            code_challenge_method=S256",                 │
│    "code_verifier": "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk" │
│  }                                                       │
└─────────────────────────────────────────────────────────┘
```

**Code Location**: `pkg/oauth/elicitation.go` - `GenerateAuthURL()` function

### Phase 3: Browser Stores PKCE Verifier and Redirects to GitHub

```
┌─────────────────────────────────────────────────────────┐
│  Step 3: Browser Stores code_verifier                   │
│  ──────────────────────────────────────────────────────  │
│  Browser (oauth-demo.html)                              │
│                                                          │
│  sessionStorage.setItem('code_verifier',                │
│    'dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk')      │
│                                                          │
│  sessionStorage.setItem('oauth_state', 'xyzABC123')     │
│                                                          │
│  window.location.href = data.url  // Redirect to GitHub │
└─────────────────────────────────────────────────────────┘
       │
       │ Browser redirects
       ▼
┌─────────────────────────────────────────────────────────┐
│  Step 4: User Authorizes on GitHub                      │
│  ──────────────────────────────────────────────────────  │
│  GitHub OAuth Page                                       │
│                                                          │
│  User sees: "Authorize [App Name]?"                     │
│  - Access to repositories                                │
│  - Access to user profile                                │
│                                                          │
│  User clicks "Authorize"                                 │
└─────────────────────────────────────────────────────────┘
```

### Phase 4: GitHub Redirects Back with Authorization Code

```
┌─────────────────────────────────────────────────────────┐
│  Step 5: GitHub Redirects to Callback URL               │
│  ──────────────────────────────────────────────────────  │
│  GitHub → Browser                                        │
│                                                          │
│  Redirect to:                                            │
│  http://localhost:8184/callback?                        │
│    code=abc123def456&                                   │
│    state=xyzABC123                                      │
│                                                          │
│  Note: GitHub validates code_challenge was provided     │
│        and stores it for later verification             │
└─────────────────────────────────────────────────────────┘
       │
       │ Browser loads callback page
       ▼
┌─────────────────────────────────────────────────────────┐
│  Step 6: Browser Extracts Code and State                │
│  ──────────────────────────────────────────────────────  │
│  Browser (oauth-demo.html - handleCallback())           │
│                                                          │
│  const urlParams = new URLSearchParams(location.search);│
│  const code = urlParams.get('code');                    │
│  const state = urlParams.get('state');                  │
│                                                          │
│  // Verify state matches (CSRF protection)              │
│  const savedState = sessionStorage.getItem('oauth_state');│
│  if (state !== savedState) {                            │
│    throw new Error('State mismatch');                   │
│  }                                                       │
└─────────────────────────────────────────────────────────┘
```

**Code Location**: `oauth-demo.html` - `handleCallback()` function

### Phase 5: Token Exchange (Browser → MCP Server → GitHub)

```
┌─────────────────────────────────────────────────────────┐
│  Step 7: Browser Requests Token Exchange                │
│  ──────────────────────────────────────────────────────  │
│  Browser → MCP Server                                    │
│                                                          │
│  GET /oauth/exchange-token?                             │
│    code=abc123def456&                                   │
│    code_verifier=dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk │
│                                                          │
│  Note: code_verifier retrieved from sessionStorage      │
└─────────────────────────────────────────────────────────┘
       │
       ▼
┌─────────────────────────────────────────────────────────┐
│  Step 8: MCP Server Exchanges Code for Token            │
│  ──────────────────────────────────────────────────────  │
│  MCP Server → GitHub API                                 │
│  POST https://github.com/login/oauth/access_token       │
│                                                          │
│  Body (form-encoded):                                    │
│  {                                                       │
│    "client_id": "Ov23lipjfVegsSqldX5H",                │
│    "client_secret": "ea6743e63c16c4b512fd610043951361adcd43f9", │
│    "code": "abc123def456",                              │
│    "redirect_uri": "http://localhost:8184/callback",    │
│    "code_verifier": "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk" │
│  }                                                       │
│                                                          │
│  GitHub validates:                                       │
│  1. client_id and client_secret match                   │
│  2. code is valid and not expired                       │
│  3. redirect_uri matches original request               │
│  4. SHA256(code_verifier) matches stored code_challenge │
└─────────────────────────────────────────────────────────┘
       │
       │ GitHub validates PKCE
       ▼
┌─────────────────────────────────────────────────────────┐
│  Step 9: GitHub Returns Access Token                    │
│  ──────────────────────────────────────────────────────  │
│  GitHub → MCP Server                                     │
│                                                          │
│  Response:                                               │
│  {                                                       │
│    "access_token": "gho_16C7e42F292c6912E7710c838347Ae178B4a",│
│    "token_type": "bearer",                              │
│    "scope": "repo,user"                                 │
│  }                                                       │
└─────────────────────────────────────────────────────────┘
       │
       │ MCP Server forwards token
       ▼
┌─────────────────────────────────────────────────────────┐
│  Step 10: MCP Server Returns Token to Browser           │
│  ──────────────────────────────────────────────────────  │
│  MCP Server → Browser                                    │
│                                                          │
│  Response:                                               │
│  {                                                       │
│    "access_token": "gho_16C7e42F292c6912E7710c838347Ae178B4a",│
│    "token_type": "bearer",                              │
│    "scope": "repo,user"                                 │
│  }                                                       │
└─────────────────────────────────────────────────────────┘
```

**Code Location**: `pkg/http/oauth/token_exchange.go` - `ServeHTTP()` method

### Phase 6: Fetch User Information

```
┌─────────────────────────────────────────────────────────┐
│  Step 11: Browser Stores Token and Fetches User Info    │
│  ──────────────────────────────────────────────────────  │
│  Browser (oauth-demo.html)                              │
│                                                          │
│  // Store token                                          │
│  sessionStorage.setItem('github_token', token);         │
│                                                          │
│  // Clean up PKCE data                                   │
│  sessionStorage.removeItem('code_verifier');            │
│  sessionStorage.removeItem('oauth_state');              │
│                                                          │
│  // Fetch user info                                      │
│  GET https://api.github.com/user                        │
│  Headers: {                                              │
│    Authorization: "Bearer gho_16C7e42F292c6912E7710c838347Ae178B4a" │
│  }                                                       │
└─────────────────────────────────────────────────────────┘
       │
       │ GitHub API call
       ▼
┌─────────────────────────────────────────────────────────┐
│  Step 12: GitHub Returns User Information               │
│  ──────────────────────────────────────────────────────  │
│  GitHub API → Browser                                    │
│                                                          │
│  Response:                                               │
│  {                                                       │
│    "login": "davidhadas",                               │
│    "id": 12345,                                         │
│    "name": "David Hadas",                               │
│    "email": "david@example.com",                        │
│    "avatar_url": "https://avatars.githubusercontent.com/...",│
│    "public_repos": 42,                                  │
│    "followers": 100,                                    │
│    "following": 50                                      │
│  }                                                       │
└─────────────────────────────────────────────────────────┘
       │
       │ Display in UI
       ▼
┌─────────────────────────────────────────────────────────┐
│  Step 13: Browser Displays User Information             │
│  ──────────────────────────────────────────────────────  │
│  Browser updates DOM to show:                            │
│  - User avatar                                           │
│  - Username                                              │
│  - Email                                                 │
│  - Repository count                                      │
│  - Follower/following counts                            │
│                                                          │
│  Login complete! ✅                                      │
└─────────────────────────────────────────────────────────┘
```

## Security Features

### 1. PKCE (Proof Key for Code Exchange)
```
Purpose: Prevents authorization code interception attacks

Flow:
1. Client generates random code_verifier
2. Client creates code_challenge = SHA256(code_verifier)
3. Client sends code_challenge to GitHub (not the verifier)
4. GitHub stores code_challenge
5. Client sends code_verifier during token exchange
6. GitHub verifies SHA256(code_verifier) == stored code_challenge

Why it's secure:
- Even if authorization code is intercepted, attacker cannot exchange it
- Attacker would need the original code_verifier (never transmitted to GitHub)
- code_challenge is one-way hash, cannot reverse to get verifier
```

### 2. State Parameter (CSRF Protection)
```
Purpose: Prevents Cross-Site Request Forgery attacks

Flow:
1. Client generates random state value
2. Client stores state in sessionStorage
3. Client includes state in authorization URL
4. GitHub includes state in callback redirect
5. Client verifies returned state matches stored state

Why it's secure:
- Attacker cannot forge callback with correct state
- State is unique per session
- Prevents malicious authorization attempts
```

### 3. Server-Side Token Exchange
```
Purpose: Keeps client_secret secure

Flow:
1. Browser never sees client_secret
2. Only MCP server has client_secret
3. Token exchange happens server-to-server
4. Browser only receives final access_token

Why it's secure:
- client_secret never exposed to browser
- Cannot be stolen from browser dev tools
- Cannot be extracted from page source
```

## Component Responsibilities

### Browser (oauth-demo.html)
- **Responsibilities**:
  - Initiate OAuth flow
  - Store PKCE verifier in sessionStorage
  - Handle GitHub callback
  - Verify state parameter
  - Request token exchange from MCP server
  - Store access token
  - Make GitHub API calls
  - Display user information

- **Security Role**:
  - Generates and stores PKCE verifier
  - Validates state parameter
  - Never handles client_secret

### MCP Server (localhost:8184)
- **Responsibilities**:
  - Generate authorization URLs with PKCE
  - Serve demo page
  - Handle OAuth callbacks
  - Exchange authorization codes for tokens
  - Proxy GitHub API requests (optional)

- **Security Role**:
  - Generates PKCE parameters
  - Securely stores client_secret
  - Performs server-to-server token exchange
  - Validates all OAuth parameters

### GitHub OAuth API
- **Responsibilities**:
  - Authenticate users
  - Display authorization consent screen
  - Generate authorization codes
  - Validate PKCE parameters
  - Issue access tokens
  - Provide user information API

- **Security Role**:
  - Validates client credentials
  - Stores and verifies code_challenge
  - Ensures code_verifier matches challenge
  - Issues time-limited tokens
  - Enforces scope restrictions

## File Locations

### Frontend
- `oauth-demo.html` - Complete OAuth flow UI and logic

### Backend - OAuth Handling
- `pkg/oauth/elicitation.go` - Authorization URL generation with PKCE
- `pkg/http/oauth/oauth.go` - OAuth route registration
- `pkg/http/oauth/token_exchange.go` - Token exchange handler
- `pkg/http/oauth/elicitation.go` - HTTP handlers for OAuth endpoints

### Backend - Server Setup
- `pkg/http/server.go` - HTTP server initialization and route setup
- `cmd/github-mcp-server/main.go` - Application entry point
- `config.yaml` - OAuth configuration (gitignored)

## Configuration

### Required OAuth Settings
```yaml
port: 8184
oauth-client-id: Ov23lipjfVegsSqldX5H
oauth-client-secret: ea6743e63c16c4b512fd610043951361adcd43f9
oauth-redirect-uri: http://localhost:8184/callback
oauth-scopes: repo,user
demo-page-path: ./oauth-demo.html
```

### GitHub App Setup
1. Create GitHub OAuth App at https://github.com/settings/developers
2. Set Authorization callback URL to `http://localhost:8184/callback`
3. Copy Client ID and Client Secret to config.yaml
4. Configure required scopes (repo, user)

## Testing the Flow

### Start Server
```bash
./github-mcp-server http --port 8184 \
  --oauth-client-id YOUR_CLIENT_ID \
  --oauth-client-secret YOUR_CLIENT_SECRET \
  --oauth-redirect-uri http://localhost:8184/callback \
  --oauth-scopes repo,user \
  --demo-page-path ./oauth-demo.html
```

### Test Steps
1. Open http://localhost:8184/demo
2. Click "Login with GitHub"
3. Authorize on GitHub
4. View your account information

### Monitor Logs
```bash
tail -f /tmp/mcp-server.log
```

## Troubleshooting

### Common Issues

1. **"State mismatch" error**
   - Cause: Browser sessionStorage cleared between steps
   - Solution: Complete flow in single browser session

2. **"code_verifier is required" error**
   - Cause: PKCE verifier not stored or retrieved
   - Solution: Check sessionStorage in browser dev tools

3. **"Invalid client" error**
   - Cause: Wrong client_id or client_secret
   - Solution: Verify credentials in config.yaml

4. **"Redirect URI mismatch" error**
   - Cause: Callback URL doesn't match GitHub app settings
   - Solution: Update GitHub app or config.yaml

## Summary

The OAuth flow uses **13 distinct steps** across **3 components** (Browser, MCP Server, GitHub) with **3 security mechanisms** (PKCE, State, Server-Side Exchange) to securely authenticate users and obtain access tokens for GitHub API access.

The key innovation is the **MCP Elicitation URL Mode** which allows the server to generate complete authorization URLs including PKCE parameters, while keeping the client_secret secure on the server side.