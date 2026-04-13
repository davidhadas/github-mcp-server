# Sidecar Demo Plan with Kind

## Overview

This plan outlines how to create a complete demo of the AuthBridge sidecar architecture using **Kind (Kubernetes in Docker)** for local testing. This will demonstrate the full flow with all components running as Kubernetes pods.

## Architecture Decision

### Option 1: Full Kubernetes (Recommended)
Run ALL components as Kubernetes pods in Kind:
- ✅ AI Agent Pod (with AuthBridge sidecar)
- ✅ Backend Pod
- ✅ MCP Server Pod
- ✅ Frontend (port-forward or NodePort)

**Benefits:**
- True production-like environment
- Tests actual Kubernetes networking
- Validates service discovery
- Tests iptables rules in real K8s
- Complete end-to-end validation

### Option 2: Hybrid (Not Recommended)
- AI Agent Pod in Kind
- Backend/MCP Server as local processes
- Complex networking between Kind and host

**Issues:**
- Network complexity (Kind → host)
- Not representative of production
- Harder to debug

**Decision: Go with Option 1 (Full Kubernetes)**

## Demo Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Kind Cluster (Local)                      │
├─────────────────────────────────────────────────────────────┤
│                                                               │
│  ┌──────────────┐                                            │
│  │   Frontend   │ (Port-forward or NodePort)                │
│  │   (Browser)  │                                            │
│  └──────┬───────┘                                            │
│         │                                                     │
│         ▼                                                     │
│  ┌──────────────┐                                            │
│  │  Backend Pod │                                            │
│  │    :8187     │                                            │
│  └──────┬───────┘                                            │
│         │                                                     │
│         ▼                                                     │
│  ┌─────────────────────────────────────────────────────┐    │
│  │              AI Agent Pod                            │    │
│  │  ┌─────────────────────────────────────────────┐    │    │
│  │  │  AI Agent    AuthBridge Sidecar             │    │    │
│  │  │  :8186       :15001 (no endpoints)          │    │    │
│  │  └─────────────────────────────────────────────┘    │    │
│  └─────────────────────────────────────────────────────┘    │
│         │                                                     │
│         ▼                                                     │
│  ┌──────────────┐                                            │
│  │ MCP Server   │                                            │
│  │    Pod       │                                            │
│  │    :8082     │                                            │
│  └──────────────┘                                            │
│                                                               │
└─────────────────────────────────────────────────────────────┘
```

## Implementation Plan

### Phase 1: Setup Kind Cluster

#### 1.1 Install Kind
```bash
# macOS
brew install kind

# Linux
curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.20.0/kind-linux-amd64
chmod +x ./kind
sudo mv ./kind /usr/local/bin/kind
```

#### 1.2 Create Kind Configuration
**File:** `kind-config.yaml`

```yaml
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
name: kagenti-demo
nodes:
- role: control-plane
  extraPortMappings:
  # Backend port
  - containerPort: 30187
    hostPort: 8187
    protocol: TCP
  # AI Agent port (for direct testing)
  - containerPort: 30186
    hostPort: 8186
    protocol: TCP
  # MCP Server port (for direct testing)
  - containerPort: 30184
    hostPort: 8184
    protocol: TCP
```

#### 1.3 Create Cluster
```bash
kind create cluster --config kind-config.yaml
```

### Phase 2: Create All Dockerfiles

We need Dockerfiles for all components:

#### 2.1 Backend Dockerfile
**File:** `Dockerfile.backend`

```dockerfile
FROM golang:1.23.8-alpine AS build
WORKDIR /build
RUN apk add --no-cache git
COPY . .
RUN CGO_ENABLED=0 go build -o /bin/backend ./cmd/backend

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /bin/backend .
EXPOSE 8187
ENTRYPOINT ["/app/backend"]
```

#### 2.2 MCP Server Dockerfile
Already exists: `Dockerfile` (builds github-mcp-server)

#### 2.3 AI Agent & Sidecar Dockerfiles
Already created:
- `Dockerfile.aiagent`
- `Dockerfile.authbridge-sidecar`

### Phase 3: Build and Load Images into Kind

#### 3.1 Build Script
**File:** `build-and-load-kind.sh`

```bash
#!/bin/bash
set -e

