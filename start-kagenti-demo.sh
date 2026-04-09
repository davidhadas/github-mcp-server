#!/bin/bash

# Start KAgentI Architecture Demo
# Browser → OAuth Coordinator (8185) → MCP Server (8184)

echo "🚀 Starting KAgentI Architecture Demo"
echo "======================================"
echo ""

# Check if binaries exist
if [ ! -f "./github-mcp-server" ]; then
    echo "❌ github-mcp-server binary not found. Please build it first:"
    echo "   go build -o github-mcp-server ./cmd/github-mcp-server"
    exit 1
fi

if [ ! -f "./cmd/oauth-coordinator/oauth-coordinator" ]; then
    echo "❌ oauth-coordinator binary not found. Building it now..."
    cd cmd/oauth-coordinator
    go build -o oauth-coordinator
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

# Start OAuth Coordinator (port 8185) - Provider-agnostic coordinator
echo ""
echo "2️⃣  Starting OAuth Coordinator (port 8185)..."
echo "   Role: Provider-agnostic session coordinator"
echo "   NO credentials, forwards OAuth ops to MCP servers"
cd cmd/oauth-coordinator
./oauth-coordinator > /tmp/oauth-coordinator.log 2>&1 &
OAUTH_PID=$!
echo $OAUTH_PID > /tmp/oauth-coordinator.pid
cd ../..
echo "   ✅ OAuth Coordinator started (PID: $OAUTH_PID)"
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
    echo "   ✅ OAuth Coordinator is responding on port 8185"
else
    echo "   ⚠️  OAuth Coordinator may not be ready yet"
fi

echo ""
echo "======================================"
echo "✅ KAgentI Architecture Demo Started!"
echo "======================================"
echo ""
echo "📊 Architecture:"
echo "   Browser → OAuth Coordinator (8185) → MCP Server (8184)"
echo ""
echo "🌐 Open in browser:"
echo "   http://localhost:8185/demo"
echo ""
echo "📝 Logs:"
echo "   MCP Server:        tail -f /tmp/mcp-server-kagenti.log"
echo "   OAuth Coordinator: tail -f /tmp/oauth-coordinator.log"
echo ""
echo "🛑 To stop:"
echo "   ./stop-kagenti-demo.sh"
echo ""
echo "💡 Key Differences from Original:"
echo "   - OAuth Coordinator is provider-agnostic (NO credentials)"
echo "   - MCP Server handles token exchange (HAS client_secret)"
echo "   - OAuth Coordinator forwards all OAuth ops to MCP servers"
echo "   - Demonstrates KAgentI multi-tier architecture"
echo ""

# Made with Bob
