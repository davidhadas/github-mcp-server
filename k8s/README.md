# Kubernetes Deployment for AI Agent with AuthBridge Sidecar

This directory contains Kubernetes manifests for deploying the AI Agent with AuthBridge as a sidecar proxy.

## Architecture

The deployment uses the **sidecar pattern** where:
- **AI Agent** runs as the main container
- **AuthBridge** runs as a sidecar proxy with **NO HTTP endpoints**
- All traffic is intercepted via iptables
- OAuth tokens are managed transparently

```
┌─────────────────────────────────────┐
│         AI Agent Pod                │
│  ┌─────────────────────────────┐   │
│  │  AI Agent    AuthBridge     │   │
│  │  Container   Sidecar        │   │
│  │  :8186       :15001         │   │
│  │              (proxy)        │   │
│  └─────────────────────────────┘   │
│         Shared Network              │
└─────────────────────────────────────┘
```

## Files

- `aiagent-deployment.yaml` - Main deployment with AI Agent and AuthBridge sidecar
- `README.md` - This file

## Prerequisites

1. Kubernetes cluster (v1.24+)
2. `kubectl` configured
3. Container images built and pushed:
   - `ghcr.io/kagenti/aiagent:latest`
   - `ghcr.io/kagenti/authbridge-sidecar:latest`

## Building Images

```bash
# Build AI Agent image
docker build -f Dockerfile.aiagent -t ghcr.io/kagenti/aiagent:latest .
docker push ghcr.io/kagenti/aiagent:latest

# Build AuthBridge Sidecar image
docker build -f Dockerfile.authbridge-sidecar -t ghcr.io/kagenti/authbridge-sidecar:latest .
docker push ghcr.io/kagenti/authbridge-sidecar:latest
```

## Deployment

### 1. Create Namespace

```bash
kubectl apply -f aiagent-deployment.yaml
```

This creates:
- `kagenti` namespace
- AI Agent deployment with 3 replicas
- AI Agent service (ClusterIP)

### 2. Verify Deployment

```bash
# Check pods
kubectl get pods -n kagenti

# Check services
kubectl get svc -n kagenti

# Check logs
kubectl logs -n kagenti -l app=aiagent -c aiagent
kubectl logs -n kagenti -l app=aiagent -c authbridge-sidecar
```

### 3. Test the Deployment

```bash
# Port forward to test locally
kubectl port-forward -n kagenti svc/aiagent-service 8186:8186

# Send a test request
curl -X POST http://localhost:8186/task \
  -H "Content-Type: application/json" \
  -H "X-User-ID: test-user" \
  -d '{"user_id":"test-user","task":"Get my GitHub profile"}'
```

## How It Works

### Traffic Flow

**Inbound (Backend → AI Agent):**
1. Request arrives at pod IP:8186
2. iptables redirects to AuthBridge sidecar :15001
3. Sidecar checks for OAuth code in query params
4. If code present: exchange for token, cache it
5. Sidecar forwards to AI Agent on localhost:8186
6. Response flows back through sidecar

**Outbound (AI Agent → MCP Server):**
1. AI Agent makes request to MCP server
2. iptables redirects to AuthBridge sidecar :15001
3. Sidecar checks token cache
4. If no token: return 401 with OAuth URL
5. If token exists: add Authorization header
6. Sidecar forwards to MCP server
7. Response flows back to AI Agent

### OAuth Flow

1. Frontend sends task → Backend → AI Agent pod
2. Sidecar intercepts, no token found
3. Sidecar returns 401 with OAuth URL
4. Frontend opens OAuth URL, user authorizes
5. Frontend gets code and code_verifier
6. Frontend retries task with `?code=...&code_verifier=...`
7. Sidecar intercepts, exchanges code for token
8. Sidecar caches token, forwards clean request
9. Task succeeds

### iptables Rules

The init container sets up these rules:

```bash
# Redirect all outbound traffic to sidecar
iptables -t nat -A OUTPUT -p tcp -j REDIRECT --to-port 15001

# Redirect all inbound traffic to sidecar
iptables -t nat -A PREROUTING -p tcp -j REDIRECT --to-port 15001

# Exclude sidecar's own traffic (UID 1337)
iptables -t nat -A OUTPUT -m owner --uid-owner 1337 -j RETURN

# Exclude localhost traffic to AI Agent
iptables -t nat -A OUTPUT -d 127.0.0.1/32 -p tcp --dport 8186 -j RETURN
```

## Scaling

Scale the deployment:

```bash
kubectl scale deployment aiagent -n kagenti --replicas=5
```

Each pod has its own:
- AI Agent instance
- AuthBridge sidecar instance
- Token cache (not shared between pods)

## Monitoring

### Logs

```bash
# AI Agent logs
kubectl logs -n kagenti -l app=aiagent -c aiagent -f

# AuthBridge sidecar logs
kubectl logs -n kagenti -l app=aiagent -c authbridge-sidecar -f

# All logs from a specific pod
kubectl logs -n kagenti <pod-name> --all-containers -f
```

### Metrics

```bash
# Pod resource usage
kubectl top pods -n kagenti

# Describe pod for events
kubectl describe pod -n kagenti <pod-name>
```

## Troubleshooting

### Pod Not Starting

```bash
# Check pod status
kubectl get pods -n kagenti

# Check events
kubectl describe pod -n kagenti <pod-name>

# Check init container logs
kubectl logs -n kagenti <pod-name> -c init-iptables
```

### iptables Issues

```bash
# Exec into pod to check iptables
kubectl exec -n kagenti <pod-name> -c authbridge-sidecar -- iptables -t nat -L -n -v
```

### Traffic Not Being Intercepted

1. Check iptables rules are set up correctly
2. Verify sidecar is running (UID 1337)
3. Check sidecar logs for connection attempts
4. Verify AI Agent is making requests with X-User-ID header

### OAuth Not Working

1. Check sidecar logs for OAuth discovery attempts
2. Verify MCP server is accessible from pod
3. Check that code and code_verifier are in query params
4. Verify token exchange endpoint is working

## Security Considerations

1. **Network Isolation**: AuthBridge sidecar not exposed externally
2. **Token Management**: Tokens cached per pod, not persistent
3. **RBAC**: Minimal pod permissions required
4. **Privileged Init**: Only init container needs NET_ADMIN capability

## Cleanup

```bash
# Delete deployment
kubectl delete -f aiagent-deployment.yaml

# Or delete namespace (removes everything)
kubectl delete namespace kagenti
```

## References

- [Kubernetes Sidecar Pattern](https://kubernetes.io/docs/concepts/workloads/pods/sidecar-containers/)
- [Architecture Documentation](../docs/authbridge-sidecar-architecture.md)
- [MCP Protocol](https://modelcontextprotocol.io/)

---

**Made with Bob**