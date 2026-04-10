package oauth

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeneratePKCEChallenge(t *testing.T) {
	t.Parallel()

	pkce, err := GeneratePKCEChallenge()
	require.NoError(t, err)
	require.NotNil(t, pkce)

	// Verify verifier is not empty
	assert.NotEmpty(t, pkce.Verifier)

	// Verify challenge is not empty
	assert.NotEmpty(t, pkce.Challenge)

	// Verify method is S256
	assert.Equal(t, "S256", pkce.Method)

	// Verify verifier and challenge are different
	assert.NotEqual(t, pkce.Verifier, pkce.Challenge)

	// Verify they are base64url encoded (no padding)
	assert.NotContains(t, pkce.Verifier, "=")
	assert.NotContains(t, pkce.Challenge, "=")
}

func TestGenerateState(t *testing.T) {
	t.Parallel()

	state, err := GenerateState()
	require.NoError(t, err)
	assert.NotEmpty(t, state)

	// Verify it's base64url encoded (no padding)
	assert.NotContains(t, state, "=")

	// Generate another state and verify they're different
	state2, err := GenerateState()
	require.NoError(t, err)
	assert.NotEqual(t, state, state2)
}

func TestElicitationConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cfg         ElicitationConfig
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid configuration",
			cfg: ElicitationConfig{
				ClientID:            "test-client-id",
				AuthorizationServer: "https://github.com/login/oauth",
				Scopes:              []string{"repo", "read:org"},
			},
			expectError: false,
		},
		{
			name: "valid with redirect URI",
			cfg: ElicitationConfig{
				ClientID:            "test-client-id",
				AuthorizationServer: "https://github.com/login/oauth",
				RedirectURI:         "http://localhost:3000/callback",
			},
			expectError: false,
		},
		{
			name: "missing client ID",
			cfg: ElicitationConfig{
				AuthorizationServer: "https://github.com/login/oauth",
			},
			expectError: true,
			errorMsg:    "OAuth client ID is required",
		},
		{
			name: "missing authorization server is allowed (can be resolved at runtime)",
			cfg: ElicitationConfig{
				ClientID: "test-client-id",
			},
			expectError: false,
		},
		{
			name: "invalid authorization server URL",
			cfg: ElicitationConfig{
				ClientID:            "test-client-id",
				AuthorizationServer: "://invalid-url",
			},
			expectError: true,
			errorMsg:    "invalid authorization server URL",
		},
		{
			name: "invalid redirect URI",
			cfg: ElicitationConfig{
				ClientID:            "test-client-id",
				AuthorizationServer: "https://github.com/login/oauth",
				RedirectURI:         "://invalid-url",
			},
			expectError: true,
			errorMsg:    "invalid redirect URI",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestElicitationConfig_BuildAuthorizationURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		cfg              ElicitationConfig
		req              AuthURLRequest
		expectError      bool
		errorMsg         string
		validateResponse func(t *testing.T, resp *AuthURLResponse)
	}{
		{
			name: "basic authorization URL with callback",
			cfg: ElicitationConfig{
				ClientID:            "test-client-id",
				AuthorizationServer: "https://github.com/login/oauth",
				Scopes:              []string{"repo", "read:org"},
			},
			req: AuthURLRequest{
				CallbackURL: "http://localhost:3000/callback",
			},
			expectError: false,
			validateResponse: func(t *testing.T, resp *AuthURLResponse) {
				assert.NotEmpty(t, resp.URL)

				parsedURL, err := url.Parse(resp.URL)
				require.NoError(t, err)

				// Verify base URL
				assert.Equal(t, "https", parsedURL.Scheme)
				assert.Equal(t, "github.com", parsedURL.Host)
				assert.Equal(t, "/login/oauth/authorize", parsedURL.Path)

				// Verify query parameters
				params := parsedURL.Query()
				assert.Equal(t, "test-client-id", params.Get("client_id"))
				assert.Equal(t, "http://localhost:3000/callback", params.Get("redirect_uri"))
				assert.Equal(t, "code", params.Get("response_type"))
				assert.Equal(t, "repo read:org", params.Get("scope"))
				assert.NotEmpty(t, params.Get("state"))
				assert.NotEmpty(t, params.Get("code_challenge"))
				assert.Equal(t, "S256", params.Get("code_challenge_method"))
			},
		},
		{
			name: "uses configured redirect URI when callback not provided",
			cfg: ElicitationConfig{
				ClientID:            "test-client-id",
				AuthorizationServer: "https://github.com/login/oauth",
				RedirectURI:         "http://localhost:8080/oauth/callback",
			},
			req:         AuthURLRequest{},
			expectError: false,
			validateResponse: func(t *testing.T, resp *AuthURLResponse) {
				parsedURL, err := url.Parse(resp.URL)
				require.NoError(t, err)

				params := parsedURL.Query()
				assert.Equal(t, "http://localhost:8080/oauth/callback", params.Get("redirect_uri"))
			},
		},
		{
			name: "callback URL overrides configured redirect URI",
			cfg: ElicitationConfig{
				ClientID:            "test-client-id",
				AuthorizationServer: "https://github.com/login/oauth",
				RedirectURI:         "http://localhost:8080/oauth/callback",
			},
			req: AuthURLRequest{
				CallbackURL: "http://localhost:3000/callback",
			},
			expectError: false,
			validateResponse: func(t *testing.T, resp *AuthURLResponse) {
				parsedURL, err := url.Parse(resp.URL)
				require.NoError(t, err)

				params := parsedURL.Query()
				assert.Equal(t, "http://localhost:3000/callback", params.Get("redirect_uri"))
			},
		},
		{
			name: "no scopes configured",
			cfg: ElicitationConfig{
				ClientID:            "test-client-id",
				AuthorizationServer: "https://github.com/login/oauth",
			},
			req: AuthURLRequest{
				CallbackURL: "http://localhost:3000/callback",
			},
			expectError: false,
			validateResponse: func(t *testing.T, resp *AuthURLResponse) {
				parsedURL, err := url.Parse(resp.URL)
				require.NoError(t, err)

				params := parsedURL.Query()
				assert.Empty(t, params.Get("scope"))
			},
		},
		{
			name: "authorization server without /authorize path",
			cfg: ElicitationConfig{
				ClientID:            "test-client-id",
				AuthorizationServer: "https://github.com/login/oauth/",
			},
			req: AuthURLRequest{
				CallbackURL: "http://localhost:3000/callback",
			},
			expectError: false,
			validateResponse: func(t *testing.T, resp *AuthURLResponse) {
				parsedURL, err := url.Parse(resp.URL)
				require.NoError(t, err)
				assert.Equal(t, "/login/oauth/authorize", parsedURL.Path)
			},
		},
		{
			name: "missing client ID",
			cfg: ElicitationConfig{
				AuthorizationServer: "https://github.com/login/oauth",
			},
			req: AuthURLRequest{
				CallbackURL: "http://localhost:3000/callback",
			},
			expectError: true,
			errorMsg:    "OAuth client ID is required",
		},
		{
			name: "missing authorization server",
			cfg: ElicitationConfig{
				ClientID: "test-client-id",
			},
			req: AuthURLRequest{
				CallbackURL: "http://localhost:3000/callback",
			},
			expectError: true,
			errorMsg:    "authorization server URL is required",
		},
		{
			name: "missing callback and redirect URI",
			cfg: ElicitationConfig{
				ClientID:            "test-client-id",
				AuthorizationServer: "https://github.com/login/oauth",
			},
			req:         AuthURLRequest{},
			expectError: true,
			errorMsg:    "callback URL or redirect URI is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp, err := tt.cfg.BuildAuthorizationURL(tt.req)

			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, resp)
			} else {
				require.NoError(t, err)
				require.NotNil(t, resp)
				if tt.validateResponse != nil {
					tt.validateResponse(t, resp)
				}
			}
		})
	}
}

