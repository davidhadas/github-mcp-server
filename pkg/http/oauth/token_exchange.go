package oauth

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

// TokenExchangeHandler handles OAuth token exchange requests.
type TokenExchangeHandler struct {
	clientID     string
	clientSecret string
	redirectURI  string
	tokenURL     string
	logger       *slog.Logger
}

// NewTokenExchangeHandler creates a new token exchange handler.
func NewTokenExchangeHandler(clientID, clientSecret, redirectURI, tokenURL string, logger *slog.Logger) *TokenExchangeHandler {
	return &TokenExchangeHandler{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		tokenURL:     tokenURL,
		logger:       logger,
	}
}

// HandleTokenExchange handles POST /oauth/exchange-token requests.
// This endpoint exchanges an authorization code for an access token.
func (h *TokenExchangeHandler) HandleTokenExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Code == "" {
		http.Error(w, "Missing authorization code", http.StatusBadRequest)
		return
	}

	if req.CodeVerifier == "" {
		http.Error(w, "Missing code_verifier (required for PKCE)", http.StatusBadRequest)
		return
	}

	// Prepare token exchange request
	data := url.Values{}
	data.Set("client_id", h.clientID)
	data.Set("client_secret", h.clientSecret)
	data.Set("code", req.Code)
	data.Set("code_verifier", req.CodeVerifier)
	data.Set("redirect_uri", h.redirectURI)

	h.logger.Info("Exchanging authorization code for token",
		"client_id", h.clientID,
		"redirect_uri", h.redirectURI,
		"token_url", h.tokenURL,
		"has_code_verifier", req.CodeVerifier != "")

	tokenReq, err := http.NewRequest("POST", h.tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		h.logger.Error("Failed to create token request", "error", err)
		http.Error(w, fmt.Sprintf("Failed to create token request: %v", err), http.StatusInternalServerError)
		return
	}

	tokenReq.Header.Set("Accept", "application/json")
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Execute token exchange
	client := &http.Client{}
	resp, err := client.Do(tokenReq)
	if err != nil {
		h.logger.Error("Failed to exchange token", "error", err)
		http.Error(w, fmt.Sprintf("Failed to exchange token: %v", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.logger.Error("Failed to read response", "error", err)
		http.Error(w, fmt.Sprintf("Failed to read response: %v", err), http.StatusInternalServerError)
		return
	}

	if resp.StatusCode != http.StatusOK {
		h.logger.Error("Token exchange failed",
			"status", resp.StatusCode,
			"response", string(body))
	} else {
		h.logger.Info("Token exchange successful")
	}

	// Forward the response from GitHub
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// Made with Bob
