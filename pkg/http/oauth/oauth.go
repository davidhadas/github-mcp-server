// Package oauth provides OAuth 2.0 Protected Resource Metadata (RFC 9728) support
// for the GitHub MCP Server HTTP mode.
package oauth

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/github/github-mcp-server/pkg/http/headers"
	oauthpkg "github.com/github/github-mcp-server/pkg/oauth"
	"github.com/github/github-mcp-server/pkg/utils"
	"github.com/go-chi/chi/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const (
	// OAuthProtectedResourcePrefix is the well-known path prefix for OAuth protected resource metadata.
	OAuthProtectedResourcePrefix = "/.well-known/oauth-protected-resource"
)

// SupportedScopes lists all OAuth scopes that may be required by MCP tools.
var SupportedScopes = []string{
	"repo",
	"read:org",
	"read:user",
	"user:email",
	"read:packages",
	"write:packages",
	"read:project",
	"project",
	"gist",
	"notifications",
	"workflow",
	"codespace",
}

// Config holds the OAuth configuration for the MCP server.
type Config struct {
	// BaseURL is the publicly accessible URL where this server is hosted.
	// This is used to construct the OAuth resource URL.
	BaseURL string

	// AuthorizationServer is the OAuth authorization server URL.
	// Defaults to GitHub's OAuth server if not specified.
	AuthorizationServer string

	// ResourcePath is the externally visible base path for the MCP server (e.g., "/mcp").
	// This is used to restore the original path when a proxy strips a base path before forwarding.
	// If empty, requests are treated as already using the external path.
	ResourcePath string

	// ElicitationConfig holds the OAuth elicitation configuration.
	// When non-nil, the server will handle auth/url requests.
	ElicitationConfig *oauthpkg.ElicitationConfig

	// ClientSecret is the OAuth client secret for token exchange (optional, for demo/development).
	// When set along with ElicitationConfig, enables the /oauth/exchange-token endpoint.
	ClientSecret string
}

// AuthHandler handles OAuth-related HTTP endpoints.
type AuthHandler struct {
	cfg            *Config
	apiHost        utils.APIHostResolver
	tokenExchanger *TokenExchangeHandler
}

// NewAuthHandler creates a new OAuth auth handler.
func NewAuthHandler(cfg *Config, apiHost utils.APIHostResolver) (*AuthHandler, error) {
	if cfg == nil {
		cfg = &Config{}
	}

	if apiHost == nil {
		var err error
		apiHost, err = utils.NewAPIHost("https://api.github.com")
		if err != nil {
			return nil, fmt.Errorf("failed to create default API host: %w", err)
		}
	}

	// Create token exchanger if client secret is provided
	var tokenExchanger *TokenExchangeHandler
	if cfg.ElicitationConfig != nil && cfg.ClientSecret != "" {
		// Use GitHub's token endpoint
		tokenURL := "https://github.com/login/oauth/access_token"
		tokenExchanger = NewTokenExchangeHandler(
			cfg.ElicitationConfig.ClientID,
			cfg.ClientSecret,
			cfg.ElicitationConfig.RedirectURI,
			tokenURL,
		)
	}

	return &AuthHandler{
		cfg:            cfg,
		apiHost:        apiHost,
		tokenExchanger: tokenExchanger,
	}, nil
}

// routePatterns defines the route patterns for OAuth protected resource metadata.
var routePatterns = []string{
	"",          // Root: /.well-known/oauth-protected-resource
	"/readonly", // Read-only mode
	"/insiders", // Insiders mode
	"/x/{toolset}",
	"/x/{toolset}/readonly",
}

// RegisterRoutes registers the OAuth protected resource metadata routes.
func (h *AuthHandler) RegisterRoutes(r chi.Router) {
	for _, pattern := range routePatterns {
		for _, route := range h.routesForPattern(pattern) {
			path := OAuthProtectedResourcePrefix + route
			r.Handle(path, h.metadataHandler())
		}
	}
}

