#!/bin/bash
# Test script for token exchange endpoint

echo "Testing token exchange endpoint..."
echo ""

# Test with a dummy code (will fail but shows the endpoint works)
curl -X POST http://localhost:3000/exchange-token \
  -H "Content-Type: application/json" \
  -d '{"code": "test_code_12345"}' \
  -w "\nHTTP Status: %{http_code}\n" \
  2>/dev/null

echo ""
echo "Note: This will fail with GitHub error because we're using a test code."
echo "The important thing is that the endpoint responds (not 404)."

# Made with Bob
