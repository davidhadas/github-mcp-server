package github

import (
	"context"
	"fmt"

	"github.com/github/github-mcp-server/pkg/oauth"
	"github.com/github/github-mcp-server/pkg/utils"
)

// AuthElicitationHandler handles MCP auth/url requests for GitHub OAuth.
type AuthElicitationHandler struct {
	config  *oauth.ElicitationConfig
	apiHost utils.APIHostResolver
}

// NewAuthElicitationHandler creates a new auth elicitation handler.
func NewAuthElicitationHandler(config *oauth.ElicitationConfig, apiHost utils.APIHostResolver) (*AuthElicitationHandler, error) {
	if config == nil {
		return nil, fmt.Errorf("elicitation config is required")
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid elicitation config: %w", err)
	}

	return &AuthElicitationHandler{
		config:  config,
		apiHost: apiHost,
	}, nil
}

// HandleAuthURL processes an auth/url request and returns the authorization URL.
func (h *AuthElicitationHandler) HandleAuthURL(ctx context.Context, req oauth.AuthURLRequest) (*oauth.AuthURLResponse, error) {
	// If authorization server is not configured, try to resolve it from the API host
	if h.config.AuthorizationServer == "" && h.apiHost != nil {
		authURL, err := h.apiHost.AuthorizationServerURL(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve authorization server URL: %w", err)
		}
		h.config.AuthorizationServer = authURL.String()
	}

	// Build the authorization URL
	resp, err := h.config.BuildAuthorizationURL(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build authorization URL: %w", err)
	}

	return resp, nil
}

// Made with Bob
