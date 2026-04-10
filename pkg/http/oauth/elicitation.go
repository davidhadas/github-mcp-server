package oauth

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/github/github-mcp-server/pkg/oauth"
	"github.com/github/github-mcp-server/pkg/utils"
)

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
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	h.logger.Info("Received auth URL request")

	// Parse the request body
	var req oauth.AuthURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// If authorization server is not configured, try to resolve it from the API host
	if h.config.AuthorizationServer == "" && h.apiHost != nil {
		authURL, err := h.apiHost.AuthorizationServerURL(r.Context())
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to resolve authorization server: %v", err), http.StatusInternalServerError)
			return
		}
		h.config.AuthorizationServer = authURL.String()
	}

	// Build the authorization URL
	resp, err := h.config.BuildAuthorizationURL(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to build authorization URL: %v", err), http.StatusInternalServerError)
		return
	}

	h.logger.Info("Built authorization URL", "callback_url", req.CallbackURL)

	// Return the response as JSON
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// Made with Bob
