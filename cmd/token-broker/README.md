# Token Broker Service

The Token Broker is a standalone service that manages OAuth sessions and token acquisition for the Kagenti MCP system. It implements the MCP Elicitation URL Mode protocol and provides a centralized token cache shared across multiple AI agents.

## Overview

The Token Broker:
- Manages OAuth sessions with timeout and lifecycle management
- Caches tokens per (user_id, mcp_server_url) with JWT expiry parsing
- Performs OAuth discovery against MCP servers
- Coordinates OAuth flows with Backend via long-polling events
- Provides Envoy ext_authz compatibility for future integration

## Architecture

```
┌─────────────┐         ┌──────────────┐         ┌─────────────┐
│   Backend   │◄────────┤ Token Broker ├────────►│ MCP Server  │
│  (Future)   │  Events │              │ Discovery│             │
└─────────────┘         └──────┬───────┘         └─────────────┘
                               │
                               │ Token Cache
                               │ Session Store
                               ▼
                        ┌──────────────┐
                        │   Storage    │
                        │  (In-Memory) │
                        └──────────────┘
```

## Building

```bash
go build -o token-broker ./cmd/token-broker/
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `TOKEN_BROKER_PORT` | `8190` | HTTP server port |
| `TOKEN_BROKER_CALLBACK_URL` | `http://backend-service:8187/callback` | OAuth callback URL |
| `TOKEN_BROKER_SESSION_TIMEOUT` | `60s` | Idle session timeout |
| `TOKEN_BROKER_MAX_SESSIONS_PER_USER` | `5` | Max concurrent sessions per user |
| `TOKEN_BROKER_TOKEN_WAIT_TIMEOUT` | `300s` | Max time to wait for OAuth completion |

### Example Configuration

```bash
export TOKEN_BROKER_PORT=8190
export TOKEN_BROKER_CALLBACK_URL=http://localhost:8187/callback
export TOKEN_BROKER_SESSION_TIMEOUT=60s
export TOKEN_BROKER_MAX_SESSIONS_PER_USER=5
export TOKEN_BROKER_TOKEN_WAIT_TIMEOUT=300s
```

## Running

### Local Development

```bash
# Set required configuration
export TOKEN_BROKER_CALLBACK_URL=http://localhost:8187/callback

# Run the service
./token-broker
```

The service will start on port 8190 by default and log to stdout in JSON format.

### Docker

```bash
docker build -t token-broker -f Dockerfile.token-broker .
docker run -p 8190:8190 \
  -e TOKEN_BROKER_CALLBACK_URL=http://backend:8187/callback \
  token-broker
```

## API Endpoints

### POST /sessions

Create a new OAuth session.

**Headers:**
- `X-User-ID`: User identifier (required)

**Response:** `201 Created`
```json
{
  "oauth_session_key": "uuid-v4"
}
```

**Errors:**
- `400` - Missing user ID
- `429` - Max sessions per user exceeded

---

### POST /sessions/{sessionKey}/token

Request a token for an MCP server. Blocks until token is available or timeout.

**Headers:**
- `X-User-ID`: User identifier (required)
- `X-Mcp-Server-Url`: MCP server base URL (required)

**Response:** `200 OK`
```json
{
  "token": "access-token"
}
```

**Errors:**
- `401` - Invalid session or user mismatch
- `408` - Timeout waiting for OAuth completion
- `503` - OAuth flow failed

---

### POST /sessions/{sessionKey}/events

Long-poll for events OR complete OAuth flow.

**Headers:**
- `X-User-ID`: User identifier (required)

**Query Parameters (for OAuth completion):**
- `code`: Authorization code
- `state`: OAuth state parameter

**Long-poll Response:** `200 OK`
```json
{
  "type": "oauth_required",
  "mcp_server_url": "http://mcp-server:8184",
  "auth_url": "https://github.com/login/oauth/authorize?..."
}
```

Or error event:
```json
{
  "type": "error",
  "message": "Session expired",
  "code": "session_expired"
}
```

**OAuth Completion Response:** `200 OK` (empty body)

---

### POST /sessions/{sessionKey}/end

Terminate a session and release all resources.

**Headers:**
- `X-User-ID`: User identifier (required)

**Response:** `200 OK`

---

### POST /ext_authz

Envoy ext_authz compatibility endpoint. Same behavior as `/sessions/{key}/token` but returns Authorization header.

**Headers:**
- `X-OAuth-Session-Key`: Session key (required)
- `X-User-ID`: User identifier (required)
- `X-Mcp-Server-Url`: MCP server base URL (required)

**Success Response:** `200 OK`
- Header: `Authorization: Bearer <token>`

**Failure Response:** `403 Forbidden`
```json
{
  "jsonrpc": "2.0",
  "id": null,
  "error": {
    "code": -32001,
    "message": "Failed to obtain user authorization for this MCP server."
  }
}
```

---

### GET /health

Health check endpoint.

**Response:** `200 OK`
```
OK
```

## Testing

### Manual Testing with curl

1. **Create a session:**
```bash
curl -X POST http://localhost:8190/sessions \
  -H "X-User-ID: test-user" \
  -v
```

Response:
```json
{"oauth_session_key":"550e8400-e29b-41d4-a716-446655440000"}
```

2. **Start event long-poll (in another terminal):**
```bash
SESSION_KEY="550e8400-e29b-41d4-a716-446655440000"
curl -X POST "http://localhost:8190/sessions/$SESSION_KEY/events" \
  -H "X-User-ID: test-user" \
  -v
```

