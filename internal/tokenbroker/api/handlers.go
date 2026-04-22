package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/github/github-mcp-server/internal/tokenbroker/core"
	"github.com/go-chi/chi/v5"
)

// Handler handles HTTP requests for the Token Broker API.
type Handler struct {
	broker       *core.TokenBroker
	sessionStore core.SessionStore
	logger       *slog.Logger
}

// NewHandler creates a new API handler.
func NewHandler(broker *core.TokenBroker, sessionStore core.SessionStore, logger *slog.Logger) *Handler {
	return &Handler{
		broker:       broker,
		sessionStore: sessionStore,
		logger:       logger,
	}
}

// RegisterRoutes registers all API routes.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/sessions", h.HandleCreateSession)
	r.Post("/sessions/{sessionKey}/token", h.HandleGetToken)
	r.Post("/sessions/{sessionKey}/events", h.HandleEvents)
	r.Post("/sessions/{sessionKey}/end", h.HandleEndSession)
	r.Post("/ext_authz/*", h.HandleExtAuthz)
}

// ErrorResponse represents a standardized error response.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains error details.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeError writes a standardized error response.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
		},
	})
}

// writeMCPError writes an MCP JSON-RPC error response (for ext_authz).
func writeMCPError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	response := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      nil,
		"error": map[string]interface{}{
			"code":    -32001,
			"message": message,
		},
	}
	json.NewEncoder(w).Encode(response)
}

// HandleCreateSession handles POST /sessions
func (h *Handler) HandleCreateSession(w http.ResponseWriter, r *http.Request) {
	// Extract user ID from header
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		h.logger.Warn("Missing X-User-ID header")
		writeError(w, http.StatusBadRequest, "invalid_request", "Missing X-User-ID header")
		return
	}

	h.logger.Info("Creating session", "user_id", userID)

	// Create session
	sessionKey, err := h.sessionStore.CreateSession(userID)
	if err != nil {
		if err.Error() == "max sessions per user exceeded" {
			writeError(w, http.StatusTooManyRequests, "too_many_sessions", err.Error())
			return
		}
		h.logger.Error("Failed to create session", "error", err, "user_id", userID)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create session")
		return
	}

	h.logger.Info("Session created", "session_key", sessionKey, "user_id", userID)

	// Return session key
	writeJSON(w, http.StatusCreated, map[string]string{
		"oauth_session_key": sessionKey,
	})
}

// HandleGetToken handles POST /sessions/{sessionKey}/token
func (h *Handler) HandleGetToken(w http.ResponseWriter, r *http.Request) {
	sessionKey := chi.URLParam(r, "sessionKey")
	userID := r.Header.Get("X-User-ID")
	mcpServerURL := r.Header.Get("X-Mcp-Server-Url")

	if userID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Missing X-User-ID header")
		return
	}

	if mcpServerURL == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Missing X-Mcp-Server-Url header")
		return
	}

	h.logger.Info("Token request",
		"session_key", sessionKey,
		"user_id", userID,
		"mcp_server", mcpServerURL)

	// Validate session
	if err := h.sessionStore.ValidateSession(sessionKey, userID); err != nil {
		h.logger.Warn("Session validation failed",
			"session_key", sessionKey,
			"user_id", userID,
			"error", err)
		writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid session or user mismatch")
		return
	}

	// Acquire token (blocks until available or timeout)
	ctx := r.Context()
	token, err := h.broker.AcquireToken(ctx, sessionKey, userID, mcpServerURL)
	if err != nil {
		h.logger.Error("Token acquisition failed",
			"session_key", sessionKey,
			"user_id", userID,
			"mcp_server", mcpServerURL,
			"error", err)

		// Determine error type
		if err.Error() == "timeout waiting for OAuth completion" {
			writeError(w, http.StatusRequestTimeout, "timeout", "OAuth flow did not complete in time")
			return
		}
		if err.Error() == "session expired" || err.Error() == "session ended" {
			writeError(w, http.StatusUnauthorized, "session_expired", err.Error())
			return
		}

		writeError(w, http.StatusServiceUnavailable, "oauth_failed", fmt.Sprintf("Failed to obtain token: %v", err))
		return
	}

	h.logger.Info("Token acquired",
		"session_key", sessionKey,
		"user_id", userID,
		"mcp_server", mcpServerURL)

	// Return token
	writeJSON(w, http.StatusOK, map[string]string{
		"token": token,
	})
}

// HandleEvents handles POST /sessions/{sessionKey}/events
func (h *Handler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	sessionKey := chi.URLParam(r, "sessionKey")
	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Missing X-User-ID header")
		return
	}

	// Check if this is an OAuth completion (code and state in query params)
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code != "" && state != "" {
		// OAuth completion
		h.handleOAuthCompletion(w, r, sessionKey, userID, code, state)
		return
	}

	// Long-poll for events
	h.handleEventLongPoll(w, r, sessionKey, userID)
}

