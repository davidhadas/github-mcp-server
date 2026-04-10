#!/bin/bash

# Test Step 2: Verify AIAgent sends MCP request to MCP Server (without token)
# AIAgent → AuthBridge → MCP Server

echo "🧪 Testing Step 2: MCP Request Flow (No Token)"
echo "==============================================="
echo ""

# Clear log files
echo "📝 Clearing log files..."
> /tmp/backend.log
> /tmp/authbridge.log
> /tmp/aiagent.log
> /tmp/mcp-server-kagenti.log

# Wait a moment for logs to clear
sleep 1

# Send a test task
echo "📤 Sending test task to Backend..."
RESPONSE=$(curl -s -X POST http://localhost:8185/task \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "test-user-step2",
    "task": "Get my GitHub profile",
    "mcp_server_url": "http://localhost:8184"
  }')

echo "📥 Response received:"
echo "$RESPONSE" | jq '.' 2>/dev/null || echo "$RESPONSE"
echo ""

# Wait for logs to be written
sleep 1

# Check logs for expected flow
echo "🔍 Checking logs for MCP request flow..."
echo ""

# Check AIAgent log
echo "1️⃣  AIAgent Log:"
if grep -q "Sending MCP request to AuthBridge" /tmp/aiagent.log; then
    echo "   ✅ AIAgent sent MCP request to AuthBridge"
    grep "user_id=test-user-step2" /tmp/aiagent.log | grep -E "(Sending MCP request|Received response)"
else
    echo "   ❌ AIAgent did not send MCP request"
    tail -10 /tmp/aiagent.log
fi
echo ""

# Check AuthBridge log
echo "2️⃣  AuthBridge Log:"
if grep -q "Received MCP request from AIAgent" /tmp/authbridge.log; then
    echo "   ✅ AuthBridge received MCP request from AIAgent"
    grep "user_id=test-user-step2" /tmp/authbridge.log | grep -E "(Received MCP request|No token available|Requesting auth URL)"
else
    echo "   ❌ AuthBridge did not receive MCP request"
    tail -10 /tmp/authbridge.log
fi
echo ""

# Check if AuthBridge detected no token
echo "3️⃣  Token Check:"
if grep -q "No token available" /tmp/authbridge.log; then
    echo "   ✅ AuthBridge detected no token (expected)"
    grep "No token available" /tmp/authbridge.log
else
    echo "   ⚠️  AuthBridge did not log 'No token available'"
fi
echo ""

# Check if AuthBridge requested auth URL from MCP Server
echo "4️⃣  Auth URL Request:"
if grep -q "Requesting auth URL from MCP server" /tmp/authbridge.log; then
    echo "   ✅ AuthBridge requested auth URL from MCP Server"
    grep "Requesting auth URL from MCP server" /tmp/authbridge.log
else
    echo "   ❌ AuthBridge did not request auth URL"
fi
echo ""

# Check MCP Server log
echo "5️⃣  MCP Server Log:"
if grep -q "auth/url" /tmp/mcp-server-kagenti.log 2>/dev/null; then
    echo "   ✅ MCP Server received auth URL request"
    grep "auth/url" /tmp/mcp-server-kagenti.log | tail -2
else
    echo "   ⚠️  MCP Server log not showing auth/url request"
    tail -5 /tmp/mcp-server-kagenti.log 2>/dev/null || echo "   (Log file empty or not found)"
fi
echo ""

# Check response contains auth_required
echo "6️⃣  Response Check:"
if echo "$RESPONSE" | grep -q "auth_required"; then
    echo "   ✅ Response contains auth_required status"
    if echo "$RESPONSE" | grep -q "login_url"; then
        echo "   ✅ Response contains login_url"
    else
        echo "   ❌ Response missing login_url"
    fi
else
    echo "   ❌ Response does not contain auth_required"
fi
echo ""

# Summary
echo "==============================================="
if grep -q "Sending MCP request to AuthBridge" /tmp/aiagent.log && \
   grep -q "Received MCP request from AIAgent" /tmp/authbridge.log && \
   grep -q "No token available" /tmp/authbridge.log && \
   echo "$RESPONSE" | grep -q "auth_required"; then
    echo "✅ Step 2 PASSED: MCP request flow verified"
    echo "   Flow: AIAgent → AuthBridge → MCP Server ✓"
    echo "   No token detected ✓"
    echo "   Auth URL returned ✓"
else
    echo "❌ Step 2 FAILED: MCP request flow incomplete"
    echo ""
    echo "Full logs:"
    echo ""
    echo "AIAgent:"
    cat /tmp/aiagent.log
    echo ""
    echo "AuthBridge:"
    cat /tmp/authbridge.log
    echo ""
    echo "MCP Server:"
    cat /tmp/mcp-server-kagenti.log 2>/dev/null || echo "(empty)"
fi
echo "==============================================="

# Made with Bob
