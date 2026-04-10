#!/bin/bash

# Test Step 3: Verify MCP Server elicitation and AuthBridge OAuth discovery
# MCP Server → AuthBridge (elicitation)
# AuthBridge performs OAuth URL discovery

echo "🧪 Testing Step 3: OAuth URL Discovery"
echo "======================================="
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
    "user_id": "test-user-step3",
    "task": "Get my GitHub profile",
    "mcp_server_url": "http://localhost:8184"
  }')

echo "📥 Response received"
echo ""

# Wait for logs to be written
sleep 1

# Check logs for OAuth discovery flow
echo "🔍 Checking logs for OAuth discovery flow..."
echo ""

# Check AuthBridge detects no token
echo "1️⃣  AuthBridge - Token Check:"
if grep -q "No token available" /tmp/authbridge.log; then
    echo "   ✅ AuthBridge detected no token"
    grep "No token available" /tmp/authbridge.log | head -1
else
    echo "   ❌ AuthBridge did not detect missing token"
fi
echo ""

# Check AuthBridge requests auth URL from MCP Server
echo "2️⃣  AuthBridge - Auth URL Request:"
if grep -q "Requesting auth URL from MCP server" /tmp/authbridge.log; then
    echo "   ✅ AuthBridge requested auth URL from MCP Server"
    grep "Requesting auth URL from MCP server" /tmp/authbridge.log
else
    echo "   ❌ AuthBridge did not request auth URL"
    echo "   AuthBridge log:"
    grep "user_id=test-user-step3" /tmp/authbridge.log
fi
echo ""

# Check MCP Server received auth URL request
echo "3️⃣  MCP Server - Received Request:"
if grep -qi "auth.*url" /tmp/mcp-server-kagenti.log 2>/dev/null; then
    echo "   ✅ MCP Server received auth URL request"
    grep -i "auth" /tmp/mcp-server-kagenti.log | tail -3
else
    echo "   ⚠️  MCP Server log not showing auth URL request"
    echo "   (This might be normal if MCP server doesn't log at INFO level)"
fi
echo ""

# Check AuthBridge received auth URL from MCP Server
echo "4️⃣  AuthBridge - Received Auth URL:"
if grep -q "Auth URL obtained from MCP server" /tmp/authbridge.log; then
    echo "   ✅ AuthBridge obtained auth URL from MCP Server"
    grep "Auth URL obtained from MCP server" /tmp/authbridge.log
else
    echo "   ❌ AuthBridge did not obtain auth URL"
fi
echo ""

# Check AuthBridge returns auth required to AIAgent
echo "5️⃣  AuthBridge → AIAgent Response:"
if grep -q "Returning auth required to AIAgent" /tmp/authbridge.log; then
    echo "   ✅ AuthBridge returned auth_required to AIAgent"
    grep "Returning auth required to AIAgent" /tmp/authbridge.log
else
    echo "   ⚠️  AuthBridge log doesn't show explicit return to AIAgent"
    echo "   (Checking if response was sent...)"
fi
echo ""

# Check response contains GitHub OAuth URL
echo "6️⃣  Response - OAuth URL:"
if echo "$RESPONSE" | grep -q "github.com/login/oauth/authorize"; then
    echo "   ✅ Response contains GitHub OAuth URL"
    LOGIN_URL=$(echo "$RESPONSE" | jq -r '.result.login_url' 2>/dev/null)
    if [[ $LOGIN_URL == https://github.com/login/oauth/authorize* ]]; then
        echo "   ✅ URL format correct: ${LOGIN_URL:0:60}..."
        # Check for required OAuth parameters
        if echo "$LOGIN_URL" | grep -q "client_id="; then
            echo "   ✅ Contains client_id"
        fi
        if echo "$LOGIN_URL" | grep -q "code_challenge="; then
            echo "   ✅ Contains code_challenge (PKCE)"
        fi
        if echo "$LOGIN_URL" | grep -q "redirect_uri="; then
            echo "   ✅ Contains redirect_uri"
        fi
    fi
else
    echo "   ❌ Response does not contain GitHub OAuth URL"
    echo "   Response: $RESPONSE"
fi
echo ""

# Summary
echo "======================================="
if grep -q "No token available" /tmp/authbridge.log && \
   grep -q "Auth URL obtained from MCP server" /tmp/authbridge.log && \
   echo "$RESPONSE" | grep -q "github.com/login/oauth/authorize"; then
    echo "✅ Step 3 PASSED: OAuth discovery verified"
    echo "   MCP Server elicitation detected ✓"
    echo "   AuthBridge performed OAuth URL discovery ✓"
    echo "   Valid GitHub OAuth URL returned ✓"
else
    echo "❌ Step 3 FAILED: OAuth discovery incomplete"
    echo ""
    echo "Full AuthBridge log:"
    cat /tmp/authbridge.log
fi
echo "======================================="

# Made with Bob
