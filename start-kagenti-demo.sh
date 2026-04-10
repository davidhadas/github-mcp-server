#!/bin/bash

# Start KAgentI Architecture Demo (4 processes)
# Browser → Backend (8187) → AuthBridge (8185) → AI Agent (8186) → MCP Server (8184)

echo "🚀 Starting KAgentI Architecture Demo (4 Processes)"
echo "===================================================="
echo ""

# Check if binaries exist
if [ ! -f "./github-mcp-server" ]; then
    echo "❌ github-mcp-server binary not found. Please build it first:"
    echo "   go build -o github-mcp-server ./cmd/github-mcp-server"
    exit 1
fi

if [ ! -f "./backend" ]; then
    echo "❌ backend binary not found. Building it now..."
    go build -o backend ./cmd/backend
fi

if [ ! -f "./authbridge-extension" ]; then
    echo "❌ authbridge-extension binary not found. Building it now..."
    go build -o authbridge-extension ./cmd/authbridge-extension
fi

if [ ! -f "./aiagent" ]; then
    echo "❌ aiagent binary not found. Building it now..."
    go build -o aiagent ./cmd/aiagent
fi

# Kill any existing processes on these ports
echo "🧹 Cleaning up existing processes..."
lsof -ti :8184 | xargs kill -9 2>/dev/null || true
lsof -ti :8185 | xargs kill -9 2>/dev/null || true
lsof -ti :8186 | xargs kill -9 2>/dev/null || true
lsof -ti :8187 | xargs kill -9 2>/dev/null || true
sleep 2

# Start MCP Server (port 8184) - WITH token exchange capability
echo ""
echo "1️⃣  Starting MCP Server (port 8184)..."
echo "   Role: OAuth discovery + token exchange + MCP protocol"
echo "   HAS client_secret, handles token exchange"
./github-mcp-server http --port 8184 --oauth-client-id "Ov23lipjfVegsSqldX5H" --oauth-client-secret "751307b6ab9db77b4ff429e918db80b70770ed89" --oauth-redirect-uri "http://localhost:8187/callback" > /tmp/mcp-server-kagenti.log 2>&1 &
MCP_PID=$!
echo $MCP_PID > /tmp/mcp-server-kagenti.pid
echo "   ✅ MCP Server started (PID: $MCP_PID)"
sleep 2

# Start AI Agent (port 8186) - Separate process
echo ""
echo "2️⃣  Starting AI Agent (port 8186)..."
echo "   Role: Task orchestration and MCP request conversion"
echo "   Separate process, OAuth-agnostic"
./aiagent > /tmp/aiagent.log 2>&1 &
AIAGENT_PID=$!
echo $AIAGENT_PID > /tmp/aiagent.pid
echo "   ✅ AI Agent started (PID: $AIAGENT_PID)"
sleep 2

# Start AuthBridge (port 8185) - OAuth coordinator and MCP proxy
echo ""
echo "3️⃣  Starting AuthBridge (port 8185)..."
echo "   Role: OAuth coordinator + MCP proxy for AI Agent"
echo "   NO credentials, forwards OAuth ops to MCP servers"
echo "   Wraps AI Agent as sidecar"
./authbridge-extension > /tmp/authbridge.log 2>&1 &
AUTHBRIDGE_PID=$!
echo $AUTHBRIDGE_PID > /tmp/authbridge.pid
echo "   ✅ AuthBridge started (PID: $AUTHBRIDGE_PID)"
sleep 2

# Start Backend (port 8187) - Frontend interface
echo ""
echo "4️⃣  Starting Backend (port 8187)..."
echo "   Role: Frontend interface, serves demo page"
echo "   Forwards tasks to AuthBridge"
echo "   Handles OAuth callbacks"
./backend > /tmp/backend.log 2>&1 &
BACKEND_PID=$!
echo $BACKEND_PID > /tmp/backend.pid
echo "   ✅ Backend started (PID: $BACKEND_PID)"
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
    echo "   ✅ AuthBridge is responding on port 8185"
else
    echo "   ⚠️  AuthBridge may not be ready yet"
fi

if curl -s http://localhost:8187/health > /dev/null 2>&1; then
    echo "   ✅ Backend is responding on port 8187"
else
    echo "   ⚠️  Backend may not be ready yet"
fi

echo ""
echo "===================================================="
echo "✅ KAgentI Architecture Demo Started (4 Processes)!"
echo "===================================================="
echo ""
echo "📊 Architecture:"
echo "   Browser → Backend:8187 → AuthBridge:8185 → AI Agent:8186 → AuthBridge MCP Proxy → MCP Server:8184"
echo "   4 separate processes with clear separation of concerns"
echo ""
echo "🌐 Open in browser:"
echo "   http://localhost:8187/demo"
echo ""
echo "📝 Component Logs:"
echo "   MCP Server:  tail -f /tmp/mcp-server-kagenti.log"
echo "   AI Agent:    tail -f /tmp/aiagent.log"
echo "   AuthBridge:  tail -f /tmp/authbridge.log"
echo "   Backend:     tail -f /tmp/backend.log"
echo ""
echo "🛑 To stop:"
echo "   ./stop-kagenti-demo.sh"
echo ""
echo "💡 Key Features:"
echo "   - 4-process architecture: Backend, AuthBridge, AI Agent, MCP Server"
echo "   - Backend: Serves frontend, handles OAuth callbacks"
echo "   - AuthBridge: OAuth coordination, token caching, MCP proxy"
echo "   - AI Agent: Task orchestration, completely OAuth-agnostic"
echo "   - MCP Server: OAuth discovery, token exchange, MCP protocol"
echo "   - Blocking OAuth flow: AI Agent never sees 401 errors"
echo ""

# Made with Bob
