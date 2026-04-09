# AI Assistant Guide

This guide contains operational knowledge and best practices for AI assistants working with this repository.

## Running the Local MCP Server for OAuth Elicitation Testing

When testing OAuth metadata discovery or elicitation flows, run the local MCP server with the following command:

```bash
./github-mcp-server http --scope-challenge --port 8082
```

### Command Breakdown

- `http`: Starts the server in HTTP mode (not stdio mode)
- `--scope-challenge`: Enables OAuth scope validation and proper WWW-Authenticate headers
- `--port 8082`: Specifies port 8082 (the default port for HTTP mode)

### Why These Flags Matter

1. **`--scope-challenge`**: This flag is crucial for OAuth testing because it:
   - Enables proper 401 Unauthorized responses with WWW-Authenticate headers
   - Includes the `resource_metadata` URL in the WWW-Authenticate header
   - Allows clients to discover the OAuth metadata endpoint dynamically

2. **`--port 8082`**: While this is the default, explicitly specifying it makes the command clearer and ensures consistency.

### Testing OAuth Metadata Discovery

Once the server is running, you can test OAuth metadata discovery:

```bash
# Direct access to OAuth metadata endpoint
curl http://localhost:8082/.well-known/oauth-protected-resource

# Or test the WWW-Authenticate challenge flow
curl -i http://localhost:8082/
```

The server will respond with a 401 and include a `WWW-Authenticate` header pointing to the metadata URL.

### Common Mistakes to Avoid

❌ **Don't run**: `./github-mcp-server http &` (missing --scope-challenge flag)
❌ **Don't run**: `./github-mcp-server http` (missing --scope-challenge for OAuth testing)
✅ **Do run**: `./github-mcp-server http --scope-challenge --port 8082`

## Building the Server

Before running the server, build it from source:

```bash
go build -o github-mcp-server ./cmd/github-mcp-server
```

## Environment Variables

For actual GitHub API operations (not just OAuth metadata), you'll need:

```bash
export GITHUB_PERSONAL_ACCESS_TOKEN=your_token_here
./github-mcp-server http --scope-challenge --port 8082
```

However, for OAuth metadata discovery testing, the token is not required as the metadata endpoint is publicly accessible.

## Related Documentation

- [Streamable HTTP Documentation](./streamable-http.md)
- [Host Integration Guide](./host-integration.md)
- [Testing Guide](./testing.md)