// RegisterElicitationRoutes registers the OAuth elicitation routes if configured.
func (h *AuthHandler) RegisterElicitationRoutes(r chi.Router) error {
	if h.cfg.ElicitationConfig == nil {
		return nil // Elicitation not configured
	}

	elicitationHandler, err := NewElicitationHandler(h.cfg.ElicitationConfig, h.apiHost)
	if err != nil {
		return fmt.Errorf("failed to create elicitation handler: %w", err)
	}

	// Register POST /auth/url endpoint
	r.Post("/auth/url", elicitationHandler.HandleAuthURL)

	return nil
}

// RegisterTokenExchangeRoute registers the token exchange endpoint if configured.
func (h *AuthHandler) RegisterTokenExchangeRoute(r chi.Router) {
	if h.tokenExchanger != nil {
		r.Post("/oauth/exchange-token", h.tokenExchanger.HandleTokenExchange)
	}
}

func (h *AuthHandler) metadataHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		resourcePath := resolveResourcePath(
			strings.TrimPrefix(r.URL.Path, OAuthProtectedResourcePrefix),
			h.cfg.ResourcePath,
		)
		resourceURL := h.buildResourceURL(r, resourcePath)

		var authorizationServerURL string
		if h.cfg.AuthorizationServer != "" {
			authorizationServerURL = h.cfg.AuthorizationServer
		} else {
			authURL, err := h.apiHost.AuthorizationServerURL(ctx)
			if err != nil {
				http.Error(w, fmt.Sprintf("failed to resolve authorization server URL: %v", err), http.StatusInternalServerError)
				return
			}
			authorizationServerURL = authURL.String()
		}

		metadata := &oauthex.ProtectedResourceMetadata{
			Resource:               resourceURL,
			AuthorizationServers:   []string{authorizationServerURL},
			ResourceName:           "GitHub MCP Server",
			ScopesSupported:        SupportedScopes,
			BearerMethodsSupported: []string{"header"},
		}

		auth.ProtectedResourceMetadataHandler(metadata).ServeHTTP(w, r)
	})
}

// routesForPattern generates route variants for a given pattern.
// GitHub strips the /mcp prefix before forwarding, so we register both variants:
// - With /mcp prefix: for direct access or when GitHub doesn't strip
// - Without /mcp prefix: for when GitHub has stripped the prefix
func (h *AuthHandler) routesForPattern(pattern string) []string {
	basePaths := []string{""}
	if basePath := normalizeBasePath(h.cfg.ResourcePath); basePath != "" {
		basePaths = append(basePaths, basePath)
	} else {
		basePaths = append(basePaths, "/mcp")
	}

	routes := make([]string, 0, len(basePaths)*2)
	for _, basePath := range basePaths {
		routes = append(routes, joinRoute(basePath, pattern))
		routes = append(routes, joinRoute(basePath, pattern)+"/")
	}

	return routes
}

// resolveResourcePath returns the externally visible resource path,
// restoring the configured base path when proxies strip it before forwarding.
func resolveResourcePath(path, basePath string) string {
	if path == "" {
		path = "/"
	}
	base := normalizeBasePath(basePath)
	if base == "" {
		return path
	}
	if path == "/" {
		return base
	}
	if path == base || strings.HasPrefix(path, base+"/") {
		return path
	}
	return base + path
}

// ResolveResourcePath returns the externally visible resource path for a request.
// Exported for use by middleware.
func ResolveResourcePath(r *http.Request, cfg *Config) string {
	basePath := ""
	if cfg != nil {
		basePath = cfg.ResourcePath
	}
	return resolveResourcePath(r.URL.Path, basePath)
}

