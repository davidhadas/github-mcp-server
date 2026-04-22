# Building and Publishing Container Images

This guide explains how to build and publish container images for the Kagenti Envoy Demo to a container registry like GitHub Container Registry (ghcr.io).

## Overview

The demo uses two main container images:

1. **AuthBridge Sidecar** - Built from `kagenti-extensions/authbridge`
   - Published at: `ghcr.io/davidhadas/kagenti-extensions/authbridge:latest`
   - Contains: Envoy Proxy + AuthBridge ext_proc

2. **Demo Components** - Built from this repository
   - Published at: `ghcr.io/davidhadas/kagenti-demo:latest`
   - Contains: MCP Server, Token Broker, Backend, AI Agent (all in one image)

## Prerequisites

### 1. Docker Installation
```bash
# Verify Docker is installed and running
docker --version
docker info
```

### 2. GitHub Container Registry Authentication

Create a GitHub Personal Access Token (PAT) with package permissions:

1. Go to GitHub Settings → Developer settings → Personal access tokens → Tokens (classic)
2. Click "Generate new token (classic)"
3. Select scopes:
   - ✅ `write:packages` (includes `read:packages`)
   - ✅ `delete:packages` (optional, for cleanup)
4. Generate and copy the token

Add the token to your `.env` file:
```bash
# Copy the example file if you haven't already
cp .env.example .env

# Edit .env and add your token
# GITHUB_TOKEN=ghp_your_token_here
```

The `build-and-push.sh` script will automatically:
- Load the token from `.env`
- Login to ghcr.io using the token
- Push images after building

**Note:** The `.env` file is in `.gitignore` and will never be committed to git.

## Building Images

### Option 1: Build and Push to Registry (Production)

Build and push images to ghcr.io with version tag:

```bash
# Ensure .env has GITHUB_TOKEN set
# The script will automatically login using the token

# Build and push with 'latest' tag
./k8s/envoy-demo/build-and-push.sh

# Build and push with specific version
./k8s/envoy-demo/build-and-push.sh v1.0.0

# Build and push with semantic version
./k8s/envoy-demo/build-and-push.sh v1.2.3
```

This will:
1. Load GITHUB_TOKEN from `.env`
2. Login to ghcr.io automatically
3. Build the demo image with all components
4. Tag it as `ghcr.io/davidhadas/kagenti-demo:VERSION`
5. Push to GitHub Container Registry
6. Make it available for Kubernetes deployments

### Option 2: Build Locally Only (Development)

Build images locally without pushing to registry:

```bash
# Build for local testing
./k8s/envoy-demo/build-and-push.sh --local-only
```

This will:
1. Build the demo image
2. Tag it as `localhost/davidhadas/kagenti-demo:latest`
3. Keep it local (no push to registry)
4. Suitable for Kind cluster testing

## Image Contents

### Demo Image (`ghcr.io/davidhadas/kagenti-demo:latest`)

The demo image contains all four binaries:

| Binary | Path | Purpose | Default Port |
|--------|------|---------|--------------|
| `github-mcp-server` | `/github-mcp-server` | GitHub MCP Server with OAuth | 8082 |
| `token-broker` | `/token-broker` | OAuth session & token management | 8190 |
| `backend-envoy` | `/backend-envoy` | User-facing backend service | 8187 |
| `aiagent` | `/aiagent` | AI Agent application | 8186 |

The Kubernetes manifests override the entrypoint to run the appropriate binary for each component.

## Using Images in Kubernetes

### For Public Registry (ghcr.io)

If your images are public, update the manifests to use the registry images:

```yaml
# In k8s/envoy-demo/*.yaml
spec:
  containers:
    - name: token-broker
      image: ghcr.io/davidhadas/kagenti-demo:latest
      imagePullPolicy: Always
      command: ["/token-broker"]
```

### For Private Registry (ghcr.io with authentication)

1. Create an image pull secret:

```bash
kubectl create secret docker-registry ghcr-secret \
  --docker-server=ghcr.io \
  --docker-username=YOUR_GITHUB_USERNAME \
  --docker-password=YOUR_GITHUB_TOKEN \
  --namespace=kagenti-envoy-demo
```

2. Update manifests to use the secret:

```yaml
# In k8s/envoy-demo/*.yaml
spec:
  imagePullSecrets:
    - name: ghcr-secret
  containers:
    - name: token-broker
      image: ghcr.io/davidhadas/kagenti-demo:latest
      imagePullPolicy: Always
      command: ["/token-broker"]
```

### For Local Development (Kind)

When using local images with Kind:

```bash
# Build locally
./k8s/envoy-demo/build-and-push.sh --local-only

# Load into Kind cluster
kind load docker-image localhost/davidhadas/kagenti-demo:latest --name envoy-demo

# Use in manifests with imagePullPolicy: Never
```

