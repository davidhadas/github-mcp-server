# MCP OAuth Discovery Demo (Go)

This is a Go implementation of the MCP OAuth Discovery demo that demonstrates how to connect to a GitHub MCP server and retrieve the OAuth authorization server URL (elicitation URL) from the server's OAuth protected resource metadata.

## Building

```bash
cd cmd/mcp-oauth-demo
go build -o mcp-oauth-demo main.go
```

## Usage

```bash
# Remote GitHub MCP Server
./mcp-oauth-demo -url https://api.githubcopilot.com/mcp/

# Local MCP Server
./mcp-oauth-demo -url http://localhost:8082

# With debug logging
./mcp-oauth-demo -url https://api.githubcopilot.com/mcp/ -debug

# JSON output format
./mcp-oauth-demo -url https://api.githubcopilot.com/mcp/ -format json

# Specific toolset
./mcp-oauth-demo -url https://api.githubcopilot.com/mcp/x/repos

# Disable SSL verification (not recommended)
./mcp-oauth-demo -url https://localhost:8082 -no-verify-ssl
```

## Options

- `-url string`: Base URL of the MCP server (required)
- `-timeout int`: Request timeout in seconds (default: 10)
- `-no-verify-ssl`: Disable SSL certificate verification (not recommended)
- `-debug`: Enable debug logging
- `-format string`: Output format: text or json (default: "text")

## How It Works

The tool performs OAuth discovery by:

1. **Direct Access**: First attempts to fetch metadata from `/.well-known/oauth-protected-resource`
2. **WWW-Authenticate Header**: If a 401 response is received, it extracts the `resource_metadata` URL from the `WWW-Authenticate` header and follows it
3. **Metadata Parsing**: Parses the OAuth protected resource metadata JSON
4. **Authorization Server Extraction**: Extracts and displays the authorization server URL

## Output

### Text Format (Default)

```
Connecting to MCP Server: https://api.githubcopilot.com/mcp/
Fetching OAuth metadata...
✓ Successfully retrieved OAuth metadata

Authorization Server URL: https://github.com/login/oauth/authorize

Additional Information:
- Resource Name: GitHub MCP Server
- Resource URL: https://api.githubcopilot.com/mcp/
- Supported Scopes: repo, user, gist, ...
- Bearer Methods: header, query
```

### JSON Format

```json
{
  "success": true,
  "authorization_server_url": "https://github.com/login/oauth/authorize",
  "metadata": {
    "authorization_servers": [
      "https://github.com/login/oauth/authorize"
    ],
    "resource_name": "GitHub MCP Server",
    "resource": "https://api.githubcopilot.com/mcp/",
    "scopes_supported": ["repo", "user", "gist"],
    "bearer_methods_supported": ["header", "query"]
  }
}
```

## Comparison with Python Version

This Go implementation provides the same functionality as the Python version (`python-mcp-demo/mcp_oauth_demo.py`) with the following advantages:

- **No Dependencies**: Uses only Go standard library (no external packages required)
- **Single Binary**: Compiles to a single executable with no runtime dependencies
- **Better Performance**: Native compiled code with lower memory footprint
- **Type Safety**: Compile-time type checking
- **Concurrent**: Built on Go's excellent concurrency primitives (though not used in this simple demo)

## Related

- Python version: `../../python-mcp-demo/mcp_oauth_demo.py`
- Main MCP server: `../../cmd/github-mcp-server/`