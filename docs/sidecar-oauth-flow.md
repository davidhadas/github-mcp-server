# Sidecar OAuth Flow - Implementation Plan

## Executive Summary

The current sidecar implementation does NOT properly suspend and resume MCP requests during OAuth. This document details the correct flow and required changes.

### Initial Request Flow 

1. Frontend → Backend → Sidecar INBOUND → AI Agent
   - INBOUND: Generates correlation key (resume_key)
   - INBOUND: Creates empty OAuth session with key
   - INBOUND: Forwards to AI Agent with a resume_key as a request header
   - INBOUND: Waits on TWO channels:
     a) AI Agent upstream response (normal flow)
     b) OAuth discovery signal from OUTBOUND (OAuth needed)


### FLOW FOR Token Found (OAuth Not Required)
2. AI Agent → Sidecar OUTBOUND → MCP Server 
   - AI Agent converts task to MCP request and copy resume_key header
   - OUTBOUND: Extracts resume_key from AI Agent request headers
   - OUTBOUND: Checks token cache: TOKEN FOUND
   - OUTBOUND: Adds Authorization header to MCP request
   - OUTBOUND: Forwards to MCP Server

### FLOW FOR No Token Found (OAuth Required)

2. AI Agent → Sidecar OUTBOUND → MCP Server
   - AI Agent converts task to MCP request
   - OUTBOUND: Extracts resume_key from AI Agent request headers
   - OUTBOUND: Checks token cache: NO TOKEN FOUND

3. Sidecar OUTBOUND discovers OAuth and signals INBOUND
   - OUTBOUND: Sends SYNTHETIC request to the same mcp tool of the MCP Server (without token)
   - OUTBOUND: MCP Server returns 401 with OAuth discovery info
   - OUTBOUND: Extracts auth_url, generates code_verifier
   - OUTBOUND: Updates OAuth session with discovery data
   - OUTBOUND → INBOUND: Signals via session's discovery channel
   - OUTBOUND: BLOCKS on OAuthDataChan, waiting for OAuth completion

4. Sidecar INBOUND receives OAuth discovery signal
   - INBOUND: Receives signal on discovery channel (wins the race)
   - INBOUND: Reads OAuth session for auth_url, code_verifier, mcp_server_url
   - INBOUND: Returns 401 to Backend with:
     * auth_url
     * code_verifier
     * mcp_server_url
     * resume_key
   - INBOUND: AI Agent upstream request continues in background
   
5. Backend → Frontend (redirect to GitHub)
   - Frontend receives 401 with OAuth URL and resume_key
   - Redirects user to GitHub OAuth page

   **STATE**: OUTBOUND blocked waiting, INBOUND detached, AI Agent request alive

6. Frontend gets code from GitHub → Backend → Sidecar INBOUND
   - Frontend sends: X-Authbridge-Resume: <resume_key>
   - Plus: X-OAuth-Code, X-Code-Verifier, X-MCP-Server-URL, X-User-ID

7. Sidecar INBOUND sends OAuth data to OUTBOUND
   - INBOUND: Detects X-Authbridge-Resume header
   - INBOUND: Looks up OAuth session by resume_key
   - INBOUND: Extracts OAuth completion data from headers
   - INBOUND → OUTBOUND: Sends via session.OAuthDataChan:
     * oauth_code
     * code_verifier
     * mcp_server_url
   - INBOUND: Attaches to pending AI Agent response
   - INBOUND: Waits on TWO channels:
     a) AI Agent upstream response (normal flow)
     b) OAuth discovery signal from OUTBOUND (OAuth needed)

8. Sidecar OUTBOUND receives OAuth data and exchanges token
   - OUTBOUND: Receives from OAuthDataChan (unblocks)
   - OUTBOUND: Exchanges code for token via MCP Server
   - OUTBOUND: Caches token
   - OUTBOUND: Adds Authorization header to MCP request
   - OUTBOUND: Forwards to MCP Server


### Response Flow

9. Response flows back normally
   - MCP Server → OUTBOUND → AI Agent → INBOUND → Backend → Frontend
   - INBOUND cleans up OAuth session after streaming response

---

## Synchronization Mechanisms Summary

### OAuthSession - One Per User Request

```go
type OAuthSession struct {
    UserID       string    // Token cache key
    MCPServerURL string    // Current MCP server (changes per OAuth flow)
    CreatedAt    time.Time // For duration monitoring
    
    OAuthRequiredChan chan *OAuthDiscoveryData  // OUTBOUND → INBOUND
    OAuthCompleteChan chan *OAuthCompletionData // INBOUND → OUTBOUND
}
```

**Scope:** One session per user request (resume_key), handles zero or more OAuth flows for different MCP servers.

### Channel Data Structures

**OAuthDiscoveryData (OUTBOUND → INBOUND):**
- `AuthURL` - OAuth redirect URL for frontend
- `CodeVerifier` - PKCE verifier (frontend echoes back)

**OAuthCompletionData (INBOUND → OUTBOUND):**
- `Code` - OAuth authorization code
- `CodeVerifier` - For validation
- `Error` - Empty = success, non-empty = error message

### Correlation Locks

```go
correlationLocks map[string]*sync.Mutex  // Key = resume_key
```

**Purpose:** Serialize OAuth flows within a single user request. Prevents duplicate OAuth when AI Agent calls multiple MCP servers in parallel.

### Session Lifecycle

1. **Created:** INBOUND when user request arrives
2. **Lifetime:** From initial request until AI Agent responds (can be minutes with multiple OAuth flows)
3. **Cleanup:** INBOUND deletes session after streaming AI Agent response (uses context to pass resume_key)

### Key Properties

- **Single Goroutine:** Same goroutine handles initial request, all resume requests, and cleanup
- **Reusable Channels:** Same channels used for multiple OAuth flows (different MCP servers)
- **Context Propagation:** resume_key stored in request context for cleanup
- **Duration Logging:** Tracks OAuth duration per flow and total session duration
- **Security:** Code verifier validation, multi-tenant token isolation, 5-minute OAuth timeout

