package oauthflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// Discoverer performs OAuth discovery against an MCP server.
type Discoverer struct {
	logger *slog.Logger
	client *http.Client
}

// NewDiscoverer creates a new OAuth discoverer.
func NewDiscoverer(logger *slog.Logger) *Discoverer {
	return &Discoverer{
		logger: logger,
		client: &http.Client{
			Timeout: 30 * http.DefaultClient.Timeout,
		},
	}
}

// SendSyntheticRequest sends an unauthenticated MCP request to trigger OAuth elicitation.
// Returns true if the server responds with 401, false otherwise.
func (d *Discoverer) SendSyntheticRequest(ctx context.Context, mcpServerURL string) (bool, error) {
	// Build synthetic tools/list request
	requestBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]interface{}{},
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return false, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send POST request to /mcp endpoint without Authorization header
	mcpEndpoint := strings.TrimSuffix(mcpServerURL, "/") + "/mcp"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	d.logger.Debug("Sending synthetic MCP request",
		"endpoint", mcpEndpoint,
		"method", "tools/list")

	resp, err := d.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for logging
	body, _ := io.ReadAll(resp.Body)

	d.logger.Debug("Synthetic MCP request response",
		"status", resp.StatusCode,
		"www_authenticate", resp.Header.Get("WWW-Authenticate"),
		"body_length", len(body))

	// Check if we got 401 Unauthorized
	if resp.StatusCode == http.StatusUnauthorized {
		d.logger.Info("OAuth elicitation triggered (401 response)",
			"mcp_server", mcpServerURL)
		return true, nil
	}

	// If we got a different status, OAuth might not be required
	d.logger.Warn("Expected 401 but got different status",
		"status", resp.StatusCode,
		"mcp_server", mcpServerURL)

	return false, fmt.Errorf("expected 401 Unauthorized, got %d", resp.StatusCode)
}

// DiscoverAuthServer discovers the OAuth authorization server for an MCP server.
func (d *Discoverer) DiscoverAuthServer(ctx context.Context, mcpServerURL string) (string, error) {
	// Call /.well-known/oauth-protected-resource endpoint
	wellKnownURL := strings.TrimSuffix(mcpServerURL, "/") + "/.well-known/oauth-protected-resource"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create discovery request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	d.logger.Debug("Discovering OAuth authorization server",
		"well_known_url", wellKnownURL)

	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send discovery request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("discovery endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read discovery response: %w", err)
	}

	var discoveryResponse struct {
		AuthorizationServers []string `json:"authorization_servers"`
	}

	if err := json.Unmarshal(body, &discoveryResponse); err != nil {
		return "", fmt.Errorf("failed to parse discovery response: %w", err)
	}

	if len(discoveryResponse.AuthorizationServers) == 0 {
		return "", fmt.Errorf("no authorization servers found in discovery response")
	}

	authServerURL := discoveryResponse.AuthorizationServers[0]

	d.logger.Info("OAuth authorization server discovered",
		"mcp_server", mcpServerURL,
		"auth_server", authServerURL)

	return authServerURL, nil
}

// GetAuthURL requests an authorization URL from the MCP server.
// The MCP server generates PKCE and returns both the auth URL and code_verifier.
func (d *Discoverer) GetAuthURL(ctx context.Context, mcpServerURL, callbackURL string) (string, string, error) {
	authURLEndpoint := strings.TrimSuffix(mcpServerURL, "/") + "/auth/url"

	requestBody := map[string]string{
		"callback_url": callbackURL,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal auth URL request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURLEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", "", fmt.Errorf("failed to create auth URL request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	d.logger.Debug("Requesting authorization URL from MCP server",
		"endpoint", authURLEndpoint,
		"callback_url", callbackURL)

	resp, err := d.client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to send auth URL request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read auth URL response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("auth URL endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var authURLResponse struct {
		URL          string `json:"url"`
		CodeVerifier string `json:"code_verifier"`
	}

	if err := json.Unmarshal(body, &authURLResponse); err != nil {
		return "", "", fmt.Errorf("failed to parse auth URL response: %w", err)
	}

	if authURLResponse.URL == "" {
		return "", "", fmt.Errorf("no authorization URL in response")
	}

	if authURLResponse.CodeVerifier == "" {
		return "", "", fmt.Errorf("no code_verifier in response")
	}

	d.logger.Info("Authorization URL obtained from MCP server",
		"mcp_server", mcpServerURL,
		"auth_url_length", len(authURLResponse.URL),
		"code_verifier_length", len(authURLResponse.CodeVerifier))

	return authURLResponse.URL, authURLResponse.CodeVerifier, nil
}

// Made with Bob
