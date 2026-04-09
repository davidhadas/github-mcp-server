#!/bin/bash
# Run the GitHub MCP Server OAuth Demo
# This script starts the server with configuration from config.yaml

echo "🚀 Starting GitHub MCP Server OAuth Demo..."
echo ""

# Kill any existing server processes
pkill -9 -f "github-mcp-server http" 2>/dev/null
sleep 1

# Start the server (config.yaml is automatically loaded by viper)
./github-mcp-server http &
SERVER_PID=$!

# Wait for server to start
sleep 3

# Check if server is running
if ps -p $SERVER_PID > /dev/null; then
    echo "✅ Server started successfully!"
    echo "   PID: $SERVER_PID"
    echo "   Port: 8184"
    echo "   Demo: http://localhost:8184/demo"
    echo ""
    echo "Press Ctrl+C to stop the server"
    echo ""
    
    # Keep script running and forward signals to server
    trap "kill $SERVER_PID 2>/dev/null" EXIT INT TERM
    wait $SERVER_PID
else
    echo "❌ Server failed to start. Check the logs for errors."
    exit 1
fi

# Made with Bob
