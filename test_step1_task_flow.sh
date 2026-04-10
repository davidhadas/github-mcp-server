#!/bin/bash

# Test Step 1: Verify task reaches AIAgent
# Frontend → Backend → AuthBridge → AIAgent

echo "🧪 Testing Step 1: Task Flow to AIAgent"
echo "========================================"
echo ""

# Clear log files
echo "📝 Clearing log files..."
> /tmp/backend.log
> /tmp/authbridge.log
> /tmp/aiagent.log

# Wait a moment for logs to clear
sleep 1

# Send a test task
echo "📤 Sending test task to Backend..."
RESPONSE=$(curl -s -X POST http://localhost:8185/task \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "test-user-step1",
    "task": "Get my GitHub profile",
    "mcp_server_url": "http://localhost:8184"
  }')

echo "📥 Response received:"
echo "$RESPONSE" | jq '.' 2>/dev/null || echo "$RESPONSE"
echo ""

# Wait for logs to be written
sleep 1

# Check logs for expected flow
echo "🔍 Checking logs for task flow..."
echo ""

# Check Backend log
echo "1️⃣  Backend Log:"
if grep -q "Received task from browser" /tmp/backend.log && \
   grep -q "Forwarding task to AuthBridge" /tmp/backend.log; then
    echo "   ✅ Backend received and forwarded task"
    grep "user_id=test-user-step1" /tmp/backend.log | head -2
else
    echo "   ❌ Backend did not process task correctly"
    tail -5 /tmp/backend.log
fi
echo ""

# Check AuthBridge log
echo "2️⃣  AuthBridge Log:"
if grep -q "Received task request from Backend" /tmp/authbridge.log && \
   grep -q "Forwarding task to AIAgent" /tmp/authbridge.log; then
    echo "   ✅ AuthBridge received and forwarded to AIAgent"
    grep "user_id=test-user-step1" /tmp/authbridge.log | head -2
else
    echo "   ❌ AuthBridge did not process task correctly"
    tail -5 /tmp/authbridge.log
fi
echo ""

# Check AIAgent log
echo "3️⃣  AIAgent Log:"
if grep -q "Received task from browser" /tmp/aiagent.log; then
    echo "   ✅ AIAgent received task!"
    grep "user_id=test-user-step1" /tmp/aiagent.log | head -3
else
    echo "   ❌ AIAgent did not receive task"
    tail -5 /tmp/aiagent.log
fi
echo ""

# Summary
echo "========================================"
if grep -q "Received task from browser" /tmp/aiagent.log; then
    echo "✅ Step 1 PASSED: Task successfully reached AIAgent"
    echo "   Flow: Frontend → Backend → AuthBridge → AIAgent ✓"
else
    echo "❌ Step 1 FAILED: Task did not reach AIAgent"
    echo ""
    echo "Full logs:"
    echo "Backend:"
    cat /tmp/backend.log
    echo ""
    echo "AuthBridge:"
    cat /tmp/authbridge.log
    echo ""
    echo "AIAgent:"
    cat /tmp/aiagent.log
fi
echo "========================================"

# Made with Bob
