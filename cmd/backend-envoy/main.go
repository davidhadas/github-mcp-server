// Package main provides the Envoy-based backend service entry point.
package main

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/github/github-mcp-server/internal/backend/envoy"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Config holds the backend configuration.
type Config struct {
	Port           int
	TokenBrokerURL string
	DemoPagePath   string
}

// LoadConfig loads configuration from environment variables.
func LoadConfig() *Config {
	cfg := &Config{
		Port:           8187,
		TokenBrokerURL: "http://token-broker-service:8190",
		DemoPagePath:   "",
	}

	// Override from environment
	if port := os.Getenv("BACKEND_PORT"); port != "" {
		fmt.Sscanf(port, "%d", &cfg.Port)
	}

	if brokerURL := os.Getenv("TOKEN_BROKER_URL"); brokerURL != "" {
		cfg.TokenBrokerURL = brokerURL
	}

	if demoPath := os.Getenv("DEMO_PAGE_PATH"); demoPath != "" {
		cfg.DemoPagePath = demoPath
	}

	return cfg
}

func main() {
	// Initialize logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("Starting Envoy-based Backend service")

	// Load configuration
	cfg := LoadConfig()

	logger.Info("Configuration loaded",
		"port", cfg.Port,
		"token_broker_url", cfg.TokenBrokerURL,
		"demo_page_path", cfg.DemoPagePath)

	// Create session manager
	sessionManager := envoy.NewSessionManager(cfg.TokenBrokerURL)

	// Create job manager
	jobManager := envoy.NewJobManager(logger)

	// Link job manager to session manager for OAuth event handling
	sessionManager.SetJobManager(jobManager)

	// Start job cleanup routine (clean up jobs older than 1 hour every 10 minutes)
	jobManager.StartCleanupRoutine(10*time.Minute, 1*time.Hour)

	// Create handler
	handler := envoy.NewHandler(sessionManager, jobManager, logger)

	// Setup HTTP router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(corsMiddleware)

	// API endpoints
	r.Post("/task", handler.HandleTask)
	r.Get("/job/*", handler.HandleJobStatus) // Job status polling endpoint
	r.Get("/oauth/callback", handler.HandleOAuthCallback)
	r.Get("/callback", handler.HandleOAuthCallback)
	r.Get("/events", handler.HandleEvents)
	r.Post("/session/end", handler.HandleEndSession)
	r.Get("/health", handler.HandleHealth)

	// Serve demo page if configured
	if cfg.DemoPagePath != "" {
		r.Get("/demo", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
			http.ServeFile(w, r, cfg.DemoPagePath)
		})
		logger.Info("Demo page registered", "path", cfg.DemoPagePath)
	}

	// Create HTTP server
	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 320 * time.Second, // Longer for SSE
		IdleTimeout:  120 * time.Second,
	}

	// Start server in goroutine
	go func() {
		logger.Info("Backend service listening",
			"address", addr,
			"demo_url", fmt.Sprintf("http://localhost:%d/demo", cfg.Port))

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	logger.Info("Shutting down Backend service")

	// Graceful shutdown
	// Note: In production, we should also clean up all sessions here
	logger.Info("Backend service stopped")
}

// corsMiddleware adds CORS headers.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Made with Bob