echo "Building images for Kind demo..."

# Build all images
docker build -f Dockerfile.backend -t kagenti/backend:demo .
docker build -f Dockerfile.aiagent -t kagenti/aiagent:demo .
docker build -f Dockerfile.authbridge-sidecar -t kagenti/authbridge-sidecar:demo .
docker build -f Dockerfile -t kagenti/mcp-server:demo .

echo "Loading images into Kind cluster..."

# Load images into Kind
kind load docker-image kagenti/backend:demo --name kagenti-demo
kind load docker-image kagenti/aiagent:demo --name kagenti-demo
kind load docker-image kagenti/authbridge-sidecar:demo --name kagenti-demo
kind load docker-image kagenti/mcp-server:demo --name kagenti-demo

echo "Images loaded successfully!"
```

### Phase 4: Create Kubernetes Manifests

#### 4.1 Namespace and ConfigMaps
**File:** `k8s/demo/00-namespace.yaml`

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: kagenti-demo
  labels:
    name: kagenti-demo
```

#### 4.2 MCP Server Deployment
**File:** `k8s/demo/01-mcp-server.yaml`

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-server
  namespace: kagenti-demo
spec:
  replicas: 1
  selector:
    matchLabels:
      app: mcp-server
  template:
    metadata:
      labels:
        app: mcp-server
    spec:
      containers:
      - name: mcp-server
        image: kagenti/mcp-server:demo
        imagePullPolicy: Never  # Use local image
        ports:
        - containerPort: 8082
        env:
        - name: GITHUB_PERSONAL_ACCESS_TOKEN
          valueFrom:
            secretKeyRef:
              name: github-token
              key: token
        args: ["http"]
---
apiVersion: v1
kind: Service
metadata:
  name: mcp-server-service
  namespace: kagenti-demo
spec:
  type: NodePort
  ports:
  - port: 8184
    targetPort: 8082
    nodePort: 30184
  selector:
    app: mcp-server
```

#### 4.3 AI Agent with Sidecar
**File:** `k8s/demo/02-aiagent.yaml`

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: aiagent
  namespace: kagenti-demo
spec:
  replicas: 1
  selector:
    matchLabels:
      app: aiagent
  template:
    metadata:
      labels:
        app: aiagent
    spec:
      initContainers:
      - name: init-iptables
        image: alpine:latest
        securityContext:
          capabilities:
            add:
            - NET_ADMIN
          privileged: true
        command:
        - sh
        - -c
        - |
          apk add --no-cache iptables
          iptables -t nat -A OUTPUT -p tcp -j REDIRECT --to-port 15001
          iptables -t nat -A PREROUTING -p tcp -j REDIRECT --to-port 15001
          iptables -t nat -A OUTPUT -m owner --uid-owner 1337 -j RETURN
          iptables -t nat -A OUTPUT -d 127.0.0.1/32 -p tcp --dport 8186 -j RETURN
          echo "iptables configured"
      
      containers:
      - name: aiagent
        image: kagenti/aiagent:demo
        imagePullPolicy: Never
        ports:
        - containerPort: 8186
        env:
        - name: MCP_SERVER_URL
          value: "http://mcp-server-service:8184"
      
      - name: authbridge-sidecar
        image: kagenti/authbridge-sidecar:demo
        imagePullPolicy: Never
        ports:
        - containerPort: 15001
        env:
        - name: DEFAULT_MCP_SERVER_URL
          value: "http://mcp-server-service:8184"
        securityContext:
          runAsUser: 1337
          runAsGroup: 1337
---
apiVersion: v1
kind: Service
metadata:
  name: aiagent-service
  namespace: kagenti-demo
spec:
  type: NodePort
  ports:
  - port: 8186
    targetPort: 8186
    nodePort: 30186
  selector:
    app: aiagent
```

#### 4.4 Backend Deployment
**File:** `k8s/demo/03-backend.yaml`

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: backend
  namespace: kagenti-demo