## Updating Kubernetes Manifests

After building and pushing images, update the image references in the manifests:

### Token Broker (`01-token-broker.yaml`)
```yaml
containers:
  - name: token-broker
    image: ghcr.io/davidhadas/kagenti-demo:latest
    imagePullPolicy: Always
    command: ["/token-broker"]
```

### MCP Server (`02-mcp-server.yaml`)
```yaml
containers:
  - name: mcp-server
    image: ghcr.io/davidhadas/kagenti-demo:latest
    imagePullPolicy: Always
    command: ["/github-mcp-server"]
```

### AI Agent (`04-aiagent.yaml`)
```yaml
containers:
  - name: authbridge
    image: ghcr.io/davidhadas/kagenti-extensions/authbridge:latest
    imagePullPolicy: Always
  - name: aiagent
    image: ghcr.io/davidhadas/kagenti-demo:latest
    imagePullPolicy: Always
    command: ["/aiagent"]
```

### Backend (`05-backend.yaml`)
```yaml
containers:
  - name: backend
    image: ghcr.io/davidhadas/kagenti-demo:latest
    imagePullPolicy: Always
    command: ["/backend-envoy"]
```

## Image Versioning Strategy

### Development
- Use `latest` tag for ongoing development
- Build and push frequently
- Use `imagePullPolicy: Always` to ensure latest version

### Staging/Testing
- Use semantic versions: `v1.0.0-rc1`, `v1.0.0-beta1`
- Tag specific commits for reproducibility
- Use `imagePullPolicy: IfNotPresent`

### Production
- Use immutable semantic versions: `v1.0.0`, `v1.2.3`
- Never overwrite existing version tags
- Use `imagePullPolicy: IfNotPresent`
- Consider using image digests for maximum reproducibility

## Multi-Architecture Builds

To build for multiple architectures (amd64, arm64):

```bash
# Create and use buildx builder
docker buildx create --name multiarch --use
docker buildx inspect --bootstrap

# Build and push multi-arch image
docker buildx build \
  -f k8s/envoy-demo/Dockerfile.demo \
  -t ghcr.io/davidhadas/kagenti-demo:latest \
  --platform linux/amd64,linux/arm64 \
  --build-arg VERSION=latest \
  --build-arg BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ") \
  --push \
  .
```

## Troubleshooting

### Authentication Failed
```bash
# Re-login to ghcr.io
docker logout ghcr.io
echo "YOUR_TOKEN" | docker login ghcr.io -u YOUR_USERNAME --password-stdin
```

### Image Push Denied
- Verify your GitHub token has `packages:write` scope
- Ensure you have write access to the repository
- Check if the package already exists and you have permission to update it

### Image Pull Failed in Kubernetes
```bash
# Check if secret exists
kubectl get secret ghcr-secret -n kagenti-envoy-demo

# Verify secret is correct
kubectl get secret ghcr-secret -n kagenti-envoy-demo -o yaml

# Check pod events
kubectl describe pod -n kagenti-envoy-demo -l app=token-broker
```

### Build Failures
```bash
# Clean Docker build cache
docker builder prune -a

# Rebuild without cache
docker build --no-cache -f k8s/envoy-demo/Dockerfile.demo -t test .
```

## GitHub Actions (Optional)

For automated builds on push/release, create `.github/workflows/build-images.yml`:

```yaml
name: Build and Push Images

on:
  push:
    branches: [main]
    tags: ['v*']
  pull_request:
    branches: [main]

jobs:
  build:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      packages: write
    
    steps:
      - uses: actions/checkout@v4
      
      - name: Login to GitHub Container Registry
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      
      - name: Extract metadata
        id: meta
        uses: docker/metadata-action@v5
        with:
          images: ghcr.io/${{ github.repository_owner }}/kagenti-demo
          tags: |
            type=ref,event=branch
            type=ref,event=pr
            type=semver,pattern={{version}}
            type=semver,pattern={{major}}.{{minor}}
      
      - name: Build and push
        uses: docker/build-push-action@v5
        with:
          context: .
          file: k8s/envoy-demo/Dockerfile.demo
          push: ${{ github.event_name != 'pull_request' }}
          tags: ${{ steps.meta.outputs.tags }}
          labels: ${{ steps.meta.outputs.labels }}
          build-args: |
            VERSION=${{ steps.meta.outputs.version }}
            BUILD_TIME=${{ github.event.head_commit.timestamp }}
```

## References

- [GitHub Container Registry Documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [Docker Multi-platform Builds](https://docs.docker.com/build/building/multi-platform/)
- [Kubernetes Image Pull Secrets](https://kubernetes.io/docs/tasks/configure-pod-container/pull-image-private-registry/)