# Sidecar TCP Connection Failure Analysis

**Date:** 2026-04-14  
**Component:** authbridge-sidecar  
**Purpose:** Comprehensive analysis of TCP connection failures across all system states

---

## System States and TCP Connections

### State A: User → Inbound (Initial Connection)

**TCP Connections:**
1. Frontend → Inbound (port 15001)

**Code Location:** [`cmd/authbridge-sidecar/main.go:147-160`](cmd/authbridge-sidecar/main.go:147-160)

**Resources:**
- None allocated yet

---

### State B: User → Inbound → AI Agent (Request Forwarded)

**TCP Connections:**
1. Frontend → Inbound (port 15001)
2. Inbound → AI Agent (port 8186)

**Code Locations:**
- Inbound proxy: [`cmd/authbridge-sidecar/main.go:177`](cmd/authbridge-sidecar/main.go:177)
- ExtendedReverseProxy: [`pkg/extendedreverseproxy/proxy.go:73-91`](pkg/extendedreverseproxy/proxy.go:73-91)

**Resources:**
- OAuthSession created at [`cmd/authbridge-sidecar/main.go:341-348`](cmd/authbridge-sidecar/main.go:341-348)
- Stored in map at [`cmd/authbridge-sidecar/main.go:383-387`](cmd/authbridge-sidecar/main.go:383-387)

---

### State C: User → Inbound → AI Agent → Outbound (Checking Token)

**TCP Connections:**
1. Frontend → Inbound (port 15001)
2. Inbound → AI Agent (port 8186)
3. AI Agent → Outbound (sidecar intercept)

**Code Locations:**
- Outbound handler: [`cmd/authbridge-sidecar/main.go:180-233`](cmd/authbridge-sidecar/main.go:180-233)
- Token check: [`cmd/authbridge-sidecar/main.go:464-469`](cmd/authbridge-sidecar/main.go:464-469)

**Resources:**
- OAuthSession exists
- No correlation lock yet

---

### State D: User → Inbound → AI Agent → Outbound → MCP (Service Call with Token)

**TCP Connections:**
1. Frontend → Inbound (port 15001)
2. Inbound → AI Agent (port 8186)
3. AI Agent → Outbound (sidecar intercept)
4. Outbound → MCP (via reverse proxy)

**Code Locations:**
- MCP proxy: [`cmd/authbridge-sidecar/main.go:232`](cmd/authbridge-sidecar/main.go:232)
- Reverse proxy: [`cmd/authbridge-sidecar/main.go:107`](cmd/authbridge-sidecar/main.go:107)

**Resources:**
- OAuthSession exists
- Token cached
- No correlation lock

---

### State E: User → Inbound → AI Agent → Outbound (Waiting for Lock)

**TCP Connections:**
1. Frontend → Inbound (port 15001)
2. Inbound → AI Agent (port 8186)
3. AI Agent → Outbound (sidecar intercept)

**Code Locations:**
- Lock acquisition: [`cmd/authbridge-sidecar/main.go:477-479`](cmd/authbridge-sidecar/main.go:477-479)
- Lock management: [`cmd/authbridge-sidecar/main.go:449-457`](cmd/authbridge-sidecar/main.go:449-457)

**Resources:**
- OAuthSession exists
- Goroutine blocked on `lock.Lock()`
- Another goroutine holds lock (doing OAuth for same MCP server)

---

### State F: User → Inbound → AI Agent → Outbound → MCP (Synthetic tools/list)

**TCP Connections:**
1. Frontend → Inbound (port 15001)
2. Inbound → AI Agent (port 8186)
3. AI Agent → Outbound (sidecar intercept)
4. Outbound → MCP (synthetic request)

**Code Locations:**
- Synthetic request: [`internal/authbridge/authbridge.go:166-171`](internal/authbridge/authbridge.go:166-171)
- Called from: [`cmd/authbridge-sidecar/main.go:500`](cmd/authbridge-sidecar/main.go:500)
- HTTP POST: [`internal/authbridge/authbridge.go:228-232`](internal/authbridge/authbridge.go:228-232)

**Resources:**
- OAuthSession exists
- Correlation lock **HELD**
- Goroutine owns lock

---

### State G: User → Inbound → AI Agent → Outbound → MCP (OAuth Discovery)

