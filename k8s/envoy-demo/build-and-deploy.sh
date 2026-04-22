#!/bin/bash

# Build and Deploy Envoy Demo to Kubernetes
# This script builds Docker images and deploys the Envoy-based demo to k8s
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

# Change to repository root directory (parent of k8s/envoy-demo)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

echo "🚀 Building and Deploying Envoy Demo"
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
    kind delete cluster --name envoy-demo 2>/dev/null || true
    kind create cluster --name envoy-demo --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30184  # MCP Server
    hostPort: 30184
  - containerPort: 30186  # AI Agent
    hostPort: 30186
  - containerPort: 30187  # Backend
    hostPort: 30187
EOF
    echo "✅ Kind cluster recreated"
else
    # Check if Kind cluster exists, create only if needed
    if ! kind get clusters 2>/dev/null | grep -q "^envoy-demo$"; then
        echo "🔄 Creating Kind cluster..."
        kind create cluster --name envoy-demo --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30184  # MCP Server
    hostPort: 30184
  - containerPort: 30186  # AI Agent
    hostPort: 30186
  - containerPort: 30187  # Backend
    hostPort: 30187
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

# Build AuthBridge image
echo "🔨 Building AuthBridge image..."
echo ""

docker build -f kagenti-extensions/authbridge/cmd/authbridge/Dockerfile \
  -t localhost/kagenti/authbridge:demo \
  --build-arg BUILD_TIME="$BUILD_TIME" \
  kagenti-extensions/authbridge

echo ""
echo "✅ AuthBridge image built successfully"
echo ""

# Build demo image with all other components
echo "🔨 Building demo image with all other components..."
echo ""

docker build -f Dockerfile.envoy-demo -t localhost/kagenti/mcp-server:demo \
  --build-arg BUILD_TIME="$BUILD_TIME" .

echo ""
echo "✅ Demo image built successfully"
echo ""

# Load images into Kind cluster
echo "📦 Loading images into Kind cluster..."
kind load docker-image localhost/kagenti/authbridge:demo --name envoy-demo
kind load docker-image localhost/kagenti/mcp-server:demo --name envoy-demo
echo "✅ Images loaded into Kind cluster"
echo ""

# Create namespace first
echo "📦 Creating namespace..."
kubectl apply -f k8s/envoy-demo/00-namespace.yaml
echo ""

# Delete all existing resources to ensure clean state
echo "🧹 Cleaning up existing resources..."
kubectl delete deployment,service,configmap -n kagenti-envoy-demo --all 2>/dev/null || true

echo ""
echo "🔐 Creating/updating OAuth secret..."
kubectl delete secret mcp-oauth-credentials -n kagenti-envoy-demo 2>/dev/null || true
kubectl create secret generic mcp-oauth-credentials \
  --from-literal=client-id="$GITHUB_OAUTH_CLIENT_ID" \
  --from-literal=client-secret="$GITHUB_OAUTH_CLIENT_SECRET" \
  --namespace=kagenti-envoy-demo

echo "✅ OAuth secret created"
echo ""

# Apply Kubernetes manifests (creates all resources fresh)
echo "☸️  Deploying to Kubernetes..."
kubectl apply -f k8s/envoy-demo/

echo ""
echo "⏳ Waiting for deployments to be created..."
sleep 2

echo ""
echo "⏳ Waiting for pods to be ready..."
kubectl wait --for=condition=ready pod -l app=token-broker -n kagenti-envoy-demo --timeout=60s
kubectl wait --for=condition=ready pod -l app=mcp-server -n kagenti-envoy-demo --timeout=60s
kubectl wait --for=condition=ready pod -l app=aiagent -n kagenti-envoy-demo --timeout=90s
kubectl wait --for=condition=ready pod -l app=backend -n kagenti-envoy-demo --timeout=60s

echo ""
echo "======================================"
echo "✅ Envoy Demo Deployed Successfully!"
echo "======================================"
echo ""
echo "📊 Architecture:"
echo "   Browser → Backend:8187 → AI Agent Pod (AuthBridge sidecar) → MCP Server:30184"
echo "   AuthBridge sidecar contains: Envoy + AuthBridge (ext_proc)"
echo "   Envoy → AuthBridge (ext_proc) → Token Broker → MCP Server"
echo "   Token Broker manages OAuth sessions and token caching"
echo ""
echo "🔄 Setting up port forwarding for backend on port 8187..."
# Kill any existing port-forward on 8187
pkill -f "port-forward.*backend-service.*8187" 2>/dev/null || true
sleep 1
# Start port-forward in background
kubectl port-forward -n kagenti-envoy-demo svc/backend-service 8187:8187 >/dev/null 2>&1 &
PORT_FORWARD_PID=$!
sleep 2
echo "✅ Port forwarding active (PID: $PORT_FORWARD_PID)"
echo ""
echo "🌐 Access URLs:"
echo "   Demo:       http://localhost:8187/demo"
echo "   Backend:    http://localhost:8187/health"
echo "   Callback:   http://localhost:8187/callback"
echo ""
echo "   (Also available via NodePort: http://localhost:30187/demo)"
echo ""
echo "⚠️  GitHub App Configuration:"
echo "   Homepage URL:            http://localhost:8187/demo"
echo "   Authorization callback:  http://localhost:8187/callback"
echo ""
echo "📝 View logs:"
echo "   Token Broker: kubectl logs -n kagenti-envoy-demo -l app=token-broker --tail=100 -f"
echo "   Backend:      kubectl logs -n kagenti-envoy-demo -l app=backend --tail=100 -f"
echo "   AI Agent:     kubectl logs -n kagenti-envoy-demo -l app=aiagent -c aiagent --tail=100 -f"
echo "   AuthBridge:   kubectl logs -n kagenti-envoy-demo -l app=aiagent -c authbridge --tail=100 -f"
echo "   MCP Server:   kubectl logs -n kagenti-envoy-demo -l app=mcp-server --tail=100 -f"
echo ""
echo "🔍 Check Envoy stats:"
echo "   kubectl port-forward -n kagenti-envoy-demo -l app=aiagent 9901:9901"
echo "   curl http://localhost:9901/stats | grep authbridge"
echo ""
echo "🔄 To rebuild and redeploy:"
echo "   ./k8s/envoy-demo/build-and-deploy.sh       # Fast (reuse cluster)"
echo "   ./k8s/envoy-demo/build-and-deploy.sh -f    # Force recreate cluster"
echo ""
echo "🧹 To clean up:"
echo "   ./k8s/envoy-demo/cleanup.sh  # Or: kind delete cluster --name envoy-demo"
echo ""

# Made with Bob