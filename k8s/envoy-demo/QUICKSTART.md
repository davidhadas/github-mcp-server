# Quick Start: Building and Publishing Container Images

This guide provides a streamlined workflow for building and publishing the Kagenti Envoy Demo container images.

## Prerequisites

1. **Docker** installed and running
2. **GitHub Personal Access Token** with package permissions

## Setup (One-time)

### 1. Create GitHub Personal Access Token

1. Go to [GitHub Settings → Tokens](https://github.com/settings/tokens)
2. Click "Generate new token (classic)"
3. Select scopes:
   - ✅ `write:packages` (includes read:packages)
   - ✅ `delete:packages` (optional, for cleanup)
4. Generate and copy the token

### 2. Configure Environment

```bash
# Copy the example environment file
cp .env.example .env

# Edit .env and add your tokens
nano .env  # or use your preferred editor
```

Your `.env` should contain:
```bash
# GitHub OAuth (for MCP Server)
GITHUB_OAUTH_CLIENT_ID=your_oauth_client_id
GITHUB_OAUTH_CLIENT_SECRET=your_oauth_client_secret

# GitHub Personal Access Token (for container registry)
GITHUB_TOKEN=ghp_your_personal_access_token_here
```

## Build and Push Images

### Option 1: Build and Push to Registry

```bash
# Build and push with 'latest' tag
./k8s/envoy-demo/build-and-push.sh

# Build and push with specific version
./k8s/envoy-demo/build-and-push.sh v1.0.0
```

The script will:
1. ✅ Load `GITHUB_TOKEN` from `.env`
2. ✅ Login to `ghcr.io` automatically
3. ✅ Build the demo image with all components
4. ✅ Push to GitHub Container Registry

### Option 2: Build Locally (No Push)

```bash
# Build for local testing only
./k8s/envoy-demo/build-and-push.sh --local-only
```

## What Gets Built

The build creates one multi-component image:

**`ghcr.io/davidhadas/kagenti-demo:latest`**

Contains all four binaries:
- `/github-mcp-server` - GitHub MCP Server
- `/token-broker` - OAuth Token Broker
- `/backend-envoy` - Backend Service
- `/aiagent` - AI Agent

Each Kubernetes pod runs the appropriate binary via command override.

## Verify Build

```bash
# Pull the image
docker pull ghcr.io/davidhadas/kagenti-demo:latest

# List binaries in the image
docker run --rm ghcr.io/davidhadas/kagenti-demo:latest ls -la /

# Check image size
docker images ghcr.io/davidhadas/kagenti-demo
```

## Deploy to Kubernetes

### For Production (Using Registry Images)

```bash
# Update manifests to use registry images
# See REGISTRY_DEPLOYMENT.md for details

# Create namespace
kubectl apply -f k8s/envoy-demo/00-namespace.yaml

# Create image pull secret (if using private images)
kubectl create secret docker-registry ghcr-secret \
  --docker-server=ghcr.io \
  --docker-username=davidhadas \
  --docker-password=$GITHUB_TOKEN \
  --namespace=kagenti-envoy-demo

# Deploy
kubectl apply -f k8s/envoy-demo/
```

### For Local Development (Using Kind)

```bash
# Build locally
./k8s/envoy-demo/build-and-push.sh --local-only

# Use existing deployment script (handles Kind loading)
./k8s/envoy-demo/build-and-deploy.sh
```

## Troubleshooting

### Build Fails

```bash
# Clean Docker cache
docker builder prune -a

# Rebuild without cache
docker build --no-cache -f k8s/envoy-demo/Dockerfile.demo .
```

### Login Fails

```bash
# Check GITHUB_TOKEN is set
echo $GITHUB_TOKEN

# Verify token has correct permissions
# Token needs: packages:read, packages:write

# Try manual login
echo $GITHUB_TOKEN | docker login ghcr.io -u davidhadas --password-stdin
```

### Push Fails

```bash
# Check if you have permission to push
# You need write access to the repository

# Verify image was built
docker images | grep kagenti-demo

# Try pushing manually
docker push ghcr.io/davidhadas/kagenti-demo:latest
```

### Image Pull Fails in Kubernetes

```bash
# Check if image exists
docker pull ghcr.io/davidhadas/kagenti-demo:latest

# Verify secret exists
kubectl get secret ghcr-secret -n kagenti-envoy-demo

# Check pod events
kubectl describe pod -n kagenti-envoy-demo <pod-name>
```

## Common Workflows

### Update and Redeploy

```bash
# 1. Make code changes
git add .
git commit -m "feat: update component"

# 2. Build and push new version
./k8s/envoy-demo/build-and-push.sh v1.1.0

# 3. Update manifests to use new version
sed -i 's|kagenti-demo:.*|kagenti-demo:v1.1.0|g' k8s/envoy-demo/*.yaml

# 4. Deploy
kubectl apply -f k8s/envoy-demo/

# 5. Verify
kubectl rollout status deployment -n kagenti-envoy-demo
```

### Test Locally Before Pushing

```bash
# 1. Build locally
./k8s/envoy-demo/build-and-push.sh --local-only

# 2. Test in Kind
./k8s/envoy-demo/build-and-deploy.sh

# 3. Verify everything works
curl http://localhost:8187/demo

# 4. If good, build and push
./k8s/envoy-demo/build-and-push.sh v1.0.0
```

## Next Steps

- **Detailed Build Guide**: See [IMAGE_BUILD.md](IMAGE_BUILD.md)
- **Registry Deployment**: See [REGISTRY_DEPLOYMENT.md](REGISTRY_DEPLOYMENT.md)
- **Architecture**: See [ARCHITECTURE.md](ARCHITECTURE.md)
- **General Deployment**: See [DEPLOYMENT.md](DEPLOYMENT.md)

## Summary

```bash
# Complete workflow in 3 commands:

# 1. Setup (one-time)
cp .env.example .env && nano .env

# 2. Build and push
./k8s/envoy-demo/build-and-push.sh

# 3. Deploy
kubectl apply -f k8s/envoy-demo/
```

That's it! 🚀