package envoy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// TokenBrokerClient is a client for the Token Broker service.
type TokenBrokerClient struct {
	baseURL string
	client  *http.Client
}

// NewTokenBrokerClient creates a new Token Broker client.
func NewTokenBrokerClient(baseURL string) *TokenBrokerClient {
	return &TokenBrokerClient{
		baseURL: baseURL,
		client: &http.Client{
			Timeout: 320 * time.Second, // Longer than Token Broker's 300s timeout
		},
	}
}

// CreateSession creates a new session with the Token Broker.
func (c *TokenBrokerClient) CreateSession(ctx context.Context, userID string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/sessions", nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-User-ID", userID)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create session: status %d, body: %s", resp.StatusCode, string(body))
	}

	var result struct {
		OAuthSessionKey string `json:"oauth_session_key"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	return result.OAuthSessionKey, nil
}

// PollEvents polls for events from the Token Broker (long-polling).
func (c *TokenBrokerClient) PollEvents(ctx context.Context, sessionKey, userID string) (*Event, error) {
	url := fmt.Sprintf("%s/sessions/%s/events", c.baseURL, sessionKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-User-ID", userID)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// 204 No Content means timeout (no events)
	if resp.StatusCode == http.StatusNoContent {
		return nil, fmt.Errorf("no events (timeout)")
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to poll events: status %d, body: %s", resp.StatusCode, string(body))
	}

	var event Event
	if err := json.NewDecoder(resp.Body).Decode(&event); err != nil {
		return nil, fmt.Errorf("failed to decode event: %w", err)
	}

	return &event, nil
}

// CompleteOAuth completes an OAuth flow by sending the authorization code to the Token Broker.
func (c *TokenBrokerClient) CompleteOAuth(ctx context.Context, sessionKey, userID, code, state string) error {
	url := fmt.Sprintf("%s/sessions/%s/events?code=%s&state=%s", c.baseURL, sessionKey, code, state)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-User-ID", userID)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to complete OAuth: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// EndSession ends a session with the Token Broker.
func (c *TokenBrokerClient) EndSession(ctx context.Context, sessionKey, userID string) error {
	url := fmt.Sprintf("%s/sessions/%s/end", c.baseURL, sessionKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("X-User-ID", userID)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to end session: status %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// ForwardToAgent forwards a request to the AI Agent with the session key header.
func (c *TokenBrokerClient) ForwardToAgent(ctx context.Context, agentURL, sessionKey, userID string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, agentURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-OAuth-Session-Key", sessionKey)
	req.Header.Set("X-User-ID", userID)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	return resp, nil
}

// Made with Bob
