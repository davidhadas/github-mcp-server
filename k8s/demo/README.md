# Kagenti Sidecar Demo - Kubernetes Deployment

This directory contains Kubernetes manifests for deploying the Kagenti architecture with AuthBridge as a sidecar container.

## Architecture

```
Browser → Backend (8187) → AI Agent Pod → MCP Server (8184)
                              ├─ AI Agent (8186)
                              └─ AuthBridge Sidecar (15001, transparent proxy)
```

### Key Features

- **Transparent TCP Proxy**: AuthBridge sidecar intercepts all traffic using iptables
- **OAuth Token Management**: Sidecar handles OAuth discovery and token caching
- **No Code Changes**: AI Agent sends requests directly to MCP server
- **Per-User Tokens**: Tokens cached per (user_id, mcp_server_url) within each pod

## Prerequisites

1. **Kind (Kubernetes in Docker)** or any Kubernetes cluster
2. **kubectl** configured to access your cluster
3. **Docker or Podman** for building images
4. **GitHub OAuth App** for authentication

## Setup OAuth Credentials

### Step 1: Create GitHub OAuth App

1. Go to https://github.com/settings/developers
2. Click "New OAuth App"
3. Fill in the details:
   - **Application name**: Kagenti Demo (or any name)
   - **Homepage URL**: http://localhost:8187
   - **Authorization callback URL**: `http://localhost:8187/oauth/callback`
4. Click "Register application"
5. Copy the **Client ID**
6. Click "Generate a new client secret" and copy the **Client Secret**

### Step 2: Configure Credentials

You have two options:

#### Option A: Use Environment Variables (Recommended)

1. Create a `.env` file in the project root:
   ```bash
   cp .env.example .env
   ```

2. Edit `.env` and add your credentials:
   ```bash
   GITHUB_OAUTH_CLIENT_ID=your_client_id_here
   GITHUB_OAUTH_CLIENT_SECRET=your_client_secret_here
   ```

3. The deployment script will automatically create a Kubernetes Secret from these values

#### Option B: Edit the Secret Manifest

1. Edit `k8s/demo/00-oauth-secret.yaml`
2. Replace the placeholder values with your actual credentials:
   ```yaml
   stringData:
     client-id: "your_actual_client_id"
     client-secret: "your_actual_client_secret"
   ```

## Deployment

### Quick Start

```bash
# Start the demo (creates cluster, builds images, deploys)
./start-sidecar-demo.sh
```

### Manual Deployment

```bash
# 1. Create Kind cluster
kind create cluster --config kind-config.yaml

# 2. Build and load images
./build-and-load-kind.sh

# 3. Deploy components
kubectl apply -f k8s/demo/00-namespace.yaml

# 4. Create OAuth Secret (if using .env)
source .env
kubectl create secret generic mcp-oauth-credentials -n kagenti-demo \
  --from-literal=client-id="$GITHUB_OAUTH_CLIENT_ID" \
  --from-literal=client-secret="$GITHUB_OAUTH_CLIENT_SECRET"

# Or apply the manifest (if edited)
kubectl apply -f k8s/demo/00-oauth-secret.yaml

# 5. Deploy other components
kubectl apply -f k8s/demo/02a-aiagent-config.yaml
kubectl apply -f k8s/demo/03a-backend-config.yaml
kubectl apply -f k8s/demo/01-mcp-server.yaml
kubectl apply -f k8s/demo/02-aiagent.yaml
kubectl apply -f k8s/demo/03-backend.yaml

# 6. Wait for pods to be ready
kubectl wait --for=condition=ready pod -l app=mcp-server -n kagenti-demo --timeout=120s
kubectl wait --for=condition=ready pod -l app=aiagent -n kagenti-demo --timeout=120s
kubectl wait --for=condition=ready pod -l app=backend -n kagenti-demo --timeout=120s
```

## Testing

### Test OAuth Flow

```bash
# Send a task request (will trigger OAuth if no token)
curl -X POST http://localhost:8187/task \
  -H 'Content-Type: application/json' \
  -H 'X-User-ID: demo-user' \
  -d '{"user_id":"demo-user","task":"Get my GitHub profile"}'
```

Expected response (first time):
```json
{
  "status": "oauth_required",
  "auth_url": "https://github.com/login/oauth/authorize?client_id=...",
  "code_verifier": "...",
  "mcp_server_url": "http://mcp-server-service:8184"
}
```

### Complete OAuth Flow

