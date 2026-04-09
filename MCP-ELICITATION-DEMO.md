# MCP Elicitation Implementation in OAuth Demo

## 🎯 Overview

The OAuth demo now implements **MCP elicitation** as defined in the [MCP specification](https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation). This allows the demo to dynamically discover OAuth configuration from the MCP server instead of using hardcoded values.

## ✅ What Changed

### 1. **Removed Hardcoded OAuth Configuration**

**Before:**
```javascript
const CONFIG = {
    clientId: 'Ov23lipjfVegsSqldX5H',
    redirectUri: 'http://localhost:3000/callback',
    scopes: 'repo user read:org workflow',  // ← Hardcoded
    mcpServerUrl: 'http://localhost:8180'
};
```

**After:**
```javascript
const CONFIG = {
    clientId: 'Ov23lipjfVegsSqldX5H',
    redirectUri: 'http://localhost:3000/callback',
    mcpServerUrl: 'http://localhost:8180'
    // scopes removed - discovered dynamically
};

let discoveredConfig = null; // Stores discovered OAuth metadata
```

### 2. **Added OAuth Discovery Function**

New function `discoverOAuthMetadata()` that:
- Fetches `.well-known/oauth-protected-resource/mcp` from MCP server
- Parses OAuth metadata (authorization servers, scopes, resource)
- Stores configuration for use in OAuth flow
- Updates UI with discovered values

```javascript
async function discoverOAuthMetadata() {
    const metadataUrl = `${CONFIG.mcpServerUrl}/.well-known/oauth-protected-resource/mcp`;
    const response = await fetch(metadataUrl);
    const metadata = await response.json();
    
    discoveredConfig = {
        authorizationServer: metadata.authorization_servers[0],
        scopes: metadata.scopes_supported || [],
        resource: metadata.resource,
        bearerMethods: metadata.bearer_methods_supported || ['header']
    };
}
```

### 3. **Updated OAuth Flow to Use Discovered Config**

**Before:**
```javascript
function startOAuthFlow() {
    const authUrl = new URL('https://github.com/login/oauth/authorize'); // Hardcoded
    authUrl.searchParams.append('scope', CONFIG.scopes); // Hardcoded
}
```

**After:**
```javascript
async function startOAuthFlow() {
    // Discover config if not already done
    if (!discoveredConfig) {
        await discoverOAuthMetadata();
    }
    
    // Use discovered values
    const authUrl = new URL(discoveredConfig.authorizationServer + '/authorize');
    authUrl.searchParams.append('scope', discoveredConfig.scopes.join(' '));
}
```

### 4. **Added Discovery UI Section**

New section showing discovered OAuth configuration:
- Authorization Server URL
- Supported Scopes (first 5 + count)
- Resource URL

## 🔄 MCP Elicitation Flow

```
┌─────────────────────────────────────────────────────────┐
│ 1. Page Load                                            │
│    ↓                                                     │
│    GET /.well-known/oauth-protected-resource/mcp        │
│    ↓                                                     │
│    Response:                                            │
│    {                                                     │
│      "authorization_servers": [                         │
│        "https://github.com/login/oauth"                 │
│      ],                                                  │
│      "scopes_supported": [                              │
│        "repo", "user", "read:org", "workflow", ...      │
│      ],                                                  │
│      "resource": "http://localhost:8180/mcp"            │
│    }                                                     │
│    ↓                                                     │
│    Store in discoveredConfig                            │
│    Update UI                                            │
└─────────────────────────────────────────────────────────┘
                          ↓
┌─────────────────────────────────────────────────────────┐
│ 2. User Clicks "Login with GitHub"                     │
│    ↓                                                     │
│    Use discoveredConfig.authorizationServer             │
│    Use discoveredConfig.scopes                          │
│    ↓                                                     │
│    Redirect to: https://github.com/login/oauth/        │
│                 authorize?scope=repo+user+...           │
└─────────────────────────────────────────────────────────┘
                          ↓
                   (Rest of OAuth flow)
```

## 📊 Benefits

### 1. **Dynamic Configuration**
- Works with any MCP server that provides OAuth metadata
- No code changes needed if server changes scopes
- Adapts to different authorization servers

### 2. **Spec Compliant**
- Follows MCP elicitation specification
- Standard `.well-known` endpoint discovery
- Proper metadata parsing

### 3. **Better UX**
- Shows users what scopes will be requested
- Displays authorization server being used
- Transparent about OAuth configuration

