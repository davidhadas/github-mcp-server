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

if [ ! -f "./cmd/aiagent/aiagent" ]; then
    echo "❌ aiagent binary not found. Building it now..."
    cd cmd/aiagent
    go build -o aiagent
    cd ../..
fi

# Kill any existing processes on these ports
echo "🧹 Cleaning up existing processes..."
lsof -ti:8184 | xargs kill -9 2>/dev/null || true
lsof -ti:8185 | xargs kill -9 2>/dev/null || true
lsof -ti:8186 | xargs kill -9 2>/dev/null || true
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

# Start AI Agent (port 8186) - Separate process
echo ""
echo "2️⃣  Starting AI Agent (port 8186)..."
echo "   Role: Task orchestration and MCP request conversion"
echo "   Separate process, OAuth-agnostic"
cd cmd/aiagent
./aiagent > /tmp/aiagent.log 2>&1 &
AIAGENT_PID=$!
echo $AIAGENT_PID > /tmp/aiagent.pid
cd ../..
echo "   ✅ AI Agent started (PID: $AIAGENT_PID)"
sleep 2

# Start AuthBridge Extension (port 8185) - Provider-agnostic coordinator
echo ""
echo "3️⃣  Starting AuthBridge Extension (port 8185)..."
echo "   Role: Provider-agnostic OAuth coordinator + MCP proxy"
echo "   NO credentials, forwards OAuth ops to MCP servers"
echo "   Wraps AI Agent as sidecar"
cd cmd/authbridge-extension
./authbridge-extension > /tmp/authbridge-extension.log 2>&1 &
AUTHBRIDGE_PID=$!
echo $AUTHBRIDGE_PID > /tmp/authbridge-extension.pid
cd ../..
echo "   ✅ AuthBridge Extension started (PID: $AUTHBRIDGE_PID)"
sleep 1

# Check if services are running
echo ""
echo "🔍 Checking services..."
if curl -s http://localhost:8184/health > /dev/null 2>&1; then
    echo "   ✅ MCP Server is responding on port 8184"
else
    echo "   ⚠️  MCP Server may not be ready yet"
fi

if curl -s http://localhost:8186/health > /dev/null 2>&1; then
    echo "   ✅ AI Agent is responding on port 8186"
else
    echo "   ⚠️  AI Agent may not be ready yet"
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
echo "📊 Architecture (KAgentI-style):"
echo "   Browser → Backend → AuthBridge (wrapper) → AI Agent (8186) → AuthBridge MCP Proxy → MCP Server (8184)"
echo "   3 separate processes: MCP Server, AI Agent, AuthBridge+Backend"
echo "   AuthBridge wraps AI Agent as sidecar (like Envoy in K8s)"
echo ""
echo "🌐 Open in browser:"
echo "   http://localhost:8185/demo"
echo ""
echo "📝 Component Logs:"
echo "   MCP Server:  tail -f /tmp/mcp-server-kagenti.log"
echo "   AI Agent:    tail -f /tmp/aiagent.log"
echo "   Backend:     tail -f /tmp/backend.log"
echo "   AuthBridge:  tail -f /tmp/authbridge.log"
echo ""
echo "🛑 To stop:"
echo "   ./stop-kagenti-demo.sh"
echo ""
echo "💡 Key Features:"
echo "   - Task-based API: Browser sends natural language tasks"
echo "   - AI Agent: Separate process, OAuth-agnostic (port 8186)"
echo "   - AuthBridge: Wraps AI Agent, handles OAuth (port 8185)"
echo "   - MCP Proxy: AuthBridge intercepts AI Agent's MCP requests"
echo "   - MCP Server: Handles token exchange (HAS client_secret)"
echo "   - True KAgentI architecture: AuthBridge as sidecar to AI Agent"
echo ""

# Made with Bob
