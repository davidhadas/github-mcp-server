# Backend Service (Envoy Architecture)

This is the Envoy-based backend implementation for the Kagenti MCP OAuth system. It replaces the old backend that communicated with the authbridge-sidecar.

## Overview

The backend serves as the user-facing application that:
1. Creates sessions with the Token Broker
2. Listens for OAuth events via Server-Sent Events (SSE)
3. Handles OAuth callbacks from the OAuth provider
4. Forwards requests to the AI Agent with session keys
5. Manages session lifecycle

## Architecture

```
Browser → Backend → Token Broker (session management)
                 ↓
                 AI Agent (with X-OAuth-Session-Key header)
```

## Key Differences from Old Backend

| Feature | Old Backend | New Backend (Envoy) |
|---------|-------------|---------------------|
| Session Management | Embedded in sidecar | Centralized Token Broker |
| OAuth Events | Synchronous (blocking) | Asynchronous (SSE) |
| Token Storage | Per-sidecar | Shared cache |
| Architecture | Tightly coupled | Loosely coupled |

## API Endpoints

### POST /task
Submit a task to the AI Agent.

**Request:**
```json
{
  "user_id": "test-user",
  "task": "List my GitHub repositories",
  "agent_url": "http://aiagent-service:8186/task"
}
```

**Response:**
```json
{
  "status": "success",
  "result": "..."
}
```

### GET /events?user_id={userID}
Server-Sent Events stream for OAuth events.

**Events:**
```json
{
  "type": "oauth_required",
  "mcp_server_url": "http://mcp-server:8184",
  "auth_url": "https://github.com/login/oauth/authorize?..."
}
```

### GET /oauth/callback?code={code}&state={state}&user_id={userID}
OAuth callback endpoint.

Redirects to `/demo?oauth_success=true&user_id={userID}` on success.

### POST /session/end?user_id={userID}
End a user session.

### GET /health
Health check endpoint.

### GET /demo
Demo page (if configured).

## Configuration

Environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `BACKEND_PORT` | `8187` | HTTP server port |
| `TOKEN_BROKER_URL` | `http://token-broker-service:8190` | Token Broker URL |
| `DEMO_PAGE_PATH` | `""` | Path to demo HTML file |

## Building

```bash
go build -o backend-envoy ./cmd/backend-envoy/
```

## Running

```bash
export TOKEN_BROKER_URL=http://localhost:8190
export DEMO_PAGE_PATH=./ui/src/apps/envoy-demo/index.html
./backend-envoy
```

## Session Flow

1. **User submits task** → Backend creates session with Token Broker
2. **Backend starts SSE connection** → Listens for OAuth events
3. **Backend forwards request to Agent** → Includes `X-OAuth-Session-Key` header
4. **Agent calls MCP server** → Envoy intercepts, calls Token Broker via ext_authz
5. **Token Broker needs OAuth** → Sends `oauth_required` event to Backend
6. **Backend sends event to browser** → Browser displays OAuth popup
7. **User authorizes** → OAuth provider redirects to Backend callback
8. **Backend completes OAuth** → Sends code to Token Broker
9. **Token Broker exchanges code** → Caches token, unblocks Agent request
10. **Agent receives MCP response** → Returns to Backend → Returns to browser

## Implementation Details

### Session Manager (`internal/backend/envoy/session.go`)
- Manages user sessions
- Creates sessions with Token Broker
- Polls for OAuth events in background goroutines
- Distributes events to SSE connections

### Token Broker Client (`internal/backend/envoy/client.go`)
- HTTP client for Token Broker API
- Handles session creation, event polling, OAuth completion
- Forwards requests to AI Agent with session keys

### HTTP Handlers (`internal/backend/envoy/handlers.go`)
- Task submission
- SSE event streaming
- OAuth callback handling
- Session termination

## Demo Page

The demo page (`ui/src/apps/envoy-demo/index.html`) provides a simple UI for:
- Submitting tasks
- Receiving OAuth events via SSE
- Handling OAuth redirects
- Displaying results

## Testing

### Manual Testing

1. Start Token Broker:
```bash
cd cmd/token-broker
go run . &
```

2. Start Backend:
```bash
cd cmd/backend-envoy
export TOKEN_BROKER_URL=http://localhost:8190
export DEMO_PAGE_PATH=../../ui/src/apps/envoy-demo/index.html
go run .
```

3. Open browser:
```
http://localhost:8187/demo
```

4. Submit a task and observe OAuth flow.

### Integration Testing

See `k8s/envoy-demo/README.md` for full deployment testing.

## Troubleshooting

### Session not created
- Check Token Broker is running and accessible
- Verify `TOKEN_BROKER_URL` is correct
- Check Token Broker logs for errors

### OAuth events not received
- Verify SSE connection is established (check browser console)
- Check Token Broker event polling is working
- Verify session exists in Token Broker

### OAuth callback fails
- Verify OAuth provider redirect URI matches Backend URL
- Check `user_id` is included in callback URL
- Verify session still exists (not expired)

## Production Considerations

- Add authentication/authorization for Backend endpoints
- Implement session cleanup on user disconnect
- Add metrics and monitoring
- Use HTTPS for OAuth callbacks
- Implement rate limiting
- Add request validation and sanitization

## Future Enhancements

- WebSocket support for bidirectional communication
- Multiple concurrent tasks per user
- Task cancellation
- Progress updates during long-running tasks
- Session persistence across Backend restarts