1. Copy the `auth_url` from the response
2. Open it in a browser and authorize the app
3. GitHub will redirect to `http://localhost:8187/oauth/callback?code=...`
4. Copy the authorization code from the URL
5. Retry the request with the code:
   ```bash
   curl -X POST http://localhost:8187/task \
     -H 'Content-Type: application/json' \
     -H 'X-User-ID: demo-user' \
     -d '{
       "user_id":"demo-user",
       "task":"Get my GitHub profile",
       "oauth_code":"PASTE_CODE_HERE",
       "code_verifier":"PASTE_VERIFIER_HERE",
       "mcp_server_url":"http://mcp-server-service:8184"
     }'
   ```

### View Logs

```bash
# AI Agent logs
kubectl logs -n kagenti-demo -l app=aiagent -c aiagent -f

# AuthBridge Sidecar logs
kubectl logs -n kagenti-demo -l app=aiagent -c authbridge-sidecar -f

# Backend logs
kubectl logs -n kagenti-demo -l app=backend -f

# MCP Server logs
kubectl logs -n kagenti-demo -l app=mcp-server -f
```

### Check Pod Status

```bash
kubectl get pods -n kagenti-demo
kubectl describe pod -n kagenti-demo -l app=aiagent
```

## Troubleshooting

### OAuth Discovery Returns Empty Values

**Symptom**: `auth_url` and `code_verifier` are empty in the response

**Cause**: MCP server doesn't have OAuth credentials configured

**Solution**: 
1. Check if the Secret exists:
   ```bash
   kubectl get secret mcp-oauth-credentials -n kagenti-demo
   ```
2. Verify the Secret has the correct keys:
   ```bash
   kubectl get secret mcp-oauth-credentials -n kagenti-demo -o yaml
   ```
3. Check MCP server logs for OAuth configuration:
   ```bash
   kubectl logs -n kagenti-demo -l app=mcp-server
   ```

### Pods Not Starting

**Symptom**: Pods stuck in `ImagePullBackOff` or `ErrImageNeverPull`

**Solution**:
1. Rebuild and reload images:
   ```bash
   ./build-and-load-kind.sh
   ```
2. Delete and recreate pods:
   ```bash
   kubectl delete pod -n kagenti-demo --all
   ```

### Sidecar Not Intercepting Traffic

**Symptom**: Requests bypass the sidecar

**Solution**:
1. Check iptables rules in the init container logs:
   ```bash
   kubectl logs -n kagenti-demo -l app=aiagent -c init-iptables
   ```
2. Verify sidecar is running:
   ```bash
   kubectl get pod -n kagenti-demo -l app=aiagent -o jsonpath='{.items[0].status.containerStatuses[*].name}'
   ```

## Cleanup

```bash
# Delete all resources
kubectl delete namespace kagenti-demo

# Delete Kind cluster
kind delete cluster --name kagenti-demo
```

## Architecture Details

### Traffic Flow

1. **Frontend → Backend**: User sends task request with `X-User-ID` header
2. **Backend → AI Agent**: Forwards request to AI Agent pod
3. **AI Agent → Sidecar (iptables)**: All outbound traffic intercepted by iptables rules
4. **Sidecar → MCP Server**: Sidecar forwards request, adds `X-User-ID` header
5. **MCP Server → Sidecar**: Returns 401 if no token
6. **Sidecar**: Intercepts 401, triggers OAuth discovery, returns auth URL to client
7. **Client**: Completes OAuth, retries with code
8. **Sidecar**: Exchanges code for token, caches it, forwards original request with token

### iptables Rules

The init container sets up these rules:

```bash
# Exclude sidecar's own traffic (UID 1337)
iptables -t nat -A OUTPUT -m owner --uid-owner 1337 -j RETURN

# Exclude localhost traffic (health probes)
iptables -t nat -A OUTPUT -d 127.0.0.1/32 -j RETURN

# Redirect all other outbound traffic to sidecar
iptables -t nat -A OUTPUT -p tcp -j REDIRECT --to-port 15001

# Redirect all inbound traffic to sidecar
iptables -t nat -A PREROUTING -p tcp -j REDIRECT --to-port 15001
```

### Security Considerations

1. **OAuth Credentials**: Stored in Kubernetes Secret, mounted as environment variables
2. **Token Storage**: Tokens cached in-memory within each pod (not persisted)
3. **User Isolation**: Each user has separate tokens per MCP server
4. **Network Policies**: Consider adding NetworkPolicies to restrict traffic

## Files

- `00-namespace.yaml`: Creates the `kagenti-demo` namespace
- `00-oauth-secret.yaml`: OAuth credentials (template, replace with actual values)
- `01-mcp-server.yaml`: MCP Server deployment and service
- `02-aiagent.yaml`: AI Agent pod with AuthBridge sidecar and init container
- `02a-aiagent-config.yaml`: AI Agent configuration ConfigMap
- `03-backend.yaml`: Backend deployment and service
- `03a-backend-config.yaml`: Backend configuration ConfigMap