#!/bin/bash

# Stop KAgentI Architecture Demo (4 processes)

echo "🛑 Stopping KAgentI Architecture Demo"
echo "======================================"

# Stop Backend
if [ -f /tmp/backend.pid ]; then
    BACKEND_PID=$(cat /tmp/backend.pid)
    if kill -0 $BACKEND_PID 2>/dev/null; then
        echo "Stopping Backend (PID: $BACKEND_PID)..."
        kill $BACKEND_PID
        rm /tmp/backend.pid
    fi
fi

# Stop AuthBridge
if [ -f /tmp/authbridge-extension.pid ]; then
    AUTHBRIDGE_PID=$(cat /tmp/authbridge-extension.pid)
    if kill -0 $AUTHBRIDGE_PID 2>/dev/null; then
        echo "Stopping AuthBridge (PID: $AUTHBRIDGE_PID)..."
        kill $AUTHBRIDGE_PID
        rm /tmp/authbridge-extension.pid
    fi
fi

# Stop AI Agent
if [ -f /tmp/aiagent.pid ]; then
    AIAGENT_PID=$(cat /tmp/aiagent.pid)
    if kill -0 $AIAGENT_PID 2>/dev/null; then
        echo "Stopping AI Agent (PID: $AIAGENT_PID)..."
        kill $AIAGENT_PID
        rm /tmp/aiagent.pid
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
lsof -ti:8186 | xargs kill -9 2>/dev/null || true
lsof -ti:8187 | xargs kill -9 2>/dev/null || true

echo "✅ All services stopped"

# Made with Bob
