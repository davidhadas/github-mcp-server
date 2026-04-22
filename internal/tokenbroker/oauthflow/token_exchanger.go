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

	"github.com/github/github-mcp-server/internal/tokenbroker/cache"
)

// TokenExchanger exchanges an authorization code for an access token.
type TokenExchanger struct {
	logger *slog.Logger
	client *http.Client
}

// NewTokenExchanger creates a new token exchanger.
func NewTokenExchanger(logger *slog.Logger) *TokenExchanger {
	return &TokenExchanger{
		logger: logger,
		client: &http.Client{
			Timeout: 30 * http.DefaultClient.Timeout,
		},
	}
}

// ExchangeToken exchanges an authorization code for an access token via the MCP server.
// The MCP server uses its client_secret to exchange with the OAuth provider.
func (te *TokenExchanger) ExchangeToken(ctx context.Context, mcpServerURL, code, codeVerifier string) (string, error) {
	// Call /oauth/exchange-token endpoint on MCP server
	exchangeEndpoint := strings.TrimSuffix(mcpServerURL, "/") + "/oauth/exchange-token"

	requestBody := map[string]string{
		"code":          code,
		"code_verifier": codeVerifier,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal token exchange request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, exchangeEndpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create token exchange request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	te.logger.Debug("Exchanging authorization code for token",
		"endpoint", exchangeEndpoint,
		"code_length", len(code),
		"code_verifier_length", len(codeVerifier))

	resp, err := te.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send token exchange request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read token exchange response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		te.logger.Error("Token exchange failed",
			"status", resp.StatusCode,
			"response", string(body))
		return "", fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse GitHub OAuth token response
	accessToken, expiresIn, err := cache.ParseGitHubTokenResponse(body)
	if err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}

	te.logger.Info("Token exchange successful",
		"mcp_server", mcpServerURL,
		"token_length", len(accessToken),
		"expires_in", expiresIn)

	return accessToken, nil
}

// Made with Bob
