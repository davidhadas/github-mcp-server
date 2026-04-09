#!/bin/bash

# Stop KAgentI Architecture Demo

echo "🛑 Stopping KAgentI Architecture Demo"
echo "======================================"

# Stop OAuth Coordinator
if [ -f /tmp/oauth-coordinator.pid ]; then
    OAUTH_PID=$(cat /tmp/oauth-coordinator.pid)
    if kill -0 $OAUTH_PID 2>/dev/null; then
        echo "Stopping OAuth Coordinator (PID: $OAUTH_PID)..."
        kill $OAUTH_PID
        rm /tmp/oauth-coordinator.pid
    fi
fi

# Stop MCP Server
if [ -f /tmp/mcp-server-kagenti.pid ]; then
    MCP_PID=$(cat /tmp/mcp-server-kagenti.pid)
    if kill -0 $MCP_PID 2>/dev/null; then
        echo "Stopping MCP Server (PID: $MCP_PID)..."
        kill $MCP_PID
        rm /tmp/mcp-server-kagenti.pid
    fi
fi

# Kill any remaining processes on these ports
lsof -ti:8184 | xargs kill -9 2>/dev/null || true
lsof -ti:8185 | xargs kill -9 2>/dev/null || true

echo "✅ All services stopped"

# Made with Bob
