#!/bin/bash
# Start all services needed for the OAuth demo

set -e

echo "========================================"
echo "🚀 Starting OAuth Demo Services"
echo "========================================"
echo ""

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Check if MCP server is already running
if lsof -Pi :8180 -sTCP:LISTEN -t >/dev/null 2>&1 ; then
    echo -e "${GREEN}✓${NC} MCP Server already running on port 8180"
else
    echo -e "${BLUE}→${NC} Starting MCP Server on port 8180..."
    cd cmd/github-mcp-server
    nohup ./github-mcp-server http > /tmp/mcp-server.log 2>&1 &
    MCP_PID=$!
    echo $MCP_PID > /tmp/mcp-server.pid
    cd ../..
    sleep 2
    
    if lsof -Pi :8180 -sTCP:LISTEN -t >/dev/null 2>&1 ; then
        echo -e "${GREEN}✓${NC} MCP Server started (PID: $MCP_PID)"
    else
        echo -e "${YELLOW}⚠${NC}  MCP Server may not have started. Check /tmp/mcp-server.log"
    fi
fi

# Check if Demo server is already running
if lsof -Pi :3000 -sTCP:LISTEN -t >/dev/null 2>&1 ; then
    echo -e "${YELLOW}⚠${NC}  Demo Server already running on port 3000. Stopping it..."
    lsof -ti:3000 | xargs kill -9 2>/dev/null || true
    sleep 1
fi

echo -e "${BLUE}→${NC} Starting Demo Server on port 3000..."
nohup python3 start-demo-server.py > /tmp/demo-server.log 2>&1 &
DEMO_PID=$!
echo $DEMO_PID > /tmp/demo-server.pid
sleep 2

if lsof -Pi :3000 -sTCP:LISTEN -t >/dev/null 2>&1 ; then
    echo -e "${GREEN}✓${NC} Demo Server started (PID: $DEMO_PID)"
else
    echo -e "${YELLOW}⚠${NC}  Demo Server may not have started. Check /tmp/demo-server.log"
fi

echo ""
echo "========================================"
echo "✅ Services Started Successfully!"
echo "========================================"
echo ""
echo -e "${GREEN}MCP Server:${NC}  http://localhost:8180"
echo -e "${GREEN}Demo Page:${NC}   http://localhost:3000"
echo ""
echo "📋 Service Status:"
echo "  - MCP Server PID: $(cat /tmp/mcp-server.pid 2>/dev/null || echo 'N/A')"
echo "  - Demo Server PID: $(cat /tmp/demo-server.pid 2>/dev/null || echo 'N/A')"
echo ""
echo "📝 Logs:"
echo "  - MCP Server:  tail -f /tmp/mcp-server.log"
echo "  - Demo Server: tail -f /tmp/demo-server.log"
echo ""
echo "🛑 To stop services:"
echo "  ./stop-demo.sh"
echo ""
echo "🧪 To test the demo:"
echo "  1. Open http://localhost:3000 in your browser"
echo "  2. Click '🚀 Login with GitHub'"
echo "  3. Authorize the application"
echo "  4. You'll be automatically logged in!"
echo "  5. Click '📚 Get My Repositories' to test"
echo "  6. Click '🔧 Test MCP Tools List' to test MCP server"
echo ""
echo "========================================"

# Made with Bob
