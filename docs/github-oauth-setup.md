# GitHub MCP Server - OAuth Multi-User Setup Guide

## Overview

Your GitHub MCP Server is now configured with OAuth authentication, requiring each user to authenticate with their own GitHub account before making requests. This guide explains how the system works and how to use it.

## ✅ What's Been Configured

### Server Configuration
- **Port**: 8180
- **OAuth Client ID**: Ov23lipjfVegsSqldX5H
- **OAuth Callback URL**: http://localhost:3000/callback
- **OAuth Scopes**: repo, user, read:org, workflow
- **Security**: scope-challenge enabled
- **API Host**: https://api.github.com

### Configuration File
Location: `cmd/github-mcp-server/config.yaml`

## 🔐 How OAuth Authentication Works

### Architecture

```
┌─────────────┐         ┌──────────────────┐         ┌─────────────┐
│  MCP Client │────────▶│  MCP Server      │────────▶│   GitHub    │
│  (Port 3000)│         │  (Port 8180)     │         │   API       │
└─────────────┘         └──────────────────┘         └─────────────┘
       │                         │                           │
       │  1. Discover metadata   │                           │
       │◀────────────────────────┤                           │
       │                         │                           │
       │  2. Redirect user to GitHub for auth                │
       │─────────────────────────────────────────────────────▶│
       │                         │                           │
       │  3. User authorizes     │                           │
       │◀─────────────────────────────────────────────────────│
       │                         │                           │
       │  4. Exchange code for token (client-side)           │
       │─────────────────────────────────────────────────────▶│
       │                         │                           │
       │  5. Make MCP requests with Bearer token             │
       │────────▶│               │                           │
       │         │  6. Validate token & forward to GitHub    │
       │         │───────────────────────────────────────────▶│
```

### Authentication Flow

1. **Discovery**: MCP client discovers OAuth configuration
   ```bash
   GET http://localhost:8180/.well-known/oauth-protected-resource/mcp
   ```

2. **User Authorization**: Client redirects user to GitHub
   ```
   https://github.com/login/oauth/authorize?
     client_id=Ov23lipjfVegsSqldX5H&
     redirect_uri=http://localhost:3000/callback&
     scope=repo+user+read:org+workflow&
     state=<random-state>&
     code_challenge=<pkce-challenge>&
     code_challenge_method=S256
   ```

3. **Callback**: GitHub redirects back with authorization code
   ```
   http://localhost:3000/callback?code=<auth-code>&state=<state>
   ```

4. **Token Exchange**: Client exchanges code for access token
   ```bash
   POST https://github.com/login/oauth/access_token
   ```

5. **API Requests**: Client makes requests with Bearer token
   ```bash
   POST http://localhost:8180/mcp
   Authorization: Bearer <github-access-token>
   ```

## 🚀 Using the Server

### For MCP Client Developers

Your MCP client needs to:

1. **Discover OAuth metadata**:
   ```javascript
   const metadata = await fetch('http://localhost:8180/.well-known/oauth-protected-resource/mcp');
   // Returns: authorization_servers, scopes_supported, etc.
   ```

2. **Implement OAuth 2.0 flow**:
   - Generate PKCE challenge
   - Redirect user to GitHub authorization URL
   - Handle callback with authorization code
   - Exchange code for access token
   - Store token securely

3. **Make authenticated requests**:
   ```javascript
   fetch('http://localhost:8180/mcp', {
     method: 'POST',
     headers: {
       'Authorization': `Bearer ${githubAccessToken}`,
       'Content-Type': 'application/json'
     },
     body: JSON.stringify({
       // MCP request
     })
   });
   ```

### Testing with curl

1. **Check server is running**:
   ```bash
   curl http://localhost:8180/.well-known/oauth-protected-resource/mcp | jq .
   ```

2. **Get a GitHub Personal Access Token** (for testing):
   - Go to: https://github.com/settings/tokens
   - Generate new token with scopes: repo, user, read:org, workflow
   
