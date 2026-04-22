#!/bin/bash
# Deployment script for Kagenti Envoy Demo
# This script deploys all components in the correct order

set -e

NAMESPACE="kagenti-envoy-demo"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=========================================="
echo "Kagenti Envoy Demo Deployment"
echo "=========================================="
echo ""

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    echo "Error: kubectl is not installed or not in PATH"
    exit 1
fi

# Function to wait for deployment to be ready
wait_for_deployment() {
    local deployment=$1
    local namespace=$2
    echo "Waiting for deployment/$deployment to be ready..."
    kubectl wait --for=condition=available --timeout=120s \
        deployment/$deployment -n $namespace || {
        echo "Warning: Deployment $deployment did not become ready in time"
        kubectl get pods -n $namespace -l app=$deployment
        return 1
    }
    echo "✓ Deployment $deployment is ready"
}

# Step 1: Create namespace
echo "Step 1: Creating namespace..."
kubectl apply -f "$SCRIPT_DIR/00-namespace.yaml"
echo "✓ Namespace created"
echo ""

# Step 2: Deploy Token Broker
echo "Step 2: Deploying Token Broker..."
kubectl apply -f "$SCRIPT_DIR/01-token-broker.yaml"
wait_for_deployment "token-broker" "$NAMESPACE"
echo ""

# Step 3: Deploy MCP Server
echo "Step 3: Deploying MCP Server..."
kubectl apply -f "$SCRIPT_DIR/02-mcp-server.yaml"
wait_for_deployment "mcp-server" "$NAMESPACE"
echo ""

# Step 4: Deploy Envoy ConfigMap
echo "Step 4: Deploying Envoy ConfigMap..."
kubectl apply -f "$SCRIPT_DIR/03-envoy-config.yaml"
echo "✓ Envoy ConfigMap created"
echo ""

# Step 5: Deploy AI Agent with Envoy + AuthBridge sidecars
echo "Step 5: Deploying AI Agent with Envoy + AuthBridge sidecars..."
kubectl apply -f "$SCRIPT_DIR/04-aiagent.yaml"
wait_for_deployment "aiagent" "$NAMESPACE"
echo ""

# Step 6: Deploy Backend (placeholder)
echo "Step 6: Deploying Backend (placeholder for Phase 3)..."
kubectl apply -f "$SCRIPT_DIR/05-backend.yaml"
echo "✓ Backend deployment created (not yet functional)"
echo ""

# Display status
echo "=========================================="
echo "Deployment Complete!"
echo "=========================================="
echo ""
echo "Checking pod status..."
kubectl get pods -n $NAMESPACE
echo ""
echo "Checking services..."
kubectl get svc -n $NAMESPACE
echo ""

# Display access information
echo "=========================================="
echo "Access Information"
echo "=========================================="
echo ""
echo "Token Broker:"
echo "  Internal: http://token-broker-service:8190"
echo "  Port-forward: kubectl port-forward -n $NAMESPACE svc/token-broker-service 8190:8190"
echo ""
echo "MCP Server:"
echo "  Internal: http://mcp-server-service:8184"
echo "  NodePort: http://localhost:30184 (if using kind/minikube)"
echo ""
echo "AI Agent (via Envoy):"
echo "  Internal: http://aiagent-service:8186"
echo "  NodePort: http://localhost:30186 (if using kind/minikube)"
echo ""
echo "Backend (Phase 3):"
echo "  Internal: http://backend-service:8187"
echo "  NodePort: http://localhost:30187 (if using kind/minikube)"
echo ""

# Display next steps
echo "=========================================="
echo "Next Steps"
echo "=========================================="
echo ""
echo "1. Test Token Broker health:"
echo "   kubectl port-forward -n $NAMESPACE svc/token-broker-service 8190:8190"
echo "   curl http://localhost:8190/health"
echo ""
echo "2. Create a session:"
echo "   curl -X POST http://localhost:8190/sessions -H 'X-User-ID: test-user'"
echo ""
echo "3. Check AuthBridge logs:"
echo "   kubectl logs -n $NAMESPACE -l app=aiagent -c authbridge -f"
echo ""
echo "4. Check Envoy admin interface:"
echo "   kubectl port-forward -n $NAMESPACE -l app=aiagent 9901:9901"
echo "   curl http://localhost:9901/stats | grep authbridge"
echo ""
echo "5. View logs:"
echo "   kubectl logs -n $NAMESPACE -l app=token-broker -f"
echo "   kubectl logs -n $NAMESPACE -l app=aiagent -c authbridge -f"
echo "   kubectl logs -n $NAMESPACE -l app=aiagent -c envoy -f"
echo "   kubectl logs -n $NAMESPACE -l app=aiagent -c aiagent -f"
echo ""
echo "For more information, see k8s/envoy-demo/README.md"

# Made with Bob