This will block until an event is available.

3. **Request a token (in another terminal):**
```bash
SESSION_KEY="550e8400-e29b-41d4-a716-446655440000"
curl -X POST "http://localhost:8190/sessions/$SESSION_KEY/token" \
  -H "X-User-ID: test-user" \
  -H "X-Mcp-Server-Url: http://localhost:8082" \
  -v
```

This will trigger OAuth discovery and send an `oauth_required` event to the long-poll.

4. **Complete OAuth (after user authorizes):**
```bash
SESSION_KEY="550e8400-e29b-41d4-a716-446655440000"
curl -X POST "http://localhost:8190/sessions/$SESSION_KEY/events?code=AUTH_CODE&state=STATE" \
  -H "X-User-ID: test-user" \
  -v
```

5. **End session:**
```bash
SESSION_KEY="550e8400-e29b-41d4-a716-446655440000"
curl -X POST "http://localhost:8190/sessions/$SESSION_KEY/end" \
  -H "X-User-ID: test-user" \
  -v
```

## Logging

The Token Broker logs to stdout in JSON format for Kubernetes compatibility.

**Log Levels:**
- `INFO` - Normal operations (session creation, token acquisition, OAuth flows)
- `DEBUG` - Detailed flow information (cache hits, semaphore acquisition)
- `WARN` - Recoverable issues (session validation failures, max sessions)
- `ERROR` - Errors requiring attention (OAuth failures, internal errors)

**Example Log Entry:**
```json
{
  "time": "2024-01-15T10:30:00Z",
  "level": "INFO",
  "msg": "Token acquired",
  "session_key": "550e8400-e29b-41d4-a716-446655440000",
  "user_id": "test-user",
  "mcp_server": "http://localhost:8082"
}
```

## Session Lifecycle

```
1. Backend creates session → Session created (active)
2. Backend polls /events → Timer reset
3. Backend disconnects → Timer starts (60s)
4. Backend reconnects → Timer reset
5. Timer expires OR Backend calls /end → Session terminated
```

## Token Caching

Tokens are cached per `(user_id, mcp_server_url)`:
- JWT tokens: Expiry parsed from `exp` claim
- Non-JWT tokens: Treated as long-lived (1 year)
- Near-expiry tokens (< 5 min): Treated as expired
- Cache is shared across all sessions

## Concurrency Model

- **Per-session semaphore**: Only one OAuth flow per session at a time
- **Double-checked locking**: Check cache before and after semaphore acquisition
- **Shared token cache**: Multiple sessions can share cached tokens
- **Session isolation**: Different sessions run OAuth flows independently

## Error Handling

All errors use standardized JSON format:
```json
{
  "error": {
    "code": "error_code",
    "message": "Human-readable message"
  }
}
```

**Error Codes:**
- `invalid_request` - Missing or invalid parameters
- `unauthorized` - Invalid session or user mismatch
- `session_not_found` - Session does not exist
- `session_expired` - Session timed out
- `too_many_sessions` - User exceeded max sessions
- `timeout` - OAuth flow did not complete in time
- `oauth_failed` - OAuth discovery or exchange failed
- `internal_error` - Internal server error

## Monitoring

Key metrics to monitor:
- Active session count
- Token cache size
- Token cache hit rate
- OAuth flow success rate
- Session timeout rate
- Average OAuth completion time

## Security Considerations

- Session keys are UUID v4 (cryptographically random)
- User ID validation on every request
- Session ownership strictly enforced
- `code_verifier` never exposed to Backend
- `client_secret` held only by MCP server
- Tokens stored in memory only (no persistence)

## Future Enhancements

Phase 1 (current):
- ✅ Standalone service with in-memory storage
- ✅ Session management and timeout
- ✅ Token caching with JWT expiry
- ✅ OAuth discovery and exchange
- ✅ ext_authz compatibility

Future phases:
- Phase 2: Envoy sidecar integration
- Phase 3: Backend implementation
- Phase 4: End-to-end testing
- Phase 5: Production hardening (Redis cache, metrics, etc.)

## Troubleshooting

### Session expires too quickly
Increase `TOKEN_BROKER_SESSION_TIMEOUT`:
```bash
export TOKEN_BROKER_SESSION_TIMEOUT=120s
```

### OAuth flow times out
Increase `TOKEN_BROKER_TOKEN_WAIT_TIMEOUT`:
```bash
export TOKEN_BROKER_TOKEN_WAIT_TIMEOUT=600s
```

### Too many sessions error
Increase `TOKEN_BROKER_MAX_SESSIONS_PER_USER`:
```bash
export TOKEN_BROKER_MAX_SESSIONS_PER_USER=10
```

### Token not cached
Check logs for JWT parsing errors. Non-JWT tokens are treated as long-lived.

## Development

### Project Structure
```
cmd/token-broker/
  main.go              # Service bootstrap
  README.md            # This file

internal/tokenbroker/
  api/                 # HTTP handlers
  cache/               # Token cache with JWT parsing
  core/                # Token acquisition orchestration
  oauthflow/           # OAuth discovery and exchange
  session/             # Session management
```

### Adding New Features

1. Update interfaces in `internal/tokenbroker/core/interfaces.go`
2. Implement in appropriate package
3. Add unit tests
4. Update integration tests
5. Update this README

## License

See LICENSE file in repository root.