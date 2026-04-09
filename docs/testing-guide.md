# OAuth Demo Testing Guide

## 🎯 Quick Start

The services are now running! Follow these steps to test the complete OAuth flow.

## ✅ Services Status

Both services are running:
- **MCP Server**: http://localhost:8180 ✓
- **Demo Page**: http://localhost:3000 ✓

## 🧪 Testing the OAuth Flow

### Step 1: Open the Demo Page

Open your browser and navigate to:
```
http://localhost:3000
```

You should see a beautiful purple gradient page with the title "🔐 GitHub MCP Server OAuth Demo"

### Step 2: Start OAuth Login

1. Click the **"🚀 Login with GitHub"** button
2. You'll be redirected to GitHub's authorization page
3. The URL will look like:
   ```
   https://github.com/login/oauth/authorize?client_id=Ov23lipjfVegsSqldX5H&...
   ```

### Step 3: Authorize the Application

On GitHub's page:
1. Review the requested permissions:
   - Access to repositories (repo)
   - Access to user profile (user)
   - Access to organization data (read:org)
   - Access to workflows (workflow)
2. Click **"Authorize"** (or the green button)

### Step 4: Automatic Token Exchange

After authorization:
1. GitHub redirects back to: `http://localhost:3000/callback?code=...`
2. The demo server automatically redirects to: `http://localhost:3000/?code=...&state=...`
3. **JavaScript automatically calls** `/exchange-token` endpoint
4. Backend exchanges the code for an access token
5. You see: **"✅ Successfully authenticated with GitHub!"**

**No manual token input required!** 🎉

### Step 5: Test Repository Fetching

1. Click **"📚 Get My Repositories"**
2. You should see a list of your GitHub repositories with:
   - Repository name
   - Description
   - Star count
   - Fork count
   - Public/Private status

### Step 6: Test MCP Server

1. Click **"🔧 Test MCP Tools List"**
2. You should see a JSON response showing available MCP tools
3. This confirms the MCP server is working with your OAuth token

## 🔍 What to Verify

### ✅ Successful Flow Indicators

1. **No manual token prompt** - The flow should be completely automatic
2. **Green success message** - "✅ Successfully authenticated with GitHub!"
3. **Token display** - Shows masked token like `gho_xxxxxxxxxxxx...xxxxxxxx`
4. **Repositories load** - Your actual GitHub repos appear
5. **MCP response** - JSON with tools list appears

### ❌ Common Issues and Solutions

#### Issue: "Token exchange failed"
**Solution**: Check the demo server logs:
```bash
tail -f /tmp/demo-server.log
```
Look for error messages from GitHub.

#### Issue: "MCP Server error: 401"
**Solution**: 
- Verify MCP server is running: `curl http://localhost:8180/.well-known/oauth-protected-resource/mcp`
- Check token has correct scopes

#### Issue: "Address already in use"
**Solution**:
```bash
./stop-demo.sh
./start-demo.sh
```

## 📊 Monitoring

### View Demo Server Logs
```bash
tail -f /tmp/demo-server.log
```

You should see:
```
[OAuth Demo Server] GET / HTTP/1.1" 200
[OAuth Demo Server] Exchanging code for token...
[OAuth Demo Server] ✓ Token exchange successful
```

### View MCP Server Logs
```bash
tail -f /tmp/mcp-server.log
```

### Check Service Status
```bash
# Check if services are running
lsof -i :3000  # Demo server
lsof -i :8180  # MCP server

# Check PIDs
cat /tmp/demo-server.pid
cat /tmp/mcp-server.pid
```

## 🧪 Advanced Testing

### Test Token Exchange Endpoint Directly

```bash
# This will fail with GitHub error (invalid code) but shows endpoint works
curl -X POST http://localhost:3000/exchange-token \
  -H "Content-Type: application/json" \
  -d '{"code": "test_code_12345"}' | jq .
```

Expected response:
```json
{
  "error": "bad_verification_code",
  "error_description": "The code passed is incorrect or expired."
}
```

This confirms the endpoint is working and communicating with GitHub.

### Test MCP Server OAuth Metadata

```bash
curl http://localhost:8180/.well-known/oauth-protected-resource/mcp | jq .
```

Expected response:
```json
{
  "authorization_servers": [
    "https://github.com/login/oauth"
  ],
  "resource": "http://localhost:8180/mcp",
  "scopes_supported": ["repo", "user", "read:org", "workflow", ...]
}
```

## 🎬 Complete Test Sequence

Here's the full sequence to test everything:

```bash
# 1. Start services
./start-demo.sh

# 2. Verify services are running
curl -s http://localhost:8180/.well-known/oauth-protected-resource/mcp | jq -r '.authorization_servers[0]'
curl -s -o /dev/null -w "HTTP Status: %{http_code}\n" http://localhost:3000/

# 3. Open browser
open http://localhost:3000  # macOS
# or manually open http://localhost:3000

# 4. Complete OAuth flow in browser (steps 1-6 above)

# 5. Monitor logs in another terminal
tail -f /tmp/demo-server.log

# 6. When done, stop services
./stop-demo.sh
```

## 📸 Expected Screenshots

### Before Login
- Purple gradient background
- "Ready to authenticate" message
- "🚀 Login with GitHub" button visible

### After Login
- "✅ Authenticated successfully!" message
- Token display (masked)
- "🚪 Logout" button visible
- "📚 Get My Repositories" button enabled
- "🔧 Test MCP Tools List" button enabled

### After Fetching Repos
- List of repositories with details
- Each repo shows: name, description, stars, forks, visibility

### After Testing MCP
- JSON response with tools list
- Shows available MCP tools and their descriptions

## 🔐 Security Notes

- **Client Secret**: Safely stored in `start-demo-server.py` (server-side only)
- **Access Token**: Stored in browser's sessionStorage (cleared on tab close)
- **State Parameter**: CSRF protection implemented
- **HTTPS**: Use HTTPS in production (currently HTTP for local testing)

## 🛑 Stopping Services

When you're done testing:

```bash
./stop-demo.sh
```

This will cleanly stop both the Demo Server and MCP Server.

## 📞 Troubleshooting

### Services won't start
```bash
# Check if ports are in use
lsof -i :3000
lsof -i :8180

# Force kill and restart
./stop-demo.sh
./start-demo.sh
```

### OAuth flow fails
1. Check GitHub OAuth app settings: https://github.com/settings/developers
2. Verify callback URL is: `http://localhost:3000/callback`
3. Verify client ID matches: `Ov23lipjfVegsSqldX5H`

### Token exchange fails
1. Check demo server logs: `tail -f /tmp/demo-server.log`
2. Verify client secret in `start-demo-server.py` matches GitHub app
3. Authorization codes expire after 10 minutes

## ✅ Success Criteria

Your test is successful if:
- [x] No manual token input required
- [x] OAuth flow completes automatically
- [x] Repositories are fetched and displayed
- [x] MCP server responds with tools list
- [x] No errors in logs
- [x] Logout and re-login works

---

**Happy Testing!** 🎉

For more details, see:
- `BACKEND-TOKEN-EXCHANGE.md` - Implementation details
- `OAUTH-QUICKSTART.md` - OAuth configuration guide
- `start-demo.sh` - Service startup script
- `stop-demo.sh` - Service shutdown script