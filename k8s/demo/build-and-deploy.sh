#!/bin/bash

# Build and Deploy KAgentI Demo to Kubernetes
# This script builds Docker images with the correct tags and deploys to k8s
#
# Usage:
#   ./build-and-deploy.sh       # Fast: reuse existing cluster
#   ./build-and-deploy.sh -f    # Force: recreate cluster from scratch

set -e

# Parse command line arguments
FORCE_RECREATE=false
while getopts "f" opt; do
  case $opt in
    f)
      FORCE_RECREATE=true
      ;;
    \?)
      echo "Usage: $0 [-f]"
      echo "  -f    Force recreate Kind cluster (slower but ensures clean state)"
      exit 1
      ;;
  esac
done

# Change to repository root directory (parent of k8s/demo)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

echo "🚀 Building and Deploying KAgentI Demo"
echo "======================================"
echo "   Working directory: $REPO_ROOT"
if [ "$FORCE_RECREATE" = true ]; then
    echo "   Mode: Force recreate cluster (-f)"
else
    echo "   Mode: Fast (reuse existing cluster)"
fi
echo ""

# Check if .env file exists for OAuth credentials
if [ ! -f .env ]; then
    echo "❌ Error: .env file not found"
    echo "   Please create .env with your GitHub OAuth credentials:"
    echo "   cp .env.example .env"
    echo "   # Then edit .env with your actual credentials"
    exit 1
fi

# Load OAuth credentials - use export to make them available to kubectl
export $(grep -v '^#' .env | xargs)

if [ -z "$GITHUB_OAUTH_CLIENT_ID" ] || [ -z "$GITHUB_OAUTH_CLIENT_SECRET" ]; then
    echo "❌ Error: OAuth credentials not set in .env"
    exit 1
fi

echo "   Using CLIENT_ID: ${GITHUB_OAUTH_CLIENT_ID:0:15}..."
echo "   Using CLIENT_SECRET: ${GITHUB_OAUTH_CLIENT_SECRET:0:15}..."

echo "✅ OAuth credentials loaded"
echo ""

# Handle Kind cluster creation/recreation
if [ "$FORCE_RECREATE" = true ]; then
    echo "🔄 Force recreating Kind cluster..."
    kind delete cluster --name kagenti-demo 2>/dev/null || true
    kind create cluster --name kagenti-demo --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
EOF
    echo "✅ Kind cluster recreated"
else
    # Check if Kind cluster exists, create only if needed
    if ! kind get clusters 2>/dev/null | grep -q "^kagenti-demo$"; then
        echo "🔄 Creating Kind cluster..."
        kind create cluster --name kagenti-demo --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
EOF
        echo "✅ Kind cluster created"
    else
        echo "✅ Using existing Kind cluster (use -f to force recreate)"
    fi
fi
echo ""

# Generate build timestamp
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
echo "🕐 Build timestamp: $BUILD_TIME"
echo ""

# Build Docker images in parallel with correct tags for demo
echo "🔨 Building Docker images in parallel..."
echo ""

# Build all images in parallel using background jobs
docker build -f Dockerfile -t localhost/kagenti/mcp-server:demo \
  --build-arg BUILD_TIME="$BUILD_TIME" . > /tmp/build-mcp.log 2>&1 &
MCP_PID=$!

docker build -f Dockerfile.aiagent -t localhost/kagenti/aiagent:demo \
  --build-arg BUILD_TIME="$BUILD_TIME" . > /tmp/build-aiagent.log 2>&1 &
AIAGENT_PID=$!

docker build -f Dockerfile.authbridge-sidecar -t localhost/kagenti/authbridge-sidecar:demo \
  --build-arg BUILD_TIME="$BUILD_TIME" . > /tmp/build-sidecar.log 2>&1 &
SIDECAR_PID=$!

docker build -f Dockerfile.backend -t localhost/kagenti/backend:demo \
  --build-arg BUILD_TIME="$BUILD_TIME" . > /tmp/build-backend.log 2>&1 &
BACKEND_PID=$!