// handleOAuthCompletion handles OAuth completion callback.
func (h *Handler) handleOAuthCompletion(w http.ResponseWriter, r *http.Request, sessionKey, userID, code, state string) {
	h.logger.Info("OAuth completion received",
		"session_key", sessionKey,
		"user_id", userID,
		"code_length", len(code),
		"state_length", len(state))

	// Validate session
	if err := h.sessionStore.ValidateSession(sessionKey, userID); err != nil {
		h.logger.Warn("Session validation failed for OAuth completion",
			"session_key", sessionKey,
			"user_id", userID,
			"error", err)
		writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid session or user mismatch")
		return
	}

	// Complete OAuth flow (with user_id validation)
	if err := h.broker.CompleteOAuth(sessionKey, userID, code, state); err != nil {
		h.logger.Error("Failed to complete OAuth",
			"session_key", sessionKey,
			"user_id", userID,
			"error", err)
		writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("Failed to complete OAuth: %v", err))
		return
	}

	h.logger.Info("OAuth completion processed",
		"session_key", sessionKey)

	// Return success
	w.WriteHeader(http.StatusOK)
}

// handleEventLongPoll handles long-polling for events.
func (h *Handler) handleEventLongPoll(w http.ResponseWriter, r *http.Request, sessionKey, userID string) {
	h.logger.Debug("Event long-poll started",
		"session_key", sessionKey,
		"user_id", userID)

	// Validate session
	if err := h.sessionStore.ValidateSession(sessionKey, userID); err != nil {
		h.logger.Warn("Session validation failed for event poll",
			"session_key", sessionKey,
			"user_id", userID,
			"error", err)
		writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid session or user mismatch")
		return
	}

	// Reset session timer (Backend is connected)
	h.sessionStore.ResetSessionTimer(sessionKey)

	// Get session
	session, err := h.sessionStore.GetSession(sessionKey)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "session_not_found", "Session not found")
		return
	}

	// Wait for event (with timeout - 60 seconds to stay under typical proxy timeouts)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	// When we return, start the session timer
	defer h.sessionStore.StartSessionTimer(sessionKey)

	select {
	case event := <-session.EventWaiters:
		h.logger.Info("Event sent to Backend",
			"session_key", sessionKey,
			"event_type", event.Type)
		writeJSON(w, http.StatusOK, event)

	case <-ctx.Done():
		// Timeout or client disconnect (ctx.Done() covers both cases)
		h.logger.Debug("Event long-poll ended",
			"session_key", sessionKey,
			"reason", ctx.Err())
		// Return empty response on timeout (Backend will reconnect)
		w.WriteHeader(http.StatusNoContent)
	}
}

// HandleEndSession handles POST /sessions/{sessionKey}/end
func (h *Handler) HandleEndSession(w http.ResponseWriter, r *http.Request) {
	sessionKey := chi.URLParam(r, "sessionKey")
	userID := r.Header.Get("X-User-ID")

	if userID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Missing X-User-ID header")
		return
	}

	h.logger.Info("Ending session",
		"session_key", sessionKey,
		"user_id", userID)

	// Validate session
	if err := h.sessionStore.ValidateSession(sessionKey, userID); err != nil {
		h.logger.Warn("Session validation failed for end session",
			"session_key", sessionKey,
			"user_id", userID,
			"error", err)
		writeError(w, http.StatusUnauthorized, "unauthorized", "Invalid session or user mismatch")
		return
	}

	// End session
	if err := h.sessionStore.EndSession(sessionKey); err != nil {
		h.logger.Error("Failed to end session",
			"session_key", sessionKey,
			"error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to end session")
		return
	}

	h.logger.Info("Session ended",
		"session_key", sessionKey)

	w.WriteHeader(http.StatusOK)
}

// HandleExtAuthz handles POST /ext_authz (Envoy ext_authz compatibility)
func (h *Handler) HandleExtAuthz(w http.ResponseWriter, r *http.Request) {
	sessionKey := r.Header.Get("X-OAuth-Session-Key")
	userID := r.Header.Get("X-User-ID")
	mcpServerURL := r.Header.Get("X-Mcp-Server-Url")

	if sessionKey == "" || userID == "" || mcpServerURL == "" {
		h.logger.Warn("Missing required headers for ext_authz",
			"has_session_key", sessionKey != "",
			"has_user_id", userID != "",
			"has_mcp_server_url", mcpServerURL != "")
		writeMCPError(w, http.StatusForbidden, "Failed to obtain user authorization for this MCP server.")
		return
	}

	h.logger.Debug("ext_authz request",
		"session_key", sessionKey,
		"user_id", userID,
		"mcp_server", mcpServerURL)

	// Validate session
	if err := h.sessionStore.ValidateSession(sessionKey, userID); err != nil {
		h.logger.Warn("Session validation failed for ext_authz",
			"session_key", sessionKey,
			"user_id", userID,
			"error", err)
		writeMCPError(w, http.StatusForbidden, "Failed to obtain user authorization for this MCP server.")
		return
	}

	// Acquire token (blocks until available or timeout)
	ctx := r.Context()
	token, err := h.broker.AcquireToken(ctx, sessionKey, userID, mcpServerURL)
	if err != nil {
		h.logger.Error("Token acquisition failed for ext_authz",
			"session_key", sessionKey,
			"user_id", userID,
			"mcp_server", mcpServerURL,
			"error", err)
		writeMCPError(w, http.StatusForbidden, "Failed to obtain user authorization for this MCP server.")
		return
	}

	h.logger.Info("Token acquired for ext_authz",
		"session_key", sessionKey,
		"user_id", userID,
		"mcp_server", mcpServerURL)

	// Return success with Authorization header
	w.Header().Set("Authorization", fmt.Sprintf("Bearer %s", token))
	w.WriteHeader(http.StatusOK)
}

// Made with Bob
