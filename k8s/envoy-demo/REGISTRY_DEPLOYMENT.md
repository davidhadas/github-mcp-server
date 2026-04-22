# Deploying with Container Registry Images

This guide shows how to deploy the Kagenti Envoy Demo using images from GitHub Container Registry (ghcr.io) instead of local images.

## Quick Start

### 1. Build and Push Images

```bash
# Login to GitHub Container Registry
echo "YOUR_GITHUB_TOKEN" | docker login ghcr.io -u YOUR_GITHUB_USERNAME --password-stdin

# Build and push demo components
cd /path/to/github-mcp-server
./k8s/envoy-demo/build-and-push.sh

# This creates: ghcr.io/davidhadas/kagenti-demo:latest
```

### 2. Create Image Pull Secret (if using private images)

```bash
# Create namespace
kubectl apply -f k8s/envoy-demo/00-namespace.yaml

# Create image pull secret
kubectl create secret docker-registry ghcr-secret \
  --docker-server=ghcr.io \
  --docker-username=YOUR_GITHUB_USERNAME \
  --docker-password=YOUR_GITHUB_TOKEN \
  --namespace=kagenti-envoy-demo
```

### 3. Update Manifests

You have two options:

#### Option A: Use the provided registry manifests (recommended)

```bash
# Copy the registry-ready manifests
cp k8s/envoy-demo/registry/*.yaml k8s/envoy-demo/

# Or deploy directly from registry directory
kubectl apply -f k8s/envoy-demo/registry/
```

#### Option B: Manually update existing manifests

See the "Manual Manifest Updates" section below.

### 4. Deploy

```bash
# Deploy all components
kubectl apply -f k8s/envoy-demo/

# Wait for pods to be ready
kubectl wait --for=condition=ready pod --all -n kagenti-envoy-demo --timeout=120s

# Check status
kubectl get pods -n kagenti-envoy-demo
```

## Manual Manifest Updates

If you prefer to manually update the existing manifests, here are the changes needed:

### 01-token-broker.yaml

```yaml
spec:
  template:
    spec:
      # Add image pull secret (if using private registry)
      imagePullSecrets:
        - name: ghcr-secret
      
      containers:
        - name: token-broker
          # Change from local image to registry image
          image: ghcr.io/davidhadas/kagenti-demo:latest
          imagePullPolicy: Always  # Changed from Never
          command: ["/token-broker"]
```

### 02-mcp-server.yaml

```yaml
spec:
  template:
    spec:
      imagePullSecrets:
        - name: ghcr-secret
      
      containers:
        - name: mcp-server
          image: ghcr.io/davidhadas/kagenti-demo:latest
          imagePullPolicy: Always
          command: ["/github-mcp-server"]
```

### 04-aiagent.yaml

```yaml
spec:
  template:
    spec:
      imagePullSecrets:
        - name: ghcr-secret
      
      containers:
        # AuthBridge sidecar
        - name: authbridge
          image: ghcr.io/davidhadas/kagenti-extensions/authbridge:latest
          imagePullPolicy: Always
          # ... rest of config
        
        # AI Agent
        - name: aiagent
          image: ghcr.io/davidhadas/kagenti-demo:latest
          imagePullPolicy: Always
          command: ["/aiagent"]
```

### 05-backend.yaml

```yaml
spec:
  template:
    spec:
      imagePullSecrets:
        - name: ghcr-secret
      
      containers:
        - name: backend
          image: ghcr.io/davidhadas/kagenti-demo:latest
          imagePullPolicy: Always
          command: ["/backend-envoy"]
```

## Image Pull Policy

| Policy | When to Use | Behavior |
|--------|-------------|----------|
| `Always` | Production, latest tag | Always pull from registry |
| `IfNotPresent` | Versioned tags | Pull only if not cached |
| `Never` | Local development | Never pull, use local only |

**Recommendation:**
- Use `Always` for `latest` tag to ensure you get updates
- Use `IfNotPresent` for versioned tags (e.g., `v1.0.0`)

## Verification

### Check Image Pull Status

```bash
# Check if images are being pulled
kubectl get events -n kagenti-envoy-demo --sort-by='.lastTimestamp' | grep -i pull

# Check pod image status
kubectl get pods -n kagenti-envoy-demo -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.containerStatuses[*].image}{"\n"}{end}'
```

### Verify Running Images

```bash
# Check what images are actually running
kubectl get pods -n kagenti-envoy-demo -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{range .spec.containers[*]}  {.name}: {.image}{"\n"}{end}{"\n"}{end}'
```

