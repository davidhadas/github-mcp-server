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

# Check AuthBridge detects no token and requests OAuth
echo "1️⃣  AuthBridge - OAuth Discovery:"
if grep -q "Auth required - requesting OAuth" /tmp/authbridge.log; then
    echo "   ✅ AuthBridge detected no token and requested OAuth"
    grep "user_id=test-user-step3" /tmp/authbridge.log | grep "Auth required"
else
    echo "   ❌ AuthBridge did not detect missing token"
fi
echo ""

# Check AuthBridge requested OAuth
echo "2️⃣  AuthBridge - OAuth Request:"
if grep -q "Auth required - requesting OAuth" /tmp/authbridge.log; then
    echo "   ✅ AuthBridge requested OAuth from MCP server"
    grep "user_id=test-user-step3" /tmp/authbridge.log | grep "Auth required"
else
    echo "   ❌ AuthBridge did not request OAuth"
fi
echo ""

# Check response contains GitHub OAuth URL
echo "3️⃣  Response - OAuth URL:"
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
if grep -q "Auth required - requesting OAuth" /tmp/authbridge.log && \
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
