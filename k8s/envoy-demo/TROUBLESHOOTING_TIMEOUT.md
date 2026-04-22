# Troubleshooting Timeout Errors in Envoy Demo

## Common Timeout Error

When starting a GitHub info task, you may encounter:
```
Error: Unexpected token 'u', "upstream r"... is not valid JSON
```

This error occurs when the backend receives a non-JSON error response (like "upstream request timeout") and tries to parse it as JSON.

## Root Causes

### 1. Backend Not Handling Non-JSON Errors

**Fixed in**: `internal/backend/envoy/handlers.go`

The backend now:
- Detects non-JSON error responses
- Wraps them in proper JSON format
- Logs detailed error information

### 2. AuthBridge Logging Not Visible

**Fixed in**: `k8s/envoy-demo/04-aiagent.yaml`

Added environment variables to authbridge container:
```yaml
env:
  - name: LOG_LEVEL
    value: "info"
  - name: LOG_FORMAT
    value: "json"
```

### 3. Actual Timeout Issues

The timeout can occur at several points in the OAuth flow:

#### A. Token Broker Session Timeout
- **Default**: 60s (configured in `01-token-broker.yaml`)
- **Symptom**: Session expires before OAuth completes
- **Solution**: Increase `TOKEN_BROKER_SESSION_TIMEOUT`

#### B. Token Wait Timeout
- **Default**: 300s (5 minutes)
- **Symptom**: Waiting for OAuth token times out
- **Solution**: Increase `TOKEN_BROKER_TOKEN_WAIT_TIMEOUT`

#### C. Envoy ext_proc Timeout
- **Default**: 320s (configured in `03-envoy-config.yaml`)
- **Symptom**: Envoy times out waiting for AuthBridge
- **Solution**: Increase timeout in Envoy config

#### D. Backend Request Timeout
- **Default**: 300s (configured in `cmd/backend-envoy/main.go`)
- **Symptom**: Backend times out waiting for Agent response
- **Solution**: Already set to 300s, should be sufficient

## Debugging Steps

### 1. Check All Component Logs

```bash
# Terminal 1: Token Broker
kubectl logs -n kagenti-envoy-demo -l app=token-broker -f

# Terminal 2: Backend
kubectl logs -n kagenti-envoy-demo -l app=backend -f

# Terminal 3: AI Agent
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c aiagent -f

# Terminal 4: AuthBridge (NEW - now visible!)
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c authbridge -f

# Terminal 5: MCP Server
kubectl logs -n kagenti-envoy-demo -l app=mcp-server -f
```

### 2. Check Envoy Statistics

```bash
# Port-forward Envoy admin
kubectl port-forward -n kagenti-envoy-demo -l app=aiagent 9901:9901

# Check ext_proc stats
curl http://localhost:9901/stats | grep ext_proc

# Check upstream timeouts
curl http://localhost:9901/stats | grep timeout

# Check AuthBridge cluster health
curl http://localhost:9901/clusters | grep authbridge
```

### 3. Trace a Request End-to-End

Submit a task and watch the logs in order:

1. **Backend** receives `/task` request
2. **Backend** creates session with Token Broker
3. **Backend** forwards to AI Agent with session key
4. **AI Agent** receives task
5. **Envoy** intercepts outbound MCP request
6. **AuthBridge** (ext_proc) processes request
7. **AuthBridge** calls Token Broker for token
8. **Token Broker** initiates OAuth flow
9. **Backend** receives OAuth event
10. **User** completes OAuth in browser
11. **Backend** sends OAuth code to Token Broker
12. **Token Broker** exchanges code for token
13. **AuthBridge** receives token
14. **Envoy** injects token and forwards to MCP Server
15. **MCP Server** responds with data
16. **AI Agent** processes response
17. **Backend** returns result to browser

### 4. Check Timeout Configuration

```bash
# Token Broker timeouts
kubectl get deployment token-broker -n kagenti-envoy-demo -o yaml | grep -A 5 TIMEOUT

# Envoy timeouts
kubectl get configmap envoy-config -n kagenti-envoy-demo -o yaml | grep timeout

# Backend timeouts
kubectl get deployment backend -n kagenti-envoy-demo -o yaml | grep -i timeout
```

## Solutions

### Quick Fix: Increase All Timeouts