**TCP Connections:**
1. Frontend → Inbound (port 15001)
2. Inbound → AI Agent (port 8186)
3. AI Agent → Outbound (sidecar intercept)
4. Outbound → MCP (/auth/url endpoint)

**Code Locations:**
- Discovery call: [`internal/authbridge/authbridge.go:228-232`](internal/authbridge/authbridge.go:228-232)
- Response handling: [`internal/authbridge/authbridge.go:245-272`](internal/authbridge/authbridge.go:245-272)

**Resources:**
- OAuthSession exists
- Correlation lock **HELD**
- Goroutine owns lock

---

### State H: Backend connection #1 → Inbound → AI Agent → Outbound (Always Detached / Waiting)

**TCP Connections:**
1. Backend connection #1 → Inbound (port 15001) - receives immediate `202` with [`resume_key`](cmd/authbridge-sidecar/main.go:289)
2. Inbound → AI Agent (port 8186) - **BACKGROUND GOROUTINE**
3. AI Agent → Outbound (sidecar intercept)

**Code Locations:**
- Always-detach decision for user-scoped requests: [`cmd/authbridge-sidecar/main.go:358`](cmd/authbridge-sidecar/main.go:358)
- Detached execution: [`pkg/extendedreverseproxy/proxy.go:168`](pkg/extendedreverseproxy/proxy.go:168)
- OAuth wait path: [`cmd/authbridge-sidecar/main.go:591`](cmd/authbridge-sidecar/main.go:591)

**Resources:**
- [`OAuthSession`](cmd/authbridge-sidecar/main.go:45) exists
- [`Detached`](cmd/authbridge-sidecar/main.go:56) flag is set
- Pending exchange exists in the proxy store
- Correlation lock may be **HELD**
- The first backend connection has already been answered and is no longer needed for the final result
- Resume is now purely key-based and independent of whether backend connection #1 stays open or closes

---

### State I: Backend connection #2 → Inbound (Resume) while backend connection #1 may still be open

**TCP Connections:**
1. Backend connection #1 may still exist at the TCP layer, but it is already logically complete after the `202`
2. Backend connection #2 → Inbound (port 15001) - **RESUME REQUEST**
3. Inbound → AI Agent (port 8186) - **RESUME FORWARDED**
4. AI Agent → Outbound (sidecar intercept) - **STILL CONNECTED**

**Code Locations:**
- Resume classification via [`X-Authbridge-Resume`](cmd/authbridge-sidecar/main.go:277): [`cmd/authbridge-sidecar/main.go:276`](cmd/authbridge-sidecar/main.go:276)
- Resume handling: [`cmd/authbridge-sidecar/main.go:317`](cmd/authbridge-sidecar/main.go:317)
- Resume proxy consumption of pending exchange: [`pkg/extendedreverseproxy/proxy.go:209`](pkg/extendedreverseproxy/proxy.go:209)

**Resources:**
- OAuthSession still exists from the detached initial request
- Correlation is based on [`resume_key`](cmd/authbridge-sidecar/main.go:289), not socket identity
- Pending exchange exists
- A second backend connection can resume even while the first connection remains open but idle

---

### State J: Backend connection #2 → Inbound (Resume) → AI Agent → Outbound → MCP (OAuth Completion / Token Exchange)

**TCP Connections:**
1. Backend connection #2 → Inbound (port 15001)
2. Inbound → AI Agent (port 8186)
3. AI Agent → Outbound (sidecar intercept)
4. Outbound → MCP (`/oauth/exchange-token`)

**Code Locations:**
- OAuth completion send to blocked outbound path: [`cmd/authbridge-sidecar/main.go:338`](cmd/authbridge-sidecar/main.go:338)
- Token exchange: [`internal/authbridge/authbridge.go:623`](internal/authbridge/authbridge.go:623)
- Called from: [`cmd/authbridge-sidecar/main.go:633`](cmd/authbridge-sidecar/main.go:633)

**Resources:**
- OAuthSession exists
- Pending exchange exists
- Correlation lock may still be **HELD**
- Resume on the second connection unblocks the original detached outbound flow

---

### State K: Backend connection #2 → Inbound (Resume) → AI Agent → Outbound → MCP (Service Call with New Token) → Final Response

**TCP Connections:**
1. Backend connection #2 → Inbound (port 15001)
2. Inbound → AI Agent (port 8186)
3. AI Agent → Outbound (sidecar intercept)
4. Outbound → MCP (actual service call)