3. **Make authenticated request**:
   ```bash
   curl -X POST http://localhost:8180/mcp \
     -H "Authorization: Bearer YOUR_GITHUB_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{
       "jsonrpc": "2.0",
       "method": "tools/list",
       "id": 1
     }'
   ```

## 📋 Server Management

### Start the Server
```bash
cd cmd/github-mcp-server
./github-mcp-server http
```

### Stop the Server
```bash
kill $(cat /tmp/mcp-oauth-server.pid)
```

### View Logs
```bash
tail -f /tmp/mcp-oauth-server.log
```

### Check Server Status
```bash
curl http://localhost:8180/.well-known/oauth-protected-resource/mcp
```

## 🔧 Configuration Reference

### Current Configuration (`cmd/github-mcp-server/config.yaml`)

```yaml
# Server Settings
port: 8180
host: https://api.github.com

# OAuth Configuration
oauth-client-id: Ov23lipjfVegsSqldX5H
oauth-client-secret: ea6743e63c16c4b512fd610043951361adcd43f9
oauth-redirect-uri: http://localhost:3000/callback
oauth-scopes:
  - repo
  - user
  - read:org
  - workflow

# Security Settings
scope-challenge: true
lockdown-mode: false

# Performance Settings
content-window-size: 5000
repo-access-cache-ttl: 5m
```

### Modifying Configuration

To change settings:
1. Edit `cmd/github-mcp-server/config.yaml`
2. Restart the server
3. Changes take effect immediately

## 🔒 Security Considerations

### OAuth Client Secret
- **Never commit** the client secret to version control
- Store securely in the config file
- Rotate periodically
- If compromised, regenerate in GitHub OAuth App settings

### Access Tokens
- Tokens are issued per-user by GitHub
- Each user's token has their permissions
- Tokens can be revoked by users at any time
- Server validates tokens on each request

### Scope Challenge
- Enabled by default (`scope-challenge: true`)
- Server validates that tokens have required scopes
- Returns 403 if token lacks necessary permissions
- Helps prevent unauthorized access

## 🎯 Per-User Authentication

### How It Works

1. **User A** authenticates → Gets Token A → Makes requests as User A
2. **User B** authenticates → Gets Token B → Makes requests as User B
3. Each user's requests use **their own GitHub permissions**

### Benefits

- ✅ No shared credentials
- ✅ Audit trail per user
- ✅ Fine-grained access control
- ✅ Users can revoke their own access
- ✅ Respects GitHub's rate limits per user

## 📚 Additional Resources

### GitHub OAuth Documentation
- [Creating an OAuth App](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/creating-an-oauth-app)
- [Authorizing OAuth Apps](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps)
- [OAuth Scopes](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps)

### MCP Server Documentation
- [Configuration File](./configuration-file.md)
- [Remote Server](./remote-server.md)
- [Scope Filtering](./scope-filtering.md)

## 🐛 Troubleshooting

### Server won't start
- Check if port 8180 is available: `lsof -i :8180`
- Verify config file syntax: `cat cmd/github-mcp-server/config.yaml`
- Check logs: `tail -f /tmp/mcp-oauth-server.log`

### Authentication fails
- Verify OAuth App settings in GitHub match config
- Check callback URL is exactly: `http://localhost:3000/callback`
- Ensure client ID and secret are correct
- Verify user has authorized the app

### 401 Unauthorized errors
- Token may be expired or invalid
- User may have revoked access
- Token may lack required scopes
- Check token with: `curl -H "Authorization: Bearer TOKEN" https://api.github.com/user`

### 403 Forbidden errors
- Token lacks required OAuth scopes
- Check scope-challenge is properly configured
- Verify token has: repo, user, read:org, workflow scopes

## 📞 Support

For issues or questions:
1. Check the logs: `/tmp/mcp-oauth-server.log`
2. Review GitHub OAuth App settings
3. Verify MCP client implementation
4. Check GitHub API status: https://www.githubstatus.com/

---

**Server Status**: ✅ Running on port 8180  
**OAuth Enabled**: ✅ Yes  
**Multi-User Support**: ✅ Active  
**Configuration**: `cmd/github-mcp-server/config.yaml`