```bash
# Increase Token Broker session timeout
kubectl set env deployment/token-broker \
  TOKEN_BROKER_SESSION_TIMEOUT=120s \
  -n kagenti-envoy-demo

# Increase Token Broker token wait timeout
kubectl set env deployment/token-broker \
  TOKEN_BROKER_TOKEN_WAIT_TIMEOUT=600s \
  -n kagenti-envoy-demo

# Restart deployments
kubectl rollout restart deployment/token-broker -n kagenti-envoy-demo
kubectl rollout restart deployment/backend -n kagenti-envoy-demo
kubectl rollout restart deployment/aiagent -n kagenti-envoy-demo
```

### Permanent Fix: Update Manifests

Edit `k8s/envoy-demo/01-token-broker.yaml`:
```yaml
env:
  - name: TOKEN_BROKER_SESSION_TIMEOUT
    value: "120s"  # Increased from 60s
  - name: TOKEN_BROKER_TOKEN_WAIT_TIMEOUT
    value: "600s"  # Increased from 300s
```

Edit `k8s/envoy-demo/03-envoy-config.yaml`:
```yaml
grpc_service:
  envoy_grpc:
    cluster_name: authbridge_cluster
  timeout: 620s  # Increased from 320s (must be > token wait timeout)
```

Then redeploy:
```bash
kubectl apply -f k8s/envoy-demo/01-token-broker.yaml
kubectl apply -f k8s/envoy-demo/03-envoy-config.yaml
kubectl rollout restart deployment/aiagent -n kagenti-envoy-demo
```

## Verification

After applying fixes:

1. **Check logs are visible**:
   ```bash
   kubectl logs -n kagenti-envoy-demo -l app=aiagent -c authbridge --tail=20
   ```
   Should see JSON-formatted logs

2. **Test error handling**:
   Submit a task and check backend logs show proper JSON errors

3. **Test OAuth flow**:
   Submit a task requiring OAuth and verify:
   - Session created
   - OAuth URL returned
   - Token exchange completes
   - Request succeeds

## Expected Log Flow

### Successful OAuth Flow

```
[Backend] Task received: user_id=demo-user
[Backend] Session created: session_key=abc123
[Backend] Forwarding to Agent
[AI Agent] Task received
[Envoy] Intercepting outbound request to mcp-server
[AuthBridge] Processing request, no token found
[AuthBridge] Calling Token Broker for token
[Token Broker] No token, initiating OAuth
[Token Broker] Returning OAuth URL
[AuthBridge] Returning 401 with OAuth URL
[Backend] Received 401, sending OAuth event to browser
[Browser] User completes OAuth
[Backend] OAuth callback received
[Backend] Sending code to Token Broker
[Token Broker] Exchanging code for token
[Token Broker] Token cached
[Token Broker] Returning token to AuthBridge
[AuthBridge] Token received, injecting into request
[Envoy] Forwarding request with token
[MCP Server] Request received with valid token
[MCP Server] Returning data
[AI Agent] Processing response
[Backend] Returning result to browser
```

### Timeout Scenario

```
[Backend] Task received
[Backend] Session created
[Backend] Forwarding to Agent
[AI Agent] Task received
[Envoy] Intercepting outbound request
[AuthBridge] Processing request, no token found
[AuthBridge] Calling Token Broker for token
[Token Broker] No token, initiating OAuth
[Token Broker] Returning OAuth URL
[AuthBridge] Returning 401 with OAuth URL
[Backend] Received 401, sending OAuth event to browser
... (user takes too long) ...
[Token Broker] Session timeout after 60s
[AuthBridge] Token request timeout after 300s
[Envoy] ext_proc timeout after 320s
[Backend] Request timeout, returning error
```

## Prevention

1. **Set appropriate timeouts** based on expected OAuth completion time
2. **Monitor timeout metrics** in Envoy and Token Broker
3. **Implement retry logic** in the frontend for timeout errors
4. **Add user feedback** showing OAuth is in progress
5. **Log all timeout events** for debugging

## Related Files

- `internal/backend/envoy/handlers.go` - Backend error handling
- `k8s/envoy-demo/04-aiagent.yaml` - AuthBridge logging config
- `k8s/envoy-demo/01-token-broker.yaml` - Token Broker timeouts
- `k8s/envoy-demo/03-envoy-config.yaml` - Envoy timeouts
- `cmd/backend-envoy/main.go` - Backend timeouts

## Made with Bob