# GitHub MCP Server - OAuth Quick Start

## ✅ Setup Complete!

Your GitHub MCP Server is now running with OAuth multi-user authentication.

## 🚀 Server Status

- **Status**: ✅ Running
- **Port**: 8180
- **PID**: Check with `cat /tmp/mcp-oauth-server.pid`
- **Logs**: `/tmp/mcp-oauth-server.log`
- **Config**: `cmd/github-mcp-server/config.yaml`

## 🔐 OAuth Configuration

- **Client ID**: `Ov23lipjfVegsSqldX5H`
- **Callback URL**: `http://localhost:3000/callback`
- **Scopes**: repo, user, read:org, workflow
- **GitHub OAuth App**: https://github.com/settings/developers

## 📖 Quick Commands

### Check Server Status
```bash
curl http://localhost:8180/.well-known/oauth-protected-resource/mcp | jq .
```

### View Server Logs
```bash
tail -f /tmp/mcp-oauth-server.log
```

### Stop Server
```bash
kill $(cat /tmp/mcp-oauth-server.pid)
```

### Restart Server
```bash
cd cmd/github-mcp-server && ./github-mcp-server http
```

## 🧪 Test with Personal Access Token

For quick testing, you can use a GitHub Personal Access Token:

1. **Create token**: https://github.com/settings/tokens
   - Select scopes: `repo`, `user`, `read:org`, `workflow`

2. **Test request**:
   ```bash
   curl -X POST http://localhost:8180/mcp \
     -H "Authorization: Bearer YOUR_GITHUB_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{
       "jsonrpc": "2.0",
       "method": "tools/list",
       "id": 1
     }' | jq .
   ```

## 🔄 OAuth Flow for MCP Clients

### 1. Discover OAuth Configuration
```bash
GET http://localhost:8180/.well-known/oauth-protected-resource/mcp
```

Response includes:
- `authorization_servers`: ["https://github.com/login/oauth"]
- `scopes_supported`: [repo, user, read:org, workflow, ...]
- `resource`: "http://localhost:8180/mcp"

### 2. Redirect User to GitHub
```
https://github.com/login/oauth/authorize?
  client_id=Ov23lipjfVegsSqldX5H&
  redirect_uri=http://localhost:3000/callback&
  scope=repo+user+read:org+workflow&
  state=<random-state>&
  code_challenge=<pkce-challenge>&
  code_challenge_method=S256
```

### 3. Handle Callback
User is redirected to: `http://localhost:3000/callback?code=<code>&state=<state>`

### 4. Exchange Code for Token
```bash
POST https://github.com/login/oauth/access_token
Content-Type: application/json

{
  "client_id": "Ov23lipjfVegsSqldX5H",
  "client_secret": "YOUR_CLIENT_SECRET",
  "code": "<authorization-code>",
  "redirect_uri": "http://localhost:3000/callback",
  "code_verifier": "<pkce-verifier>"
}
```

### 5. Make Authenticated Requests
```bash
POST http://localhost:8180/mcp
Authorization: Bearer <github-access-token>
Content-Type: application/json

{
  "jsonrpc": "2.0",
  "method": "tools/list",
  "id": 1
}
```

## 📚 Full Documentation

See [`docs/github-oauth-setup.md`](docs/github-oauth-setup.md) for:
- Complete architecture diagrams
- Detailed OAuth flow explanation
- Security best practices
- Troubleshooting guide
- MCP client implementation guide

## 🎯 Key Features

✅ **Multi-User Support**: Each user authenticates with their own GitHub account  
✅ **Per-User Permissions**: Requests use each user's GitHub permissions  
✅ **Scope Validation**: Server validates tokens have required scopes  
✅ **Secure**: OAuth 2.0 with PKCE support  
✅ **Audit Trail**: Each request is tied to a specific user  

## 🔧 Configuration File

Location: `cmd/github-mcp-server/config.yaml`

```yaml
port: 8180
host: https://api.github.com
oauth-client-id: Ov23lipjfVegsSqldX5H
oauth-client-secret: ea6743e63c16c4b512fd610043951361adcd43f9
oauth-redirect-uri: http://localhost:3000/callback
oauth-scopes:
  - repo
  - user
  - read:org
  - workflow
scope-challenge: true
```

## ⚠️ Important Notes

1. **Client Secret Security**: Never commit `oauth-client-secret` to version control
2. **Callback URL**: Must match exactly in GitHub OAuth App settings
3. **User Tokens**: Each user needs to authorize the app
4. **Token Validation**: Server validates tokens on every request
5. **Scope Requirements**: Tokens must have all configured scopes

## 🆘 Troubleshooting

### Server Not Responding
```bash
# Check if server is running
ps aux | grep github-mcp-server

# Check logs
tail -f /tmp/mcp-oauth-server.log

# Restart server
kill $(cat /tmp/mcp-oauth-server.pid)
cd cmd/github-mcp-server && ./github-mcp-server http
```

### 401 Unauthorized
- Token is invalid or expired
- User hasn't authorized the app
- Check token: `curl -H "Authorization: Bearer TOKEN" https://api.github.com/user`

### 403 Forbidden
- Token lacks required scopes
- Verify token has: repo, user, read:org, workflow

## 📞 Next Steps

1. **Implement MCP Client**: Use the OAuth flow to authenticate users
2. **Test Integration**: Use curl or Postman to test requests
3. **Deploy**: Consider using HTTPS for production
4. **Monitor**: Set up logging and monitoring
5. **Scale**: Add load balancing for multiple instances

---

**Setup Date**: 2026-04-09  
**Server Version**: Latest  
**OAuth Provider**: GitHub  
**Documentation**: [`docs/github-oauth-setup.md`](docs/github-oauth-setup.md)