spec:
  replicas: 1
  selector:
    matchLabels:
      app: backend
  template:
    metadata:
      labels:
        app: backend
    spec:
      containers:
      - name: backend
        image: kagenti/backend:demo
        imagePullPolicy: Never
        ports:
        - containerPort: 8187
        env:
        - name: AI_AGENT_URL
          value: "http://aiagent-service:8186"
---
apiVersion: v1
kind: Service
metadata:
  name: backend-service
  namespace: kagenti-demo
spec:
  type: NodePort
  ports:
  - port: 8187
    targetPort: 8187
    nodePort: 30187
  selector:
    app: backend
```

#### 4.5 GitHub Token Secret
**File:** `k8s/demo/04-secrets.yaml`

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: github-token
  namespace: kagenti-demo
type: Opaque
stringData:
  token: "${GITHUB_PERSONAL_ACCESS_TOKEN}"
```

### Phase 5: Demo Scripts

#### 5.1 Start Demo Script
**File:** `start-sidecar-demo.sh`

```bash
#!/bin/bash
set -e

echo "=========================================="
echo "Starting Kagenti Sidecar Demo with Kind"
echo "=========================================="

# Check if Kind cluster exists
if ! kind get clusters | grep -q "kagenti-demo"; then
    echo "Creating Kind cluster..."
    kind create cluster --config kind-config.yaml
else
    echo "Kind cluster already exists"
fi

# Build and load images
echo "Building and loading images..."
./build-and-load-kind.sh

# Create GitHub token secret
echo "Creating GitHub token secret..."
export GITHUB_PERSONAL_ACCESS_TOKEN=${GITHUB_PERSONAL_ACCESS_TOKEN:-$(cat .env | grep GITHUB_PERSONAL_ACCESS_TOKEN | cut -d= -f2)}
envsubst < k8s/demo/04-secrets.yaml | kubectl apply -f -

# Deploy all components
echo "Deploying components..."
kubectl apply -f k8s/demo/00-namespace.yaml
kubectl apply -f k8s/demo/01-mcp-server.yaml
kubectl apply -f k8s/demo/02-aiagent.yaml
kubectl apply -f k8s/demo/03-backend.yaml

# Wait for pods to be ready
echo "Waiting for pods to be ready..."
kubectl wait --for=condition=ready pod -l app=mcp-server -n kagenti-demo --timeout=120s
kubectl wait --for=condition=ready pod -l app=aiagent -n kagenti-demo --timeout=120s
kubectl wait --for=condition=ready pod -l app=backend -n kagenti-demo --timeout=120s

echo ""
echo "=========================================="
echo "Demo Started Successfully!"
echo "=========================================="
echo ""
echo "Services available at:"
echo "  Backend:    http://localhost:8187"
echo "  AI Agent:   http://localhost:8186"
echo "  MCP Server: http://localhost:8184"
echo ""
echo "View logs:"
echo "  kubectl logs -n kagenti-demo -l app=aiagent -c aiagent -f"
echo "  kubectl logs -n kagenti-demo -l app=aiagent -c authbridge-sidecar -f"
echo "  kubectl logs -n kagenti-demo -l app=backend -f"
echo ""
echo "Test the flow:"
echo "  curl -X POST http://localhost:8187/task \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -H 'X-User-ID: demo-user' \\"
echo "    -d '{\"user_id\":\"demo-user\",\"task\":\"Get my GitHub profile\"}'"
echo ""
```

#### 5.2 Stop Demo Script
**File:** `stop-sidecar-demo.sh`

```bash
#!/bin/bash
set -e

echo "Stopping Kagenti Sidecar Demo..."

# Delete all resources
kubectl delete namespace kagenti-demo --ignore-not-found=true

# Optionally delete Kind cluster
read -p "Delete Kind cluster? (y/N) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    kind delete cluster --name kagenti-demo
    echo "Kind cluster deleted"
else
    echo "Kind cluster kept (you can delete it later with: kind delete cluster --name kagenti-demo)"
fi

echo "Demo stopped"
```

#### 5.3 Test Script
**File:** `test-sidecar-demo.sh`

