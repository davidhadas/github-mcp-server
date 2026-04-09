package http

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"time"

	ghcontext "github.com/github/github-mcp-server/pkg/context"
	"github.com/github/github-mcp-server/pkg/github"
	"github.com/github/github-mcp-server/pkg/http/oauth"
	"github.com/github/github-mcp-server/pkg/inventory"
	"github.com/github/github-mcp-server/pkg/lockdown"
	oauthpkg "github.com/github/github-mcp-server/pkg/oauth"
	"github.com/github/github-mcp-server/pkg/observability"
	"github.com/github/github-mcp-server/pkg/observability/metrics"
	"github.com/github/github-mcp-server/pkg/scopes"
	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/github/github-mcp-server/pkg/utils"
	"github.com/go-chi/chi/v5"
)

// knownFeatureFlags are the feature flags that can be enabled via X-MCP-Features header.
// Only these flags are accepted from headers.
var knownFeatureFlags = []string{}

type ServerConfig struct {
	// Version of the server
	Version string

	// GitHub Host to target for API requests (e.g. github.com or github.enterprise.com)
	Host string

	// Port to listen on (default: 8082)
	Port int

	// BaseURL is the publicly accessible URL of this server for OAuth resource metadata.
	// If not set, the server will derive the URL from incoming request headers.
	BaseURL string

	// ResourcePath is the externally visible base path for this server (e.g., "/mcp").
	// This is used to restore the original path when a proxy strips a base path before forwarding.
	ResourcePath string

	// ExportTranslations indicates if we should export translations
	// See: https://github.com/github/github-mcp-server?tab=readme-ov-file#i18n--overriding-descriptions
	ExportTranslations bool

	// EnableCommandLogging indicates if we should log commands
	EnableCommandLogging bool

	// Path to the log file if not stderr
	LogFilePath string

	// Content window size
	ContentWindowSize int

	// LockdownMode indicates if we should enable lockdown mode
	LockdownMode bool

	// RepoAccessCacheTTL overrides the default TTL for repository access cache entries.
	RepoAccessCacheTTL *time.Duration

	// ScopeChallenge indicates if we should return OAuth scope challenges, and if we should perform
	// tool filtering based on token scopes.
	ScopeChallenge bool

	// OAuth Elicitation Configuration
	// OAuthClientID is the OAuth client ID for elicitation (enables auth/url endpoint when set)
	OAuthClientID string

	// OAuthRedirectURI is the OAuth redirect URI for elicitation
	OAuthRedirectURI string

	// OAuthScopes are the default OAuth scopes for elicitation
	OAuthScopes []string

	// OAuthClientSecret is the OAuth client secret for token exchange (optional, for demo/development)
	OAuthClientSecret string

	// DemoPagePath is the path to the demo HTML file to serve (optional)
	DemoPagePath string
}

