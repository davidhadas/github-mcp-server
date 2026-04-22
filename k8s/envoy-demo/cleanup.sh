#!/bin/bash
# Cleanup script for Kagenti Envoy Demo
# This script removes all deployed components

set -e

NAMESPACE="kagenti-envoy-demo"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "=========================================="
echo "Kagenti Envoy Demo Cleanup"
echo "=========================================="
echo ""

# Check if kubectl is available
if ! command -v kubectl &> /dev/null; then
    echo "Error: kubectl is not installed or not in PATH"
    exit 1
fi

# Check if namespace exists
if ! kubectl get namespace $NAMESPACE &> /dev/null; then
    echo "Namespace $NAMESPACE does not exist. Nothing to clean up."
    exit 0
fi

echo "This will delete all resources in namespace: $NAMESPACE"
read -p "Are you sure? (yes/no): " -r
echo
if [[ ! $REPLY =~ ^[Yy][Ee][Ss]$ ]]; then
    echo "Cleanup cancelled."
    exit 0
fi

echo "Deleting all resources in namespace $NAMESPACE..."
echo ""

# Delete in reverse order
echo "Deleting Backend..."
kubectl delete -f "$SCRIPT_DIR/05-backend.yaml" --ignore-not-found=true

echo "Deleting AI Agent..."
kubectl delete -f "$SCRIPT_DIR/04-aiagent.yaml" --ignore-not-found=true

echo "Deleting Envoy ConfigMap..."
kubectl delete -f "$SCRIPT_DIR/03-envoy-config.yaml" --ignore-not-found=true

echo "Deleting MCP Server..."
kubectl delete -f "$SCRIPT_DIR/02-mcp-server.yaml" --ignore-not-found=true

echo "Deleting Token Broker..."
kubectl delete -f "$SCRIPT_DIR/01-token-broker.yaml" --ignore-not-found=true

echo "Deleting namespace..."
kubectl delete -f "$SCRIPT_DIR/00-namespace.yaml" --ignore-not-found=true

echo ""
echo "=========================================="
echo "Cleanup Complete!"
echo "=========================================="
echo ""
echo "All resources in namespace $NAMESPACE have been deleted."

# Made with Bob