```bash
#!/bin/bash
set -e

echo "Testing Kagenti Sidecar Demo..."
echo ""

# Test 1: Health checks
echo "Test 1: Health checks"
echo "  Backend health..."
curl -s http://localhost:8187/health || echo "FAILED"
echo "  AI Agent health..."
curl -s http://localhost:8186/health || echo "FAILED"
echo "  MCP Server health..."
curl -s http://localhost:8184/health || echo "FAILED"
echo ""

# Test 2: Task without OAuth (should trigger OAuth)
echo "Test 2: Task without OAuth (should return 401 with OAuth URL)"
curl -X POST http://localhost:8187/task \
  -H 'Content-Type: application/json' \
  -H 'X-User-ID: demo-user' \
  -d '{"user_id":"demo-user","task":"Get my GitHub profile"}' \
  -v
echo ""

# Test 3: Check sidecar logs
echo "Test 3: Check sidecar logs for OAuth discovery"
kubectl logs -n kagenti-demo -l app=aiagent -c authbridge-sidecar --tail=20
echo ""

echo "Manual test required:"
echo "1. Copy the auth_url from the response above"
echo "2. Open it in browser and authorize"
echo "3. Get the code from the callback"
echo "4. Retry the task with code:"
echo ""
echo "curl -X POST 'http://localhost:8187/task?code=CODE&code_verifier=VERIFIER' \\"
echo "  -H 'Content-Type: application/json' \\"
echo "  -H 'X-User-ID: demo-user' \\"
echo "  -d '{\"user_id\":\"demo-user\",\"task\":\"Get my GitHub profile\"}'"
```

### Phase 6: Frontend Updates

The frontend (HTML file) needs to be updated to:
1. Use the Kind cluster endpoints (localhost:8187)
2. Handle OAuth flow with code in query parameters
3. Retry task with code after OAuth

**File:** `oauth-demo-sidecar.html`

Key changes:
- Backend URL: `http://localhost:8187`
- After OAuth callback, retry task with `?code=...&code_verifier=...`

### Phase 7: Documentation

#### 7.1 Demo README
**File:** `DEMO_SIDECAR.md`

Complete guide for running the demo:
- Prerequisites
- Setup instructions
- Testing steps
- Troubleshooting
- Architecture explanation

## Testing Strategy

### Local Testing Phases

1. **Phase 1: Component Testing**
   - Test each pod individually
   - Verify health endpoints
   - Check logs

2. **Phase 2: Integration Testing**
   - Test Backend → AI Agent communication
   - Test AI Agent → MCP Server communication
   - Verify sidecar intercepts traffic

3. **Phase 3: OAuth Flow Testing**
   - Trigger OAuth discovery
   - Complete OAuth in browser
   - Retry with code
   - Verify token caching

4. **Phase 4: End-to-End Testing**
   - Full flow from frontend
   - Multiple requests
   - Token reuse
   - Error scenarios

## Benefits of Kind-based Demo

1. **Production-like**: Real Kubernetes environment
2. **Isolated**: Runs in Docker, no cluster needed
3. **Fast**: Quick setup and teardown
4. **Reproducible**: Same environment every time
5. **Complete**: Tests all components together
6. **Debuggable**: Easy to inspect pods and logs

## Timeline

- **Day 1**: Create Dockerfiles and Kind config
- **Day 2**: Create K8s manifests and scripts
- **Day 3**: Test and debug
- **Day 4**: Documentation and polish

## Success Criteria

- [ ] Kind cluster starts successfully
- [ ] All pods deploy and become ready
- [ ] Health checks pass
- [ ] Backend can reach AI Agent
- [ ] AI Agent can reach MCP Server
- [ ] Sidecar intercepts traffic (verify in logs)
- [ ] OAuth discovery works
- [ ] Token exchange works
- [ ] Token caching works
- [ ] Full end-to-end flow succeeds

## Next Steps

1. Create Backend Dockerfile
2. Create Kind configuration
3. Create K8s manifests for demo
4. Create demo scripts
5. Update frontend for Kind endpoints
6. Test complete flow
7. Document demo usage

---

**Status:** Plan Complete, Ready for Implementation  
**Branch:** authbridge_sidecar  
**Date:** 2026-04-11