func TestBuildAuthorizationURL_PKCEUniqueness(t *testing.T) {
	t.Parallel()

	cfg := ElicitationConfig{
		ClientID:            "test-client-id",
		AuthorizationServer: "https://github.com/login/oauth",
	}

	req := AuthURLRequest{
		CallbackURL: "http://localhost:3000/callback",
	}

	// Generate two URLs
	resp1, err := cfg.BuildAuthorizationURL(req)
	require.NoError(t, err)

	resp2, err := cfg.BuildAuthorizationURL(req)
	require.NoError(t, err)

	// Parse URLs
	url1, err := url.Parse(resp1.URL)
	require.NoError(t, err)

	url2, err := url.Parse(resp2.URL)
	require.NoError(t, err)

	// Verify state parameters are different
	state1 := url1.Query().Get("state")
	state2 := url2.Query().Get("state")
	assert.NotEqual(t, state1, state2, "state parameters should be unique")

	// Verify code challenges are different
	challenge1 := url1.Query().Get("code_challenge")
	challenge2 := url2.Query().Get("code_challenge")
	assert.NotEqual(t, challenge1, challenge2, "code challenges should be unique")
}

func TestBuildAuthorizationURL_EnterpriseServer(t *testing.T) {
	t.Parallel()

	cfg := ElicitationConfig{
		ClientID:            "enterprise-client-id",
		AuthorizationServer: "https://github.enterprise.com/login/oauth",
		Scopes:              []string{"repo"},
	}

	req := AuthURLRequest{
		CallbackURL: "https://app.example.com/callback",
	}

	resp, err := cfg.BuildAuthorizationURL(req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	parsedURL, err := url.Parse(resp.URL)
	require.NoError(t, err)

	// Verify enterprise host
	assert.Equal(t, "github.enterprise.com", parsedURL.Host)
	assert.True(t, strings.HasPrefix(resp.URL, "https://github.enterprise.com/login/oauth/authorize"))
}

// Made with Bob