// ResolveBaseResourcePath extracts the base resource path from a full request path.
// This is used for OAuth metadata URLs, which should point to the base resource path
// (e.g., "/mcp") rather than the full endpoint path (e.g., "/mcp/v1/messages").
// It returns one of the registered OAuth metadata route patterns.
func ResolveBaseResourcePath(r *http.Request, cfg *Config) string {
	fullPath := ResolveResourcePath(r, cfg)
	basePath := ""
	if cfg != nil {
		basePath = normalizeBasePath(cfg.ResourcePath)
	}

	// If no base path is configured, default to /mcp
	if basePath == "" {
		basePath = "/mcp"
	}

	// Check for /x/{toolset} pattern (e.g., /mcp/x/repos)
	if strings.HasPrefix(fullPath, basePath+"/x/") {
		// Extract up to the toolset name: /mcp/x/repos
		parts := strings.SplitN(strings.TrimPrefix(fullPath, basePath+"/x/"), "/", 2)
		if len(parts) > 0 && parts[0] != "" {
			return basePath + "/x/" + parts[0]
		}
	}

	// Check for /x/{toolset}/readonly pattern
	if strings.Contains(fullPath, "/x/") && strings.Contains(fullPath, "/readonly") {
		// Extract up to /readonly: /mcp/x/repos/readonly
		if idx := strings.Index(fullPath, "/readonly"); idx != -1 {
			pathUpToReadonly := fullPath[:idx+len("/readonly")]
			if strings.HasPrefix(pathUpToReadonly, basePath+"/x/") {
				return pathUpToReadonly
			}
		}
	}

	// Check other patterns in order of specificity (longer patterns first)
	specificPatterns := []string{"/readonly", "/insiders"}
	for _, pattern := range specificPatterns {
		expectedPath := basePath + pattern
		if fullPath == expectedPath || strings.HasPrefix(fullPath, expectedPath+"/") {
			return expectedPath
		}
	}

	// If path is exactly the base path or starts with it, return the base path
	if fullPath == basePath || strings.HasPrefix(fullPath, basePath+"/") {
		return basePath
	}

	// Default to the base path
	return basePath
}

// buildResourceURL constructs the full resource URL for OAuth metadata.
func (h *AuthHandler) buildResourceURL(r *http.Request, resourcePath string) string {
	host, scheme := GetEffectiveHostAndScheme(r, h.cfg)
	baseURL := fmt.Sprintf("%s://%s", scheme, host)
	if h.cfg.BaseURL != "" {
		baseURL = strings.TrimSuffix(h.cfg.BaseURL, "/")
	}
	if resourcePath == "" {
		resourcePath = "/"
	}
	if !strings.HasPrefix(resourcePath, "/") {
		resourcePath = "/" + resourcePath
	}
	return baseURL + resourcePath
}

// GetEffectiveHostAndScheme returns the effective host and scheme for a request.
func GetEffectiveHostAndScheme(r *http.Request, cfg *Config) (host, scheme string) { //nolint:revive
	if fh := r.Header.Get(headers.ForwardedHostHeader); fh != "" {
		host = fh
	} else {
		host = r.Host
	}
	if host == "" {
		host = "localhost"
	}
	if fp := r.Header.Get(headers.ForwardedProtoHeader); fp != "" {
		scheme = strings.ToLower(fp)
	} else {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return
}

// BuildResourceMetadataURL constructs the full URL to the OAuth protected resource metadata endpoint.
func BuildResourceMetadataURL(r *http.Request, cfg *Config, resourcePath string) string {
	host, scheme := GetEffectiveHostAndScheme(r, cfg)
	suffix := ""
	if resourcePath != "" && resourcePath != "/" {
		if !strings.HasPrefix(resourcePath, "/") {
			suffix = "/" + resourcePath
		} else {
			suffix = resourcePath
		}
	}
	if cfg != nil && cfg.BaseURL != "" {
		return strings.TrimSuffix(cfg.BaseURL, "/") + OAuthProtectedResourcePrefix + suffix
	}
	return fmt.Sprintf("%s://%s%s%s", scheme, host, OAuthProtectedResourcePrefix, suffix)
}

func normalizeBasePath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" || trimmed == "/" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	return strings.TrimSuffix(trimmed, "/")
}

func joinRoute(basePath, pattern string) string {
	if basePath == "" {
		return pattern
	}
	if pattern == "" {
		return basePath
	}
	if before, ok := strings.CutSuffix(basePath, "/"); ok {
		return before + pattern
	}
	return basePath + pattern
}
