#!/bin/bash

# Build and Deploy KAgentI Demo to Kubernetes
# This script builds Docker images with the correct tags and deploys to k8s

set -e

# Change to repository root directory (parent of k8s/demo)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

echo "🚀 Building and Deploying KAgentI Demo"
echo "======================================"
echo "   Working directory: $REPO_ROOT"
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

# Recreate Kind cluster to ensure fresh start
echo "🔄 Recreating Kind cluster..."
kind delete cluster --name kagenti-demo 2>/dev/null || true
kind create cluster --name kagenti-demo --config - <<EOF
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
EOF
echo ""

# Generate build timestamp
BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
echo "🕐 Build timestamp: $BUILD_TIME"
echo ""

# Build Docker images with correct tags for demo
echo "🔨 Building Docker images..."
echo ""

echo "Building MCP Server..."
docker build -f Dockerfile -t localhost/kagenti/mcp-server:demo \
  --build-arg BUILD_TIME="$BUILD_TIME" .

echo "Building AI Agent..."
docker build -f Dockerfile.aiagent -t localhost/kagenti/aiagent:demo \
  --build-arg BUILD_TIME="$BUILD_TIME" .

echo "Building AuthBridge Sidecar..."
docker build -f Dockerfile.authbridge-sidecar -t localhost/kagenti/authbridge-sidecar:demo \
  --build-arg BUILD_TIME="$BUILD_TIME" .

echo "Building Backend..."
docker build -f Dockerfile.backend -t localhost/kagenti/backend:demo \
  --build-arg BUILD_TIME="$BUILD_TIME" .

echo ""
echo "✅ All images built successfully"
echo ""

# Load images into Kind cluster
echo "📦 Loading images into Kind cluster..."
kind load docker-image localhost/kagenti/mcp-server:demo --name kagenti-demo
kind load docker-image localhost/kagenti/aiagent:demo --name kagenti-demo
kind load docker-image localhost/kagenti/authbridge-sidecar:demo --name kagenti-demo
kind load docker-image localhost/kagenti/backend:demo --name kagenti-demo
echo "✅ Images loaded into Kind cluster"
echo ""

# Create namespace first (since we recreated the cluster)
echo "📦 Creating namespace..."
kubectl apply -f k8s/demo/00-namespace.yaml
echo ""

# Delete all existing resources to ensure clean state
echo "🧹 Cleaning up existing resources..."
kubectl delete deployment,service,configmap,secret -n kagenti-demo --all 2>/dev/null || true

echo ""
echo "🔐 Creating OAuth secret..."
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
echo "   ./k8s/demo/build-and-deploy.sh"
echo ""
echo "🧹 To clean up:"
echo "   pkill -f 'port-forward.*8187:8187'  # Stop port forwarding"
echo "   kind delete cluster --name kagenti-demo  # Delete cluster"
echo ""

# Made with Bob
