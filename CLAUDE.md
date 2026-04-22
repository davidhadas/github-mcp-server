# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

GitHub MCP Server is a Go-based [Model Context Protocol (MCP)](https://modelcontextprotocol.io) server that exposes GitHub's platform to AI agents via tools. It supports GitHub.com, GitHub Enterprise Server, and GitHub Enterprise Cloud, and can run as a stdio server (for local MCP clients) or an HTTP server (for remote access with OAuth).

## Commands

```bash
# Build
go build -v ./cmd/github-mcp-server

# Test (with race detection)
script/test          # go test -race ./...

# Lint + format
script/lint          # gofmt -s -w . && golangci-lint run

# Update tool schema snapshots (after modifying tool definitions)
UPDATE_TOOLSNAPS=true go test ./...

# Regenerate README tool documentation (after modifying tools)
script/generate-docs

# Run a single test
go test -run TestFunctionName ./pkg/github/...
```

**Before committing:** always run `script/lint`, `script/test`, and `script/generate-docs` (if tools changed).

## Architecture

### Entry Points

- `cmd/github-mcp-server/` — main MCP server (stdio or HTTP subcommands)
- `cmd/aiagent/` — standalone AI agent that uses the MCP server
- `cmd/token-broker/` — centralized OAuth token management service
- `cmd/mcpcurl/` — CLI for testing MCP calls manually

### Request Flow

```
CLI (cobra/viper config)
  → stdio or HTTP transport
    → internal/ghmcp/server.go  (MCPServer construction, client injection)
      → pkg/github/server.go    (tool registration via inventory)
        → pkg/github/*.go       (tool handlers: issues, pull_requests, actions, …)
          → go-github REST / shurcooL/githubv4 GraphQL clients
```

### Dependency Injection

Tools receive dependencies via context, not direct injection:

- `ContextWithDeps(ctx, deps)` — injects a `ToolDependencies` into context (done per-request in HTTP mode so tokens can vary per request)
- `MustDepsFromContext(ctx)` — retrieved inside tool handlers
- `ToolDependencies` interface (`pkg/github/dependencies.go`) — provides REST client, GraphQL client, raw client, translations, feature flags, repo access cache

### Tool Registration

`pkg/inventory/` manages toolsets. Tools are grouped into named toolsets (e.g. `issues`, `pull_requests`, `actions`). Enabled toolsets are resolved at startup from config/env. The default set is: `context, repos, issues, pull_requests, users`.

Each toolset file (`pkg/github/issues.go`, `pkg/github/actions.go`, etc.) registers its tools against the MCP server. Tool schemas are snapshot-tested in `pkg/github/__toolsnaps__/*.snap`.

### Key Config Fields (MCPServerConfig)

Defined in `pkg/github/server.go`:
- `EnabledToolsets`, `EnabledTools`, `ExcludeTools` — what tools to expose
- `ReadOnly` — disables all write operations
- `LockdownMode` — restricts access to specific repos (via cache in `pkg/lockdown/`)
- `DynamicToolsets` — allows runtime toolset toggling
- `TokenScopes` — filters tools by available OAuth scopes

### HTTP Server

`pkg/http/` handles the HTTP transport. Per-request dependencies are injected by middleware using the bearer token from the `Authorization` header, enabling multi-tenant scenarios where each caller uses their own GitHub token.

## Testing Patterns

- **Unit tests**: table-driven, with mocked GitHub API responses using `githubv4mock` for GraphQL and `go-github` mock transport for REST
- **Toolsnaps**: every tool's JSON schema is stored in `pkg/github/__toolsnaps__/`. Tests fail if the schema changes without updating snaps. Update with `UPDATE_TOOLSNAPS=true go test ./...`
- **E2E tests** (`e2e/`): require a real `GITHUB_PERSONAL_ACCESS_TOKEN`; skipped in normal CI

## Important Conventions

- **Export functions** that are called by the remote server wrapper — preserve existing export patterns
- **Toolsnap files** (`*.snap`) must be committed alongside any tool definition changes
- **README.md tool documentation** is generated; do not edit the tool tables manually
- The `pkg/github/` package is the core — it contains ~40K LoC across 74 files; changes here have broad impact
- Feature flags live in `MCPServerConfig.EnabledFeatures` and are checked via `deps.IsFeatureEnabled()`