# Wait for all builds to complete
echo "⏳ Waiting for parallel builds to complete..."
wait $MCP_PID && echo "✅ MCP Server built" || (echo "❌ MCP Server build failed"; cat /tmp/build-mcp.log; exit 1)
wait $AIAGENT_PID && echo "✅ AI Agent built" || (echo "❌ AI Agent build failed"; cat /tmp/build-aiagent.log; exit 1)
wait $SIDECAR_PID && echo "✅ AuthBridge Sidecar built" || (echo "❌ Sidecar build failed"; cat /tmp/build-sidecar.log; exit 1)
wait $BACKEND_PID && echo "✅ Backend built" || (echo "❌ Backend build failed"; cat /tmp/build-backend.log; exit 1)

echo ""
echo "✅ All images built successfully"
echo ""

# Load images into Kind cluster in parallel
echo "📦 Loading images into Kind cluster in parallel..."
kind load docker-image localhost/kagenti/mcp-server:demo --name kagenti-demo &
kind load docker-image localhost/kagenti/aiagent:demo --name kagenti-demo &
kind load docker-image localhost/kagenti/authbridge-sidecar:demo --name kagenti-demo &
kind load docker-image localhost/kagenti/backend:demo --name kagenti-demo &
wait
echo "✅ Images loaded into Kind cluster"
echo ""

# Create namespace first (since we recreated the cluster)
echo "📦 Creating namespace..."
kubectl apply -f k8s/demo/00-namespace.yaml
echo ""

# Delete all existing resources to ensure clean state
echo "🧹 Cleaning up existing resources..."
kubectl delete deployment,service,configmap -n kagenti-demo --all 2>/dev/null || true

echo ""
echo "🔐 Creating/updating OAuth secret..."
kubectl delete secret mcp-oauth-credentials -n kagenti-demo 2>/dev/null || true
kubectl create secret generic mcp-oauth-credentials \
  --from-literal=client-id="$GITHUB_OAUTH_CLIENT_ID" \
  --from-literal=client-secret="$GITHUB_OAUTH_CLIENT_SECRET" \
  --namespace=kagenti-demo

echo "✅ OAuth secret created"
echo ""

# Apply Kubernetes manifests (creates all resources fresh)
echo "☸️  Deploying to Kubernetes..."
kubectl apply -f k8s/demo/

echo ""
echo "⏳ Waiting for deployments to be created..."
sleep 2

echo ""
echo "⏳ Waiting for pods to be ready..."
kubectl wait --for=condition=ready pod -l app=mcp-server -n kagenti-demo --timeout=60s
kubectl wait --for=condition=ready pod -l app=aiagent -n kagenti-demo --timeout=60s
kubectl wait --for=condition=ready pod -l app=backend -n kagenti-demo --timeout=60s

echo ""
echo "======================================"
echo "✅ KAgentI Demo Deployed Successfully!"
echo "======================================"
echo ""
echo "📊 Architecture:"
echo "   Browser → Backend:8187 → AI Agent Pod (with AuthBridge Sidecar) → MCP Server:8184"
echo ""
echo "🌐 Setting up port forwarding..."
# Kill any existing port-forward on 8187
pkill -f "port-forward.*8187:8187" 2>/dev/null || true
# Start port-forward in background
kubectl port-forward -n kagenti-demo svc/backend-service 8187:8187 > /dev/null 2>&1 &
PORT_FORWARD_PID=$!
echo "✅ Port forwarding active (PID: $PORT_FORWARD_PID)"
echo ""
echo "🌐 Access the demo:"
echo "   Open: http://localhost:8187/demo"
echo ""
echo "📝 View logs:"
echo "   Backend:    kubectl logs -n kagenti-demo -l app=backend --tail=100 -f"
echo "   AI Agent:   kubectl logs -n kagenti-demo -l app=aiagent -c aiagent --tail=100 -f"
echo "   Sidecar:    kubectl logs -n kagenti-demo -l app=aiagent -c authbridge-sidecar --tail=100 -f"
echo "   MCP Server: kubectl logs -n kagenti-demo -l app=mcp-server --tail=100 -f"
echo ""
echo "🔄 To rebuild and redeploy:"
echo "   ./k8s/demo/build-and-deploy.sh       # Fast (reuse cluster)"
echo "   ./k8s/demo/build-and-deploy.sh -f    # Force recreate cluster"
echo ""
echo "🧹 To clean up:"
echo "   pkill -f 'port-forward.*8187:8187'  # Stop port forwarding"
echo "   kind delete cluster --name kagenti-demo  # Delete cluster"
echo ""

# Made with Bob