### 4. **Flexible**
- Could support multiple MCP servers
- Easy to add server selection UI
- Future-proof for spec changes

## 🧪 Testing the Implementation

### 1. **Start Services**
```bash
./start-demo.sh
```

### 2. **Open Demo**
Navigate to: http://localhost:3000

### 3. **Observe Discovery**
- Page loads and immediately discovers OAuth config
- "Step 1" section shows discovered values:
  - Authorization Server: `https://github.com/login/oauth`
  - Supported Scopes: `repo, user, read:org, workflow, ...`
  - Resource: `http://localhost:8180/mcp`

### 4. **Test OAuth Flow**
- Click "🚀 Login with GitHub"
- OAuth flow uses discovered configuration
- Check browser console for discovery logs

### 5. **Verify in Console**
```javascript
// In browser console:
console.log(discoveredConfig);
// Shows:
// {
//   authorizationServer: "https://github.com/login/oauth",
//   scopes: ["repo", "user", "read:org", "workflow", ...],
//   resource: "http://localhost:8180/mcp",
//   bearerMethods: ["header"]
// }
```

## 🔍 Discovery Details

### Metadata Endpoint
```
GET http://localhost:8180/.well-known/oauth-protected-resource/mcp
```

### Response Format
```json
{
  "authorization_servers": [
    "https://github.com/login/oauth"
  ],
  "resource": "http://localhost:8180/mcp",
  "scopes_supported": [
    "repo",
    "read:org",
    "read:user",
    "user:email",
    "read:packages",
    "write:packages",
    "read:project",
    "project",
    "gist",
    "notifications",
    "workflow",
    "codespace"
  ],
  "bearer_methods_supported": [
    "header",
    "query"
  ]
}
```

## 🎨 UI Updates

### New Section: "Step 1: Discover OAuth Configuration"
Shows real-time discovery status and results:
- **Authorization Server**: Where users will be redirected
- **Supported Scopes**: What permissions are available
- **Resource**: The MCP server endpoint

### Updated Step Numbers
- Step 1: Discover OAuth Configuration (NEW)
- Step 2: Authenticate
- Step 3: Access Token
- Step 4: Fetch Your Repositories
- Step 5: Test MCP Server

## 🔐 Security Considerations

### What's Still Hardcoded (Intentionally)
- **Client ID**: App-specific, not discoverable
- **Redirect URI**: App-specific, registered with OAuth provider
- **MCP Server URL**: User input or configuration

### What's Discovered (Dynamic)
- **Authorization Server**: From MCP server metadata
- **Scopes**: From MCP server metadata
- **Resource URL**: From MCP server metadata

## 📝 Code Changes Summary

| File | Changes | Lines Added |
|------|---------|-------------|
| `oauth-demo.html` | Added discovery function | ~35 |
| `oauth-demo.html` | Updated startOAuthFlow() | ~15 |
| `oauth-demo.html` | Added discovery UI | ~10 |
| `oauth-demo.html` | Updated page load handler | ~5 |
| **Total** | | **~65 lines** |

## ✅ Compliance Checklist

- [x] Discovers OAuth metadata from `.well-known` endpoint
- [x] Parses `authorization_servers` array
- [x] Uses `scopes_supported` for OAuth request
- [x] Stores `resource` URL
- [x] Handles discovery errors gracefully
- [x] Falls back to retry on login if initial discovery fails
- [x] Displays discovered configuration to user
- [x] Uses discovered config in OAuth flow

## 🚀 Future Enhancements

### Possible Additions
1. **401 Challenge Handling**: Parse `WWW-Authenticate` header
2. **Multiple Servers**: Allow user to select MCP server
3. **Scope Selection**: Let user choose which scopes to request
4. **Discovery Cache**: Cache metadata to reduce requests
5. **Refresh Discovery**: Button to re-discover configuration

## 📚 References

- [MCP Elicitation Specification](https://modelcontextprotocol.io/specification/2025-11-25/client/elicitation)
- [RFC 8414: OAuth 2.0 Authorization Server Metadata](https://tools.ietf.org/html/rfc8414)
- [RFC 8707: Resource Indicators for OAuth 2.0](https://tools.ietf.org/html/rfc8707)

---

**Implementation Date**: 2026-04-09  
**Status**: ✅ Complete and tested  
**Spec Compliance**: ✅ MCP Elicitation 2025-11-25  
**Backward Compatible**: ✅ Yes