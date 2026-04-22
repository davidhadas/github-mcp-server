# End-to-End Testing Guide

This guide provides comprehensive testing procedures for the Envoy-based Kagenti MCP OAuth system.

## Prerequisites

- Kubernetes cluster (kind, minikube, or cloud)
- kubectl configured
- GitHub OAuth App configured
- Docker (for building images)

## Test Phases

### Phase 1: Component Health Checks

Verify each component is running and healthy.

```bash
# Check all pods are running
kubectl get pods -n kagenti-envoy-demo

# Expected output:
# NAME                            READY   STATUS    RESTARTS   AGE
# token-broker-xxxxxxxxxx-xxxxx   1/1     Running   0          5m
# mcp-server-xxxxxxxxxx-xxxxx     1/1     Running   0          5m
# aiagent-xxxxxxxxxx-xxxxx        2/2     Running   0          5m
# backend-xxxxxxxxxx-xxxxx        1/1     Running   0          5m

# Check services
kubectl get svc -n kagenti-envoy-demo

# Test Token Broker health
kubectl port-forward -n kagenti-envoy-demo svc/token-broker-service 8190:8190 &
curl http://localhost:8190/health
# Expected: OK

# Test MCP Server health
kubectl port-forward -n kagenti-envoy-demo svc/mcp-server-service 8184:8184 &
curl http://localhost:8184/health
# Expected: OK (or 404 if no health endpoint)

# Test Backend health
kubectl port-forward -n kagenti-envoy-demo svc/backend-service 8187:8187 &
curl http://localhost:8187/health
# Expected: OK

# Test Envoy admin interface
kubectl port-forward -n kagenti-envoy-demo -l app=aiagent 15000:15000 &
curl http://localhost:15000/stats | grep ext_authz
# Should show ext_authz statistics
```

### Phase 2: Token Broker Session Management

Test Token Broker session creation and management.

```bash
# Port-forward Token Broker
kubectl port-forward -n kagenti-envoy-demo svc/token-broker-service 8190:8190

# Create a session
SESSION_RESPONSE=$(curl -s -X POST http://localhost:8190/sessions \
  -H "X-User-ID: test-user")
echo $SESSION_RESPONSE

# Extract session key
SESSION_KEY=$(echo $SESSION_RESPONSE | jq -r '.oauth_session_key')
echo "Session Key: $SESSION_KEY"

# Start event polling in background (will block)
curl -X POST "http://localhost:8190/sessions/$SESSION_KEY/events" \
  -H "X-User-ID: test-user" &
EVENT_PID=$!

# Request a token (will trigger OAuth discovery)
curl -X POST "http://localhost:8190/sessions/$SESSION_KEY/token" \
  -H "X-User-ID: test-user" \
  -H "X-Mcp-Server-Url: http://mcp-server-service:8184" \
  -v

# This should:
# 1. Send synthetic MCP request to MCP server
# 2. Receive 401 Unauthorized
# 3. Discover OAuth server
# 4. Get authorization URL from MCP server
# 5. Send oauth_required event (check EVENT_PID output)
# 6. Block waiting for OAuth completion

# Kill event poller
kill $EVENT_PID

# End session
curl -X POST "http://localhost:8190/sessions/$SESSION_KEY/end" \
  -H "X-User-ID: test-user"
```

### Phase 3: Envoy Transparent Proxy

Test Envoy's transparent proxying without OAuth.

```bash
# Port-forward to AI Agent via Envoy
kubectl port-forward -n kagenti-envoy-demo svc/aiagent-service 8186:8186

# Test inbound traffic (Backend → Envoy → Agent)
curl -X POST http://localhost:8186/task \
  -H "Content-Type: application/json" \
  -d '{"user_id":"test-user","task":"test"}' \
  -v

# Check Envoy stats
kubectl port-forward -n kagenti-envoy-demo -l app=aiagent 15000:15000
curl http://localhost:15000/stats | grep -E "(inbound|outbound)"

# Check Envoy config
curl http://localhost:15000/config_dump | jq '.configs[2].dynamic_listeners'
```

### Phase 4: Backend Session and SSE

Test Backend session management and Server-Sent Events.

