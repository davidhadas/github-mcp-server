#!/bin/bash

# Start KAgentI Architecture Demo
# Browser → AuthBridge Extension (8185) → MCP Server (8184)

echo "🚀 Starting KAgentI Architecture Demo"
echo "======================================"
echo ""

# Check if binaries exist
if [ ! -f "./github-mcp-server" ]; then
    echo "❌ github-mcp-server binary not found. Please build it first:"
    echo "   go build -o github-mcp-server ./cmd/github-mcp-server"
    exit 1
fi

if [ ! -f "./cmd/authbridge-extension/authbridge-extension" ]; then
    echo "❌ authbridge-extension binary not found. Building it now..."
    cd cmd/authbridge-extension
    go build -o authbridge-extension
    cd ../..
fi

# Kill any existing processes on these ports
echo "🧹 Cleaning up existing processes..."
lsof -ti:8184 | xargs kill -9 2>/dev/null || true
lsof -ti:8185 | xargs kill -9 2>/dev/null || true
sleep 1

# Start MCP Server (port 8184) - WITH token exchange capability
echo ""
echo "1️⃣  Starting MCP Server (port 8184)..."
echo "   Role: OAuth discovery + token exchange + MCP protocol"
echo "   HAS client_secret, handles token exchange"
./github-mcp-server http > /tmp/mcp-server-kagenti.log 2>&1 &
MCP_PID=$!
echo $MCP_PID > /tmp/mcp-server-kagenti.pid
echo "   ✅ MCP Server started (PID: $MCP_PID)"
sleep 2

# Start AuthBridge Extension (port 8185) - Provider-agnostic coordinator with AI Agent support
echo ""
echo "2️⃣  Starting AuthBridge Extension (port 8185)..."
echo "   Role: Provider-agnostic session coordinator with AI Agent support"
echo "   NO credentials, forwards OAuth ops to MCP servers"
cd cmd/authbridge-extension
./authbridge-extension > /tmp/authbridge-extension.log 2>&1 &
AUTHBRIDGE_PID=$!
echo $AUTHBRIDGE_PID > /tmp/authbridge-extension.pid
cd ../..
echo "   ✅ AuthBridge Extension started (PID: $AUTHBRIDGE_PID)"
sleep 2

# Check if services are running
echo ""
echo "🔍 Checking services..."
if curl -s http://localhost:8184/health > /dev/null 2>&1; then
    echo "   ✅ MCP Server is responding on port 8184"
else
    echo "   ⚠️  MCP Server may not be ready yet"
fi

if curl -s http://localhost:8185/health > /dev/null 2>&1; then
    echo "   ✅ AuthBridge Extension is responding on port 8185"
else
    echo "   ⚠️  AuthBridge Extension may not be ready yet"
fi

echo ""
echo "======================================"
echo "✅ KAgentI Architecture Demo Started!"
echo "======================================"
echo ""
echo "📊 Architecture:"
echo "   Browser → AuthBridge Extension (8185) → MCP Server (8184)"
echo "   AuthBridge wraps AI Agent interactions with MCP servers"
echo ""
echo "🌐 Open in browser:"
echo "   http://localhost:8185/demo"
echo ""
echo "📝 Logs:"
echo "   MCP Server:          tail -f /tmp/mcp-server-kagenti.log"
echo "   AuthBridge Extension: tail -f /tmp/authbridge-extension.log"
echo ""
echo "🛑 To stop:"
echo "   ./stop-kagenti-demo.sh"
echo ""
echo "💡 Key Features:"
echo "   - AuthBridge Extension is provider-agnostic (NO credentials)"
echo "   - MCP Server handles token exchange (HAS client_secret)"
echo "   - AuthBridge forwards all OAuth ops to MCP servers"
echo "   - Includes AI Agent component for task orchestration"
echo "   - Demonstrates KAgentI multi-tier architecture"
echo ""

# Made with Bob