**Code Locations:**
- MCP call: [`cmd/authbridge-sidecar/main.go:250`](cmd/authbridge-sidecar/main.go:250)
- Token cached: [`internal/authbridge/authbridge.go:671`](internal/authbridge/authbridge.go:671)
- Detached-session preservation and final cleanup: [`cmd/authbridge-sidecar/main.go:428`](cmd/authbridge-sidecar/main.go:428)

**Resources:**
- OAuthSession exists until the final response is produced on the resume path
- Correlation lock is released after token acquisition path completes
- Token cached
- Pending exchange exists until consumed by the resume request on connection #2

---

### State L: User → Inbound → AI Agent (Responding, No MCP)

**TCP Connections:**
1. Frontend → Inbound (port 15001)
2. Inbound → AI Agent (port 8186) - **RESPONSE STREAMING**

**Code Locations:**
- Response streaming: [`pkg/extendedreverseproxy/streaming.go:14-37`](pkg/extendedreverseproxy/streaming.go:14-37)
- Cleanup: [`cmd/authbridge-sidecar/main.go:407-438`](cmd/authbridge-sidecar/main.go:407-438)

**Resources:**
- OAuthSession exists
- Response body open
- `defer resp.Body.Close()` at [`pkg/extendedreverseproxy/streaming.go:15`](pkg/extendedreverseproxy/streaming.go:15)

---

### State M: User → Inbound → AI Agent → Outbound ← MCP (Response Streaming)

**TCP Connections:**
1. Frontend → Inbound (port 15001)
2. Inbound → AI Agent (port 8186) - **RESPONSE STREAMING**
3. AI Agent → Outbound (sidecar intercept)
4. Outbound ← MCP (response streaming)

**Code Locations:**
- MCP response: Standard reverse proxy
- AI Agent streaming: [`pkg/extendedreverseproxy/streaming.go:14-37`](pkg/extendedreverseproxy/streaming.go:14-37)

**Resources:**
- OAuthSession exists
- Multiple response bodies open
- Cleanup via `ModifyResponse`

---

### State N: User → Inbound (Resume) → AI Agent (Final Response)

**TCP Connections:**
1. Frontend → Inbound (port 15001) - **RESUME REQUEST**
2. Inbound → AI Agent (port 8186) - **RESPONSE STREAMING**

**Code Locations:**
- Resume response: [`pkg/extendedreverseproxy/proxy.go:230-248`](pkg/extendedreverseproxy/proxy.go:230-248)
- Streaming: [`pkg/extendedreverseproxy/streaming.go:14-37`](pkg/extendedreverseproxy/streaming.go:14-37)
- Cleanup: [`pkg/extendedreverseproxy/proxy.go:226-227`](pkg/extendedreverseproxy/proxy.go:226-227)

**Resources:**
- OAuthSession exists
- Pending exchange exists
- Response body open
- Deferred cleanup

---

## Verified Remaining Issues

### ⚠️ Pending Exchange Retention After Detach

**Affected States:** H, I, J, K, N

**Status:** Partially mitigated, not fully solved

**What is solved:**
- The detached upstream request now runs with the pending exchange context in [`pkg/extendedreverseproxy/proxy.go:196-205`](pkg/extendedreverseproxy/proxy.go:196-205), and resume cleanup explicitly cancels and deletes the pending exchange in [`pkg/extendedreverseproxy/proxy.go:217-226`](pkg/extendedreverseproxy/proxy.go:217-226).
- OAuth wait and pending exchange use the same default timeout window via [`cmd/authbridge-sidecar/main.go:592-593`](cmd/authbridge-sidecar/main.go:592-593) and [`pkg/extendedreverseproxy/proxy.go:169-175`](pkg/extendedreverseproxy/proxy.go:169-175).

**Why it remains:**
- Detached flows are still rooted in [`context.Background()`](pkg/extendedreverseproxy/proxy.go:174), so after detach there is no direct cancellation path from the original frontend request.
- If the flow never resumes, the pending exchange can still live until timeout and periodic cleanup in [`pkg/extendedreverseproxy/proxy.go:291-315`](pkg/extendedreverseproxy/proxy.go:291-315).

**Current impact:**
- Resource retention is bounded rather than permanent, but detached abandoned flows may still consume memory until timeout.

---

**End of Analysis**