Expected output:
```
token-broker
  token-broker: ghcr.io/davidhadas/kagenti-demo:latest

mcp-server
  mcp-server: ghcr.io/davidhadas/kagenti-demo:latest

aiagent
  authbridge: ghcr.io/davidhadas/kagenti-extensions/authbridge:latest
  aiagent: ghcr.io/davidhadas/kagenti-demo:latest

backend
  backend: ghcr.io/davidhadas/kagenti-demo:latest
```

## Troubleshooting

### ImagePullBackOff Error

```bash
# Check pod events
kubectl describe pod -n kagenti-envoy-demo <pod-name>

# Common causes:
# 1. Image doesn't exist in registry
# 2. Authentication failed (wrong credentials)
# 3. No permission to pull (missing imagePullSecrets)
```

**Solutions:**

1. **Verify image exists:**
```bash
# Try pulling manually
docker pull ghcr.io/davidhadas/kagenti-demo:latest
```

2. **Check secret:**
```bash
# Verify secret exists
kubectl get secret ghcr-secret -n kagenti-envoy-demo

# Recreate if needed
kubectl delete secret ghcr-secret -n kagenti-envoy-demo
kubectl create secret docker-registry ghcr-secret \
  --docker-server=ghcr.io \
  --docker-username=YOUR_USERNAME \
  --docker-password=YOUR_TOKEN \
  --namespace=kagenti-envoy-demo
```

3. **Check image pull secrets in pod:**
```bash
kubectl get pod <pod-name> -n kagenti-envoy-demo -o jsonpath='{.spec.imagePullSecrets}'
```

### ErrImagePull Error

This usually means the image doesn't exist or you don't have permission.

```bash
# Check if image is public
curl -I https://ghcr.io/v2/davidhadas/kagenti-demo/manifests/latest

# If 401, image is private - ensure imagePullSecrets is configured
# If 404, image doesn't exist - build and push it
```

### Wrong Image Version

```bash
# Force pull latest version
kubectl rollout restart deployment -n kagenti-envoy-demo

# Or delete pods to force recreation
kubectl delete pods --all -n kagenti-envoy-demo
```

## Switching Between Local and Registry Images

### From Local to Registry

1. Update manifests (see "Manual Manifest Updates" above)
2. Create image pull secret (if needed)
3. Apply changes: `kubectl apply -f k8s/envoy-demo/`
4. Restart deployments: `kubectl rollout restart deployment -n kagenti-envoy-demo`

### From Registry to Local

1. Build local images: `./k8s/envoy-demo/build-and-push.sh --local-only`
2. Load into Kind: `kind load docker-image localhost/davidhadas/kagenti-demo:latest --name envoy-demo`
3. Update manifests to use `localhost/` prefix and `imagePullPolicy: Never`
4. Apply changes: `kubectl apply -f k8s/envoy-demo/`

## Best Practices

1. **Use Versioned Tags in Production**
   - Don't use `latest` in production
   - Use semantic versions: `v1.0.0`, `v1.2.3`
   - Enables rollbacks and reproducibility

2. **Keep Image Pull Secrets Secure**
   - Don't commit secrets to git
   - Use separate tokens for different environments
   - Rotate tokens regularly

3. **Monitor Image Pulls**
   - Check registry usage/bandwidth
   - Set up alerts for pull failures
   - Monitor image sizes

4. **Use Image Digests for Critical Deployments**
   ```yaml
   image: ghcr.io/davidhadas/kagenti-demo@sha256:abc123...
   ```
   This ensures exact image version, even if tags are moved.

## GitHub Actions Integration

For automated deployments, you can use GitHub Actions to build and deploy:

```yaml
# .github/workflows/deploy.yml
name: Deploy to Kubernetes

on:
  push:
    branches: [main]

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Build and push
        run: |
          echo "${{ secrets.GITHUB_TOKEN }}" | docker login ghcr.io -u ${{ github.actor }} --password-stdin
          ./k8s/envoy-demo/build-and-push.sh
      
      - name: Deploy to Kubernetes
        run: |
          # Configure kubectl with your cluster
          # kubectl apply -f k8s/envoy-demo/
```

## References

- [IMAGE_BUILD.md](IMAGE_BUILD.md) - Detailed build instructions
- [DEPLOYMENT.md](DEPLOYMENT.md) - General deployment guide
- [GitHub Container Registry Docs](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)