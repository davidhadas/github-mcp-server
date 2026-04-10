package oauth

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/github/github-mcp-server/pkg/oauth"
	"github.com/github/github-mcp-server/pkg/utils"
)

// ErrorResponse represents a structured OAuth error response.
type ErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// writeJSONError writes a structured JSON error response.
func writeJSONError(w http.ResponseWriter, statusCode int, errorCode, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(ErrorResponse{
		Error:            errorCode,
		ErrorDescription: description,
	})
}

// ElicitationHandler handles MCP auth/url HTTP requests.
type ElicitationHandler struct {
	config  *oauth.ElicitationConfig
	apiHost utils.APIHostResolver
	logger  *slog.Logger
}

// NewElicitationHandler creates a new elicitation handler for HTTP endpoints.
func NewElicitationHandler(config *oauth.ElicitationConfig, apiHost utils.APIHostResolver, logger *slog.Logger) (*ElicitationHandler, error) {
	if config == nil {
		return nil, fmt.Errorf("elicitation config is required")
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid elicitation config: %w", err)
	}

	return &ElicitationHandler{
		config:  config,
		apiHost: apiHost,
		logger:  logger,
	}, nil
}

// HandleAuthURL handles POST /auth/url requests.
// This implements the MCP elicitation protocol via HTTP.
func (h *ElicitationHandler) HandleAuthURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "invalid_request", "Method not allowed")
		return
	}

	h.logger.Info("Received auth URL request")

	// Parse the request body
	var req oauth.AuthURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("Failed to decode request body", "error", err)
		writeJSONError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("Invalid request body: %v", err))
		return
	}

	// Validate callback_url if provided
	if req.CallbackURL != "" {
		if _, err := url.Parse(req.CallbackURL); err != nil {
			h.logger.Error("Invalid callback_url", "error", err, "callback_url", req.CallbackURL)
			writeJSONError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("Invalid callback_url: %v", err))
			return
		}
	}

	// If authorization server is not configured, try to resolve it from the API host
	if h.config.AuthorizationServer == "" && h.apiHost != nil {
		authURL, err := h.apiHost.AuthorizationServerURL(r.Context())
		if err != nil {
			h.logger.Error("Failed to resolve authorization server", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "server_error", fmt.Sprintf("Failed to resolve authorization server: %v", err))
			return
		}
		h.config.AuthorizationServer = authURL.String()
	}

	// Build the authorization URL
	resp, err := h.config.BuildAuthorizationURL(req)
	if err != nil {
		h.logger.Error("Failed to build authorization URL", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "server_error", fmt.Sprintf("Failed to build authorization URL: %v", err))
		return
	}

	h.logger.Info("Built authorization URL", "callback_url", req.CallbackURL)

	// Return the response as JSON
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		h.logger.Error("Failed to encode response", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "server_error", fmt.Sprintf("Failed to encode response: %v", err))
		return
	}
}

// Made with Bob