```bash
# Port-forward Backend
kubectl port-forward -n kagenti-envoy-demo svc/backend-service 8187:8187

# Test health
curl http://localhost:8187/health
# Expected: OK

# Start SSE connection in one terminal
curl -N http://localhost:8187/events?user_id=test-user

# In another terminal, submit a task
curl -X POST http://localhost:8187/task \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "test-user",
    "task": "List my GitHub repositories",
    "agent_url": "http://aiagent-service:8186/task"
  }' \
  -v

# The SSE connection should receive an oauth_required event
# The task request will block until OAuth is completed
```

### Phase 5: Complete OAuth Flow

Test the complete end-to-end OAuth flow.

#### Setup

1. Configure GitHub OAuth App:
   - Authorization callback URL: `http://localhost:8187/oauth/callback`
   - Note your Client ID and Client Secret

2. Update MCP Server deployment with OAuth credentials:
```bash
kubectl edit deployment mcp-server -n kagenti-envoy-demo
# Update OAUTH_CLIENT_ID and OAUTH_CLIENT_SECRET
```

3. Port-forward Backend:
```bash
kubectl port-forward -n kagenti-envoy-demo svc/backend-service 8187:8187
```

#### Test Flow

1. **Open Demo Page**
   ```
   http://localhost:8187/demo
   ```

2. **Submit Task**
   - User ID: `test-user`
   - Task: `List my GitHub repositories`
   - Click "Submit Task"

3. **Observe OAuth Flow**
   - Browser opens SSE connection
   - Backend creates session with Token Broker
   - Backend forwards request to Agent
   - Agent sends MCP request
   - Envoy intercepts, calls Token Broker ext_authz
   - Token Broker discovers OAuth needed
   - Token Broker sends oauth_required event
   - Backend streams event to browser via SSE
   - Browser shows OAuth popup

4. **Complete OAuth**
   - Click "Authorize with GitHub"
   - Browser redirects to GitHub
   - User authorizes
   - GitHub redirects back to Backend callback
   - Backend sends code to Token Broker
   - Token Broker exchanges code for token
   - Token Broker caches token
   - Token Broker returns token to Envoy
   - Envoy injects Authorization header
   - MCP server processes request
   - Response flows back to browser

5. **Verify Result**
   - Browser displays task result
   - Check logs for complete flow

#### Verify Logs

```bash
# Token Broker logs
kubectl logs -n kagenti-envoy-demo -l app=token-broker -f

# Envoy logs
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c envoy -f

# AI Agent logs
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c aiagent -f

# Backend logs
kubectl logs -n kagenti-envoy-demo -l app=backend -f

# MCP Server logs
kubectl logs -n kagenti-envoy-demo -l app=mcp-server -f
```

### Phase 6: Token Caching

Test that tokens are cached and reused.

1. **Submit First Task** (triggers OAuth)
   - Complete OAuth flow as above
   - Note the time taken

2. **Submit Second Task** (uses cached token)
   - Submit another task immediately
   - Should complete much faster (no OAuth)
   - Check Token Broker logs for cache hit

3. **Verify Cache**
   ```bash
   # Check Token Broker logs
   kubectl logs -n kagenti-envoy-demo -l app=token-broker | grep "Token found in cache"
   ```

### Phase 7: Session Timeout

Test session timeout behavior.

```bash
# Create session
curl -X POST http://localhost:8190/sessions \
  -H "X-User-ID: test-user"

# Extract session key
SESSION_KEY="..."

# Start event polling
curl -X POST "http://localhost:8190/sessions/$SESSION_KEY/events" \
  -H "X-User-ID: test-user" &

# Wait for timeout (60 seconds by default)
sleep 65

# Try to use session (should fail)
curl -X POST "http://localhost:8190/sessions/$SESSION_KEY/token" \
  -H "X-User-ID: test-user" \
  -H "X-Mcp-Server-Url: http://mcp-server-service:8184"
# Expected: 401 Unauthorized
```

### Phase 8: Error Handling

Test various error scenarios.

