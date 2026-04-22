// Package main provides the Token Broker service entry point.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/github/github-mcp-server/internal/tokenbroker/api"
	"github.com/github/github-mcp-server/internal/tokenbroker/cache"
	"github.com/github/github-mcp-server/internal/tokenbroker/core"
	"github.com/github/github-mcp-server/internal/tokenbroker/oauthflow"
	"github.com/github/github-mcp-server/internal/tokenbroker/session"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Config holds the Token Broker configuration.
type Config struct {
	// ListenPort is the HTTP server port.
	ListenPort int

	// CallbackURL is the OAuth callback URL passed to MCP servers.
	// This is where the OAuth provider redirects after user authorization.
	CallbackURL string

	// SessionTimeout is the idle timeout for sessions after Backend disconnect.
	SessionTimeout time.Duration

	// MaxSessionsPerUser is the maximum number of concurrent sessions per user.
	MaxSessionsPerUser int

	// TokenWaitTimeout is the maximum time to wait for OAuth completion.
	TokenWaitTimeout time.Duration
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		ListenPort:         8190,
		CallbackURL:        "http://backend-service:8187/callback",
		SessionTimeout:     60 * time.Second,
		MaxSessionsPerUser: 5,
		TokenWaitTimeout:   300 * time.Second,
	}
}

// LoadConfig loads configuration from environment variables.
func LoadConfig() *Config {
	cfg := DefaultConfig()

	// Override from environment variables
	if port := os.Getenv("TOKEN_BROKER_PORT"); port != "" {
		fmt.Sscanf(port, "%d", &cfg.ListenPort)
	}

	if callbackURL := os.Getenv("TOKEN_BROKER_CALLBACK_URL"); callbackURL != "" {
		cfg.CallbackURL = callbackURL
	}

	if timeout := os.Getenv("TOKEN_BROKER_SESSION_TIMEOUT"); timeout != "" {
		if d, err := time.ParseDuration(timeout); err == nil {
			cfg.SessionTimeout = d
		}
	}

	if maxSessions := os.Getenv("TOKEN_BROKER_MAX_SESSIONS_PER_USER"); maxSessions != "" {
		fmt.Sscanf(maxSessions, "%d", &cfg.MaxSessionsPerUser)
	}

	if waitTimeout := os.Getenv("TOKEN_BROKER_TOKEN_WAIT_TIMEOUT"); waitTimeout != "" {
		if d, err := time.ParseDuration(waitTimeout); err == nil {
			cfg.TokenWaitTimeout = d
		}
	}

	return cfg
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	if c.ListenPort <= 0 || c.ListenPort > 65535 {
		return fmt.Errorf("invalid listen port: %d", c.ListenPort)
	}

	if c.CallbackURL == "" {
		return fmt.Errorf("callback URL is required")
	}

	if c.SessionTimeout <= 0 {
		return fmt.Errorf("session timeout must be positive")
	}

	if c.MaxSessionsPerUser <= 0 {
		return fmt.Errorf("max sessions per user must be positive")
	}

	if c.TokenWaitTimeout <= 0 {
		return fmt.Errorf("token wait timeout must be positive")
	}

	return nil
}

func main() {
	// Initialize logger (JSON format for Kubernetes)
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("Starting Token Broker service")

	// Load configuration
	cfg := LoadConfig()
	if err := cfg.Validate(); err != nil {
		logger.Error("Invalid configuration", "error", err)
		os.Exit(1)
	}

	logger.Info("Configuration loaded",
		"listen_port", cfg.ListenPort,
		"callback_url", cfg.CallbackURL,
		"session_timeout", cfg.SessionTimeout,
		"max_sessions_per_user", cfg.MaxSessionsPerUser,
		"token_wait_timeout", cfg.TokenWaitTimeout)

	// Initialize components
	clock := &core.RealClock{}
	tokenCache := cache.NewTokenCache(clock)
	sessionManager := session.NewSessionManager(cfg.SessionTimeout, cfg.MaxSessionsPerUser, clock, logger)
	oauthDiscoverer := oauthflow.NewDiscoverer(logger)
	tokenExchanger := oauthflow.NewTokenExchanger(logger)

	// Create Token Broker
	broker := core.NewTokenBroker(
		sessionManager,
		tokenCache,
		oauthDiscoverer,
		tokenExchanger,
		cfg.CallbackURL,
		cfg.TokenWaitTimeout,
		logger,
	)

	// Create HTTP router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(cfg.TokenWaitTimeout + 10*time.Second)) // Slightly longer than token wait

	// Register API handlers
	apiHandler := api.NewHandler(broker, sessionManager, logger)
	apiHandler.RegisterRoutes(r)

	// Create HTTP server
	addr := fmt.Sprintf(":%d", cfg.ListenPort)
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: cfg.TokenWaitTimeout + 30*time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		logger.Info("Token Broker listening", "address", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	logger.Info("Shutting down Token Broker")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("Server shutdown error", "error", err)
	}

	// Cleanup sessions
	sessionManager.Shutdown()

	logger.Info("Token Broker stopped")
}

// Made with Bob
