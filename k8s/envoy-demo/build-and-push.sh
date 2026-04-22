#!/bin/bash

# Build and Push Kagenti Envoy Demo Images to Container Registry
# This script builds Docker images for all demo components and pushes them to ghcr.io
#
# Prerequisites:
#   - Docker installed and running
#   - .env file with GITHUB_TOKEN (PAT with packages:read, packages:write, packages:delete)
#
# Usage:
#   ./build-and-push.sh                    # Build and push all images with 'latest' tag
#   ./build-and-push.sh v1.0.0             # Build and push all images with specific version
#   ./build-and-push.sh --local-only       # Build locally without pushing

set -e

# Configuration
REGISTRY="ghcr.io"
OWNER="davidhadas"
REPO_PREFIX="kagenti"

# Component definitions: name:dockerfile:port
COMPONENTS=(
    "mcp-server:Dockerfile.mcp-server:8082"
    "token-broker:Dockerfile.token-broker:8190"
    "backend:Dockerfile.backend:8187"
    "aiagent:Dockerfile.aiagent:8186"
)

# Parse arguments
VERSION="${1:-latest}"
LOCAL_ONLY=false

if [ "$1" = "--local-only" ]; then
    LOCAL_ONLY=true
    VERSION="latest"
fi

# Change to repository root directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

# Load environment variables from .env
if [ -f .env ]; then
    echo "📋 Loading environment variables from .env..."
    export $(grep -v '^#' .env | grep -v '^$' | xargs)
    echo "✅ Environment variables loaded"
else
    echo "⚠️  Warning: .env file not found"
    if [ "$LOCAL_ONLY" = false ]; then
        echo "❌ Error: .env file required for pushing to registry"
        echo "   Please create .env with GITHUB_TOKEN"
        exit 1
    fi
fi

echo ""
echo "🚀 Building Kagenti Envoy Demo Images"
echo "======================================"
echo "   Working directory: $REPO_ROOT"
echo "   Registry: $REGISTRY"
echo "   Owner: $OWNER"
echo "   Version: $VERSION"
if [ "$LOCAL_ONLY" = true ]; then
    echo "   Mode: Local build only (no push)"
else
    echo "   Mode: Build and push to registry"
fi
echo ""

# Generate build timestamp
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
echo "🕐 Build timestamp: $BUILD_TIME"
echo ""

# Login to registry if pushing
if [ "$LOCAL_ONLY" = false ]; then
    if [ -z "$GITHUB_TOKEN" ]; then
        echo "❌ Error: GITHUB_TOKEN not found in .env"
        echo "   Please add GITHUB_TOKEN to .env file"
        echo "   The token needs packages:read, packages:write, packages:delete scopes"
        exit 1
    fi
    
    echo "🔐 Logging in to $REGISTRY..."
    echo "$GITHUB_TOKEN" | docker login "$REGISTRY" -u "$OWNER" --password-stdin
    
    if [ $? -ne 0 ]; then
        echo "❌ Error: Failed to login to $REGISTRY"
        echo "   Check your GITHUB_TOKEN in .env"
        exit 1
    fi
    
    echo "✅ Successfully logged in to $REGISTRY"
    echo ""
fi

# Build each component
for component_def in "${COMPONENTS[@]}"; do
    IFS=':' read -r name dockerfile port <<< "$component_def"
    
    IMAGE_NAME="$REGISTRY/$OWNER/$REPO_PREFIX-$name:$VERSION"
    if [ "$LOCAL_ONLY" = true ]; then
        IMAGE_NAME="localhost/$OWNER/$REPO_PREFIX-$name:$VERSION"
    fi
    
    echo "🔨 Building $name..."
    echo "   Dockerfile: k8s/envoy-demo/$dockerfile"
    echo "   Image: $IMAGE_NAME"
    echo ""
    
    docker build \
        -f "k8s/envoy-demo/$dockerfile" \
        -t "$IMAGE_NAME" \
        --build-arg VERSION="$VERSION" \
        --build-arg BUILD_TIME="$BUILD_TIME" \
        .
    
    echo ""
    echo "✅ $name image built successfully"
    echo ""
    
    # Push if not local-only
    if [ "$LOCAL_ONLY" = false ]; then
        echo "📤 Pushing $IMAGE_NAME..."
        docker push "$IMAGE_NAME"
        echo "✅ $name image pushed successfully"
        echo ""
    fi
done

echo "======================================"
echo "✅ All Images Built Successfully!"
echo "======================================"
echo ""

if [ "$LOCAL_ONLY" = false ]; then
    echo "📦 Published images:"
    for component_def in "${COMPONENTS[@]}"; do
        IFS=':' read -r name dockerfile port <<< "$component_def"
        echo "   $REGISTRY/$OWNER/$REPO_PREFIX-$name:$VERSION"
    done
    echo ""
    echo "🔗 View packages at:"
    echo "   https://github.com/$OWNER?tab=packages"
    echo ""
else
    echo "📦 Local images:"
    for component_def in "${COMPONENTS[@]}"; do
        IFS=':' read -r name dockerfile port <<< "$component_def"
        echo "   localhost/$OWNER/$REPO_PREFIX-$name:$VERSION"
    done
    echo ""
    echo "🔄 To load into Kind cluster:"
    for component_def in "${COMPONENTS[@]}"; do
        IFS=':' read -r name dockerfile port <<< "$component_def"
        echo "   kind load docker-image localhost/$OWNER/$REPO_PREFIX-$name:$VERSION --name envoy-demo"
    done
    echo ""
fi

echo "📝 Image references for Kubernetes manifests:"
for component_def in "${COMPONENTS[@]}"; do
    IFS=':' read -r name dockerfile port <<< "$component_def"
    if [ "$LOCAL_ONLY" = false ]; then
        echo "   $name: $REGISTRY/$OWNER/$REPO_PREFIX-$name:$VERSION"
    else
        echo "   $name: localhost/$OWNER/$REPO_PREFIX-$name:$VERSION"
    fi
done
echo ""

# Made with Bob