#### Invalid Session
```bash
curl -X POST "http://localhost:8190/sessions/invalid-key/token" \
  -H "X-User-ID: test-user" \
  -H "X-Mcp-Server-Url: http://mcp-server-service:8184"
# Expected: 401 Unauthorized
```

#### User Mismatch
```bash
SESSION_KEY="..."  # Valid session for test-user
curl -X POST "http://localhost:8190/sessions/$SESSION_KEY/token" \
  -H "X-User-ID: different-user" \
  -H "X-Mcp-Server-Url: http://mcp-server-service:8184"
# Expected: 401 Unauthorized
```

#### MCP Server Unreachable
```bash
curl -X POST "http://localhost:8190/sessions/$SESSION_KEY/token" \
  -H "X-User-ID: test-user" \
  -H "X-Mcp-Server-Url: http://nonexistent-server:8184"
# Expected: 503 Service Unavailable
```

#### OAuth Timeout
```bash
# Start token request
curl -X POST "http://localhost:8190/sessions/$SESSION_KEY/token" \
  -H "X-User-ID: test-user" \
  -H "X-Mcp-Server-Url: http://mcp-server-service:8184" &

# Don't complete OAuth, wait for timeout (300s)
# Expected: 408 Request Timeout
```

## Performance Testing

### Load Testing

Use `hey` or `ab` for load testing:

```bash
# Install hey
go install github.com/rakyll/hey@latest

# Load test Token Broker health endpoint
hey -n 1000 -c 10 http://localhost:8190/health

# Load test Backend health endpoint
hey -n 1000 -c 10 http://localhost:8187/health
```

### Concurrent Sessions

Test multiple concurrent sessions:

```bash
# Create 10 sessions concurrently
for i in {1..10}; do
  curl -X POST http://localhost:8190/sessions \
    -H "X-User-ID: user-$i" &
done
wait

# Check Token Broker can handle concurrent sessions
kubectl logs -n kagenti-envoy-demo -l app=token-broker | grep "Session created"
```

## Troubleshooting

### Pods Not Starting

```bash
# Check pod status
kubectl describe pod -n kagenti-envoy-demo <pod-name>

# Check logs
kubectl logs -n kagenti-envoy-demo <pod-name>

# Check events
kubectl get events -n kagenti-envoy-demo --sort-by='.lastTimestamp'
```

### Envoy Configuration Errors

```bash
# Validate Envoy config
kubectl exec -n kagenti-envoy-demo -l app=aiagent -c envoy -- \
  /usr/local/bin/envoy --mode validate -c /etc/envoy/envoy.yaml

# Check Envoy logs
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c envoy
```

### Token Broker Connection Issues

```bash
# Test from within cluster
kubectl run -n kagenti-envoy-demo test-pod --rm -it --image=curlimages/curl -- \
  curl http://token-broker-service:8190/health

# Check Token Broker logs
kubectl logs -n kagenti-envoy-demo -l app=token-broker -f
```

### OAuth Flow Issues

```bash
# Check MCP Server OAuth configuration
kubectl exec -n kagenti-envoy-demo -l app=mcp-server -- env | grep OAUTH

# Test MCP Server OAuth discovery
kubectl run -n kagenti-envoy-demo test-pod --rm -it --image=curlimages/curl -- \
  curl http://mcp-server-service:8184/.well-known/oauth-protected-resource

# Check all logs during OAuth flow
kubectl logs -n kagenti-envoy-demo -l app=token-broker -f &
kubectl logs -n kagenti-envoy-demo -l app=backend -f &
kubectl logs -n kagenti-envoy-demo -l app=mcp-server -f &
```

## Success Criteria

All tests should pass with:
- ✅ All pods running and healthy
- ✅ Token Broker creates and manages sessions
- ✅ Envoy transparently proxies traffic
- ✅ Backend streams OAuth events via SSE
- ✅ Complete OAuth flow works end-to-end
- ✅ Tokens are cached and reused
- ✅ Sessions timeout correctly
- ✅ Error handling works as expected
- ✅ System handles concurrent requests
- ✅ Logs show complete flow

## Next Steps

After successful testing:
1. Document any issues found
2. Update configuration as needed
3. Prepare for production deployment
4. Set up monitoring and alerting
5. Create runbooks for operations