func RunHTTPServer(cfg ServerConfig) error {
	// Create app context
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	t, dumpTranslations := translations.TranslationHelper()

	var slogHandler slog.Handler
	var logOutput io.Writer
	if cfg.LogFilePath != "" {
		file, err := os.OpenFile(cfg.LogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		logOutput = file
		slogHandler = slog.NewTextHandler(logOutput, &slog.HandlerOptions{Level: slog.LevelDebug})
	} else {
		logOutput = os.Stderr
		slogHandler = slog.NewTextHandler(logOutput, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	logger := slog.New(slogHandler)
	logger.Info("starting server", "version", cfg.Version, "host", cfg.Host, "lockdownEnabled", cfg.LockdownMode)

	apiHost, err := utils.NewAPIHost(cfg.Host)
	if err != nil {
		return fmt.Errorf("failed to parse API host: %w", err)
	}

	repoAccessOpts := []lockdown.RepoAccessOption{
		lockdown.WithLogger(logger.With("component", "lockdown")),
	}
	if cfg.RepoAccessCacheTTL != nil {
		repoAccessOpts = append(repoAccessOpts, lockdown.WithTTL(*cfg.RepoAccessCacheTTL))
	}

	featureChecker := createHTTPFeatureChecker()

	obs, err := observability.NewExporters(logger, metrics.NewNoopMetrics())
	if err != nil {
		return fmt.Errorf("failed to create observability exporters: %w", err)
	}

	deps := github.NewRequestDeps(
		apiHost,
		cfg.Version,
		cfg.LockdownMode,
		repoAccessOpts,
		t,
		cfg.ContentWindowSize,
		featureChecker,
		obs,
	)

	// Initialize the global tool scope map
	err = initGlobalToolScopeMap(t)
	if err != nil {
		return fmt.Errorf("failed to initialize tool scope map: %w", err)
	}

	// Create OAuth elicitation config if client ID is provided
	var elicitationCfg *oauthpkg.ElicitationConfig
	if cfg.OAuthClientID != "" {
		elicitationCfg = &oauthpkg.ElicitationConfig{
			ClientID:    cfg.OAuthClientID,
			RedirectURI: cfg.OAuthRedirectURI,
			Scopes:      cfg.OAuthScopes,
			// AuthorizationServer will be resolved from apiHost
		}
	}

	// Register OAuth protected resource metadata endpoints
	oauthCfg := &oauth.Config{
		BaseURL:           cfg.BaseURL,
		ResourcePath:      cfg.ResourcePath,
		ElicitationConfig: elicitationCfg,
		ClientSecret:      cfg.OAuthClientSecret,
	}

	serverOptions := []HandlerOption{}
	if cfg.ScopeChallenge {
		scopeFetcher := scopes.NewFetcher(apiHost, scopes.FetcherOptions{})
		serverOptions = append(serverOptions, WithScopeFetcher(scopeFetcher))
	}

	r := chi.NewRouter()
	handler := NewHTTPMcpHandler(ctx, &cfg, deps, t, logger, apiHost, append(serverOptions, WithFeatureChecker(featureChecker), WithOAuthConfig(oauthCfg))...)
	oauthHandler, err := oauth.NewAuthHandler(oauthCfg, apiHost)
	if err != nil {
		return fmt.Errorf("failed to create OAuth handler: %w", err)
	}

	// Public routes (no authentication required) - register FIRST
	r.Group(func(r chi.Router) {
		// Serve demo page if configured
		if cfg.DemoPagePath != "" {
			r.Get("/demo", func(w http.ResponseWriter, r *http.Request) {
				// Prevent browser caching of demo page
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
				w.Header().Set("Pragma", "no-cache")
				w.Header().Set("Expires", "0")
				http.ServeFile(w, r, cfg.DemoPagePath)
			})
			r.Get("/callback", func(w http.ResponseWriter, r *http.Request) {
				// Redirect callback to demo page with query params
				http.Redirect(w, r, "/demo?"+r.URL.RawQuery, http.StatusFound)
			})
			r.Get("/", func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/demo", http.StatusFound)
			})
			logger.Info("Demo page registered", "path", cfg.DemoPagePath)
		}

		// Register OAuth elicitation endpoints if configured
		if err := oauthHandler.RegisterElicitationRoutes(r); err != nil {
			logger.Error("failed to register elicitation routes", "error", err)
		}

		// Register token exchange endpoint if configured
		oauthHandler.RegisterTokenExchangeRoute(r)

		// Register OAuth protected resource metadata endpoints
		oauthHandler.RegisterRoutes(r)
	})
	logger.Info("OAuth protected resource endpoints registered", "baseURL", cfg.BaseURL)

	// Protected routes (authentication required)
	r.Group(func(r chi.Router) {
		// Register Middleware First, needs to be before route registration
		handler.RegisterMiddleware(r)

		// Register MCP server routes (auth required)
		handler.RegisterRoutes(r)
	})
	logger.Info("MCP endpoints registered", "baseURL", cfg.BaseURL)

	addr := fmt.Sprintf(":%d", cfg.Port)
	httpSvr := http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		logger.Info("shutting down server")
		if err := httpSvr.Shutdown(shutdownCtx); err != nil {
			logger.Error("error during server shutdown", "error", err)
		}
	}()

	if cfg.ExportTranslations {
		// Once server is initialized, all translations are loaded
		dumpTranslations()
	}

	logger.Info("HTTP server listening", "addr", addr)
	if err := httpSvr.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP server error: %w", err)
	}

	logger.Info("server stopped gracefully")
	return nil
}

func initGlobalToolScopeMap(t translations.TranslationHelperFunc) error {
	// Build inventory with all tools to extract scope information
	inv, err := inventory.NewBuilder().
		SetTools(github.AllTools(t)).
		Build()

	if err != nil {
		return fmt.Errorf("failed to build inventory for tool scope map: %w", err)
	}

	// Initialize the global scope map
	scopes.SetToolScopeMapFromInventory(inv)

	return nil
}

// createHTTPFeatureChecker creates a feature checker that reads header features from context
// and validates them against the knownFeatureFlags whitelist
func createHTTPFeatureChecker() inventory.FeatureFlagChecker {
	// Pre-compute whitelist as set for O(1) lookup
	knownSet := make(map[string]bool, len(knownFeatureFlags))
	for _, f := range knownFeatureFlags {
		knownSet[f] = true
	}

	return func(ctx context.Context, flag string) (bool, error) {
		if knownSet[flag] && slices.Contains(ghcontext.GetHeaderFeatures(ctx), flag) {
			return true, nil
		}
		return false, nil
	}
}
