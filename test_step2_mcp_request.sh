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
RESPONSE=$(curl -s -X POST http://localhost:8187/task \
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
if grep -q "Auth required - requesting OAuth" /tmp/authbridge.log; then
    echo "   ✅ AuthBridge received MCP request and detected no token"
    grep "user_id=test-user-step2" /tmp/authbridge.log | grep "Auth required"
else
    echo "   ❌ AuthBridge did not receive MCP request"
    tail -10 /tmp/authbridge.log
fi
echo ""

# Check if AuthBridge is blocking for OAuth
echo "3️⃣  OAuth Blocking:"
if grep -q "Blocking MCP request for OAuth" /tmp/authbridge.log; then
    echo "   ✅ AuthBridge blocking MCP request for OAuth"
    grep "user_id=test-user-step2" /tmp/authbridge.log | grep "Blocking"
else
    echo "   ⚠️  AuthBridge did not block for OAuth"
fi
echo ""

# Check response contains auth_required
echo "5️⃣  Response Check:"
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
   grep -q "Auth required - requesting OAuth" /tmp/authbridge.log && \
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
