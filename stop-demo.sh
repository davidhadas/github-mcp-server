#!/bin/bash
# Stop all demo services

echo "========================================"
echo "🛑 Stopping OAuth Demo Services"
echo "========================================"
echo ""

# Stop Demo Server
if [ -f /tmp/demo-server.pid ]; then
    DEMO_PID=$(cat /tmp/demo-server.pid)
    if ps -p $DEMO_PID > /dev/null 2>&1; then
        echo "Stopping Demo Server (PID: $DEMO_PID)..."
        kill $DEMO_PID 2>/dev/null || true
        rm /tmp/demo-server.pid
        echo "✓ Demo Server stopped"
    else
        echo "Demo Server not running"
        rm /tmp/demo-server.pid
    fi
else
    # Try to kill by port
    if lsof -ti:3000 >/dev/null 2>&1; then
        echo "Stopping Demo Server on port 3000..."
        lsof -ti:3000 | xargs kill -9 2>/dev/null || true
        echo "✓ Demo Server stopped"
    else
        echo "Demo Server not running"
    fi
fi

# Stop MCP Server
if [ -f /tmp/mcp-server.pid ]; then
    MCP_PID=$(cat /tmp/mcp-server.pid)
    if ps -p $MCP_PID > /dev/null 2>&1; then
        echo "Stopping MCP Server (PID: $MCP_PID)..."
        kill $MCP_PID 2>/dev/null || true
        rm /tmp/mcp-server.pid
        echo "✓ MCP Server stopped"
    else
        echo "MCP Server not running"
        rm /tmp/mcp-server.pid
    fi
else
    # Try to kill by port
    if lsof -ti:8180 >/dev/null 2>&1; then
        echo "Stopping MCP Server on port 8180..."
        lsof -ti:8180 | xargs kill -9 2>/dev/null || true
        echo "✓ MCP Server stopped"
    else
        echo "MCP Server not running"
    fi
fi

echo ""
echo "✅ All services stopped"
echo "========================================"

# Made with Bob
