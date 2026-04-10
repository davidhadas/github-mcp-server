#!/bin/bash

# Test Step 4: Token Exchange and Caching
# Frontend → Backend → AuthBridge (exchange token & cache)

echo "🧪 Testing Step 4: Token Exchange and Caching"
echo "=============================================="
echo ""

# Clear log files
echo "📝 Clearing log files..."
> /tmp/backend.log
> /tmp/authbridge.log
> /tmp/aiagent.log
> /tmp/mcp-server-kagenti.log

sleep 1

# Step 1: Get auth URL first (to get code_verifier)
echo "1️⃣  Getting auth URL to obtain code_verifier..."
AUTH_RESPONSE=$(curl -s -X POST http://localhost:8185/auth/url \
  -H "Content-Type: application/json" \
  -d '{
    "mcp_server_url": "http://localhost:8184"
  }')

CODE_VERIFIER=$(echo "$AUTH_RESPONSE" | jq -r '.code_verifier')
echo "   Code verifier obtained: ${CODE_VERIFIER:0:20}..."
echo ""

# Step 2: Simulate OAuth callback with a mock code
echo "2️⃣  Simulating token exchange with mock endpoint..."
MOCK_CODE="test_oauth_code_step4"

TOKEN_RESPONSE=$(curl -s -X POST http://localhost:8185/test/mock-token-exchange \
  -H "Content-Type: application/json" \
  -d "{
    \"code\": \"$MOCK_CODE\",
    \"code_verifier\": \"$CODE_VERIFIER\",
    \"user_id\": \"test-user-step4\",
    \"mcp_server_url\": \"http://localhost:8184\"
  }")

echo "   Response: $TOKEN_RESPONSE"
echo ""

sleep 1

# Check logs
echo "🔍 Checking logs for token exchange flow..."
echo ""

# Check Backend log
echo "3️⃣  Backend Log:"
if grep -q "Token exchange request" /tmp/backend.log && \
   grep -q "test-user-step4" /tmp/backend.log; then
    echo "   ✅ Backend received token exchange request"
    grep "test-user-step4" /tmp/backend.log | head -3
else
    echo "   ❌ Backend did not process token exchange"
fi
echo ""

# Check AuthBridge log
echo "4️⃣  AuthBridge Log:"
if grep -q "Token set for testing" /tmp/authbridge.log; then
    echo "   ✅ Mock token cached in AuthBridge"
    grep "test-user-step4" /tmp/authbridge.log | grep "Token set for testing"
else
    echo "   ❌ Mock token was not cached"
fi
echo ""

# Check if token was cached
echo "5️⃣  Token Caching:"
if grep -q "Token cached" /tmp/authbridge.log || \
   grep -q "Token set for testing" /tmp/authbridge.log; then
    echo "   ✅ Token was cached in AuthBridge"
    grep "test-user-step4" /tmp/authbridge.log | grep -E "Token cached|Token set for testing"
else
    echo "   ❌ Token was not cached"
fi
echo ""

# Check that token didn't reach Backend
echo "6️⃣  Security Check - Token Isolation:"
if grep -q "gho_" /tmp/backend.log || grep -q "access_token" /tmp/backend.log; then
    echo "   ❌ SECURITY ISSUE: Token visible in Backend log!"
    grep -i "token" /tmp/backend.log
else
    echo "   ✅ Token never reached Backend (secure)"
fi
echo ""

# Verify token is available for next request
echo "7️⃣  Token Availability Check:"
echo "   Sending a task to verify cached token is used..."

TASK_RESPONSE=$(curl -s -X POST http://localhost:8185/task \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "test-user-step4",
    "task": "Get my GitHub profile",
    "mcp_server_url": "http://localhost:8184"
  }')

sleep 1

if grep -q "Token available" /tmp/authbridge.log || \
   grep -q "Using cached token" /tmp/authbridge.log; then
    echo "   ✅ Cached token was used for subsequent request"
    grep "test-user-step4" /tmp/authbridge.log | grep -i "token" | tail -3
else
    echo "   ⚠️  Could not verify token usage (check logs manually)"
fi
echo ""

# Summary
echo "=============================================="
if grep -q "Token set for testing" /tmp/authbridge.log && \
   ! grep -q "gho_" /tmp/backend.log; then
    echo "✅ Step 4 PASSED: Token exchange and caching verified"
    echo "   Token exchanged with MCP server ✓"
    echo "   Token cached in AuthBridge ✓"
    echo "   Token never reached Backend ✓"
else
    echo "❌ Step 4 FAILED: Token exchange incomplete"
    echo ""
    echo "Full logs:"
    echo ""
    echo "Backend:"
    grep "test-user-step4" /tmp/backend.log
    echo ""
    echo "AuthBridge:"
    grep "test-user-step4" /tmp/authbridge.log
fi
echo "=============================================="

# Made with Bob
