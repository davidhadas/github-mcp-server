# Token Broker Integration Test Summary

Comparison of `integration_test.go` against the design in `docs/envoy_sidecar.md`.

## Covered

| # | Design Requirement | Test |
|---|---|---|
| 1 | Full OAuth flow: synthetic 401 → discovery → auth URL + PKCE → event → user auth → code exchange → token returned | `TestIntegration_FullOAuthFlow` |
| 2 | Token caching per (user_id, mcp_server), not tied to session | `TestIntegration_CachedTokenSkipsOAuth` (same session) |
| 3 | Session user_id binding — mismatched user gets 401 | `TestIntegration_SessionValidation` |
| 4 | Session termination via `POST /sessions/{key}/end` — subsequent requests fail | `TestIntegration_EndSession` |
| 5 | PKCE: `code_verifier` from `/auth/url` passed correctly to `/oauth/exchange-token` | `TestIntegration_FullOAuthFlow` (asserts exchange params) |
| 6 | OAuth timeout when user never completes authorization | `TestIntegration_OAuthTimeout` |
| 7 | All 4 MCP server endpoints called in correct order | `TestIntegration_FullOAuthFlow` (recorder assertion) |

## Gaps

| # | Design Requirement (from `envoy_sidecar.md`) | What to Test |
|---|---|---|
| G1 | Session semaphore — only one OAuth flow per session at a time; second token request for a different MCP server waits on semaphore, re-checks cache after acquiring it | Two goroutines request tokens for different MCP servers on the same session concurrently. Verify only one OAuth flow runs; the second blocks until the first completes. |
| G2 | Cached token spans sessions — tokens are per (user_id, mcp_server), not per session | Complete OAuth in session A, create session B for the same user, request token for same MCP server. Should return cached token with no OAuth flow. |
| G3 | Session idle timeout — timer starts when Backend stops polling events; session expires when timer fires | Create session, don't poll events, wait for timeout, verify session is expired and token requests fail. Requires short timeout in test config. |
| G4 | Session limit per user — Token Broker refuses session creation beyond the per-user limit | Create sessions up to the limit (configured as 5 in `setupTokenBrokerServer`), attempt one more, verify it's refused. |
| G5 | In-flight requests fail on session end — pending blocking token request is interrupted when session is ended concurrently | Start a blocking token request (no OAuth completion), end the session from another goroutine, verify the token request fails with an error. |
| G6 | JSON error format — all errors are returned as JSON | Validate that error responses (401, 503, timeout) are valid JSON with structured fields, not plain text. |
| G7 | Token near-expiry eviction — tokens within 5 min of expiry are evicted from cache and re-acquired | Requires a fake token with a near-future expiry. After caching, next request should trigger a new OAuth flow instead of returning the stale token. |
| G8 | Agent never sees 401 or auth URLs — AuthBridge caller gets either a token or a broker error, never raw MCP server responses | Assert that token request responses never contain 401 status or auth URL content from the MCP server. |

## Notes

- `X-User-ID` is currently a plain header. The design calls for a signed Kagenti user access token; tests will need updating when that's implemented.
- `setupTokenBrokerServer` uses `sessionTimeout: 60s` and `maxSessionsPerUser: 5` — these are the levers for G3 and G4.
- For G7, the fake MCP server's `/oauth/exchange-token` would need to return a JWT or token with a short expiry claim instead of the current static `gho_test_token_abc`.
