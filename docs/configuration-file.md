# Configuration File Support

The GitHub MCP Server supports configuration via YAML, JSON, or TOML files, in addition to CLI flags and environment variables.

## Configuration Priority

Settings are loaded in the following order (later sources override earlier ones):

1. **Default values**
2. **Configuration file** (if found)
3. **Environment variables** (prefixed with `GITHUB_`)
4. **CLI flags**

## Configuration File Locations

The server looks for a configuration file named `config.yaml`, `config.json`, or `config.toml` in these locations (in order):

1. Current working directory (`.`)
2. Home directory (`~/.github-mcp-server/`)
3. System directory (`/etc/github-mcp-server/`)

## Example Configuration Files

### YAML Format (`config.yaml`)

```yaml
# Server Configuration
port: 8084
host: github.com
base-url: https://your-server.com
base-path: /mcp

# OAuth Elicitation Configuration
oauth-client-id: your-github-app-client-id
oauth-redirect-uri: http://localhost:3000/callback
oauth-scopes:
  - repo
  - user
  - read:org

# Security Settings
scope-challenge: true
lockdown-mode: false

# Performance Settings
content-window-size: 5000
repo-access-cache-ttl: 5m

# Feature Flags
insiders: false

# Logging
log-level: info
enable-command-logging: false
```

### JSON Format (`config.json`)

```json
{
  "port": 8084,
  "host": "github.com",
  "base-url": "https://your-server.com",
  "base-path": "/mcp",
  "oauth-client-id": "your-github-app-client-id",
  "oauth-redirect-uri": "http://localhost:3000/callback",
  "oauth-scopes": ["repo", "user", "read:org"],
  "scope-challenge": true,
  "lockdown-mode": false,
  "content-window-size": 5000,
  "repo-access-cache-ttl": "5m",
  "insiders": false,
  "log-level": "info",
  "enable-command-logging": false
}
```

### TOML Format (`config.toml`)

```toml
# Server Configuration
port = 8084
host = "github.com"
base-url = "https://your-server.com"
base-path = "/mcp"

# OAuth Elicitation Configuration
oauth-client-id = "your-github-app-client-id"
oauth-redirect-uri = "http://localhost:3000/callback"
oauth-scopes = ["repo", "user", "read:org"]

# Security Settings
scope-challenge = true
lockdown-mode = false

# Performance Settings
content-window-size = 5000
repo-access-cache-ttl = "5m"

# Feature Flags
insiders = false

# Logging
log-level = "info"
enable-command-logging = false
```

## Configuration Options

### Server Options

- `port` - HTTP server port (default: 8080)
- `host` - GitHub host (default: github.com)
- `base-url` - Base URL for the server
- `base-path` - Base path for API endpoints

### OAuth Elicitation Options

- `oauth-client-id` - GitHub OAuth App client ID (enables OAuth elicitation endpoint)
- `oauth-redirect-uri` - OAuth redirect URI for your application
- `oauth-scopes` - Default OAuth scopes (array of strings)

### Security Options

- `scope-challenge` - Enable scope challenge mode (default: false)
- `lockdown-mode` - Enable lockdown mode (default: false)

### Performance Options

- `content-window-size` - Content window size (default: 5000)
- `repo-access-cache-ttl` - Repository access cache TTL (default: 5m)

### Feature Flags

- `insiders` - Enable insider features (default: false)

### Logging Options

- `log-level` - Log level (debug, info, warn, error)
- `log-file` - Log file path
- `enable-command-logging` - Enable command logging (default: false)

### Toolsets

- `toolsets` - Array of enabled toolsets (empty = all enabled)

## Usage Examples

### Using a Configuration File

1. Create a `config.yaml` file in your working directory:

```yaml
port: 8084
oauth-client-id: ghp_your_client_id
oauth-redirect-uri: http://localhost:3000/callback
oauth-scopes:
  - repo
  - user
```

2. Run the server without flags:

```bash
./github-mcp-server http
```

### Overriding Configuration with Flags

Configuration file settings can be overridden with CLI flags:

```bash
./github-mcp-server http --port 9000 --oauth-client-id "different-client-id"
```

### Using Environment Variables

Environment variables (prefixed with `GITHUB_`) override config file settings:

```bash
export GITHUB_PORT=9000
export GITHUB_OAUTH_CLIENT_ID="env-client-id"
./github-mcp-server http
```

### Combining All Methods

You can use all three methods together. Priority order:

1. Config file: `port: 8084`
2. Environment: `GITHUB_PORT=9000` (overrides config file)
3. CLI flag: `--port 10000` (overrides environment)

Result: Server runs on port 10000

## OAuth Elicitation Setup

To enable the OAuth authorization URL endpoint (`POST /auth/url`):

1. Create a GitHub OAuth App at https://github.com/settings/developers
2. Note your Client ID and set a redirect URI
3. Configure the server:

```yaml
oauth-client-id: your_github_oauth_app_client_id
oauth-redirect-uri: http://localhost:3000/callback
oauth-scopes:
  - repo
  - user
  - read:org
```

4. Start the server:

```bash
./github-mcp-server http
```

5. The OAuth elicitation endpoint will be available at `POST /auth/url`

## Troubleshooting

### Config File Not Found

If you see no error message, the config file was not found. This is normal - the server will use defaults, environment variables, and CLI flags.

### Config File Parse Error

If you see an error like "Error reading config file", check:

1. YAML/JSON/TOML syntax is correct
2. File permissions allow reading
3. File encoding is UTF-8

### Values Not Being Applied

Check the priority order:

1. Verify the config file is in a searched location
2. Check if environment variables are overriding
3. Check if CLI flags are overriding
4. Use `--help` to see available options