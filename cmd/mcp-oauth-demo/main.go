package main

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	oauthMetadataPath = "/.well-known/oauth-protected-resource"
	userAgent         = "Go-MCP-OAuth-Demo/1.0"
)

// OAuthMetadata represents the OAuth protected resource metadata
type OAuthMetadata struct {
	AuthorizationServers   []string `json:"authorization_servers"`
	ResourceName           string   `json:"resource_name,omitempty"`
	Resource               string   `json:"resource,omitempty"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	BearerMethodsSupported []string `json:"bearer_methods_supported,omitempty"`
}

// MCPOAuthDiscovery is a client for discovering OAuth metadata from MCP servers
type MCPOAuthDiscovery struct {
	baseURL   string
	timeout   time.Duration
	verifySSL bool
	client    *http.Client
	debug     bool
}

// NewMCPOAuthDiscovery creates a new OAuth discovery client
func NewMCPOAuthDiscovery(baseURL string, timeout time.Duration, verifySSL bool, debug bool) *MCPOAuthDiscovery {
	client := &http.Client{
		Timeout: timeout,
	}

	if !verifySSL {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	return &MCPOAuthDiscovery{
		baseURL:   strings.TrimRight(baseURL, "/"),
		timeout:   timeout,
		verifySSL: verifySSL,
		client:    client,
		debug:     debug,
	}
}

// GetMetadataURL constructs the full OAuth metadata URL
func (m *MCPOAuthDiscovery) GetMetadataURL() string {
	metadataURL, _ := url.JoinPath(m.baseURL, oauthMetadataPath)
	return metadataURL
}

// FetchOAuthMetadata fetches OAuth protected resource metadata from the MCP server
// This method handles two scenarios:
// 1. Direct access to .well-known/oauth-protected-resource (local servers)
// 2. Following WWW-Authenticate header from 401 response (remote servers)
func (m *MCPOAuthDiscovery) FetchOAuthMetadata() (*OAuthMetadata, error) {
	metadataURL := m.GetMetadataURL()
	log.Printf("Fetching OAuth metadata from: %s", metadataURL)

	req, err := http.NewRequest("GET", metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	// If we get 401, check for WWW-Authenticate header with resource_metadata
	if resp.StatusCode == http.StatusUnauthorized {
		wwwAuth := resp.Header.Get("WWW-Authenticate")
		if m.debug {
			log.Printf("Received 401 with WWW-Authenticate: %s", wwwAuth)
		}

		// Extract resource_metadata URL from WWW-Authenticate header
		// Format: Bearer resource_metadata="https://..."
		if strings.Contains(wwwAuth, "resource_metadata=") {
			re := regexp.MustCompile(`resource_metadata="([^"]+)"`)
			matches := re.FindStringSubmatch(wwwAuth)
			if len(matches) > 1 {
				metadataURL = matches[1]
				log.Printf("Following resource_metadata URL: %s", metadataURL)

				// Fetch from the metadata URL
				req, err = http.NewRequest("GET", metadataURL, nil)
				if err != nil {
					return nil, fmt.Errorf("failed to create request: %w", err)
				}
				req.Header.Set("User-Agent", userAgent)

				resp, err = m.client.Do(req)
				if err != nil {
					return nil, fmt.Errorf("connection failed: %w", err)
				}
				defer resp.Body.Close()
			}
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP error: %d %s", resp.StatusCode, resp.Status)
	}

	var metadata OAuthMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	if m.debug {
		metadataJSON, _ := json.MarshalIndent(metadata, "", "  ")
		log.Printf("Received metadata: %s", string(metadataJSON))
	}

	return &metadata, nil
}

// GetAuthorizationServerURL extracts the authorization server URL from OAuth metadata
func (m *MCPOAuthDiscovery) GetAuthorizationServerURL(metadata *OAuthMetadata) (string, error) {
	if len(metadata.AuthorizationServers) == 0 {
		return "", fmt.Errorf("no authorization servers found in metadata")
	}

	if len(metadata.AuthorizationServers) > 1 {
		log.Printf("Multiple authorization servers found: %v", metadata.AuthorizationServers)
		log.Printf("Using first server: %s", metadata.AuthorizationServers[0])
	}

	return metadata.AuthorizationServers[0], nil
}

func printSuccess(message string) {
	fmt.Printf("✓ %s\n", message)
}

func printError(message string) {
	fmt.Fprintf(os.Stderr, "✗ %s\n", message)
}

func printMetadataDetails(metadata *OAuthMetadata) {
	fmt.Println("\nAdditional Information:")

	if metadata.ResourceName != "" {
		fmt.Printf("- Resource Name: %s\n", metadata.ResourceName)
	}

	if metadata.Resource != "" {
		fmt.Printf("- Resource URL: %s\n", metadata.Resource)
	}

	if len(metadata.ScopesSupported) > 0 {
		scopes := metadata.ScopesSupported
		if len(scopes) <= 5 {
			fmt.Printf("- Supported Scopes: %s\n", strings.Join(scopes, ", "))
		} else {
			fmt.Printf("- Supported Scopes: %s, ... (%d total)\n", strings.Join(scopes[:5], ", "), len(scopes))
		}
	}

	if len(metadata.BearerMethodsSupported) > 0 {
		fmt.Printf("- Bearer Methods: %s\n", strings.Join(metadata.BearerMethodsSupported, ", "))
	}
}

func main() {
	// Define flags
	urlFlag := flag.String("url", "", "Base URL of the MCP server (required)")
	timeoutFlag := flag.Int("timeout", 10, "Request timeout in seconds")
	noVerifySSL := flag.Bool("no-verify-ssl", false, "Disable SSL certificate verification (not recommended)")
	debugFlag := flag.Bool("debug", false, "Enable debug logging")
	formatFlag := flag.String("format", "text", "Output format: text or json")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: mcp-oauth-demo [options]\n\n")
		fmt.Fprintf(os.Stderr, "Discover OAuth authorization server URL from MCP server\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  # Remote GitHub MCP Server\n")
		fmt.Fprintf(os.Stderr, "  mcp-oauth-demo -url https://api.githubcopilot.com/mcp/\n\n")
		fmt.Fprintf(os.Stderr, "  # Local MCP Server\n")
		fmt.Fprintf(os.Stderr, "  mcp-oauth-demo -url http://localhost:8082\n\n")
		fmt.Fprintf(os.Stderr, "  # With debug logging\n")
		fmt.Fprintf(os.Stderr, "  mcp-oauth-demo -url https://api.githubcopilot.com/mcp/ -debug\n\n")
		fmt.Fprintf(os.Stderr, "  # JSON output format\n")
		fmt.Fprintf(os.Stderr, "  mcp-oauth-demo -url https://api.githubcopilot.com/mcp/ -format json\n\n")
		fmt.Fprintf(os.Stderr, "  # Specific toolset\n")
		fmt.Fprintf(os.Stderr, "  mcp-oauth-demo -url https://api.githubcopilot.com/mcp/x/repos\n")
	}

	flag.Parse()

	// Validate required flags
	if *urlFlag == "" {
		flag.Usage()
		os.Exit(1)
	}

	// Validate format flag
	if *formatFlag != "text" && *formatFlag != "json" {
		fmt.Fprintf(os.Stderr, "Error: format must be 'text' or 'json'\n")
		os.Exit(1)
	}

	// Configure logging
	if !*debugFlag {
		log.SetOutput(os.Stderr)
		log.SetFlags(0)
	}

	// Warn about SSL verification
	if *noVerifySSL {
		fmt.Fprintf(os.Stderr, "⚠️  Warning: SSL certificate verification is disabled\n")
	}

	// Create client and fetch metadata
	if *formatFlag == "text" {
		fmt.Printf("Connecting to MCP Server: %s\n", *urlFlag)
		fmt.Println("Fetching OAuth metadata...")
	}

	client := NewMCPOAuthDiscovery(
		*urlFlag,
		time.Duration(*timeoutFlag)*time.Second,
		!*noVerifySSL,
		*debugFlag,
	)

	metadata, err := client.FetchOAuthMetadata()
	if err != nil {
		printError(fmt.Sprintf("Failed to retrieve OAuth metadata: %v", err))
		os.Exit(1)
	}

	// Extract authorization server URL
	authURL, err := client.GetAuthorizationServerURL(metadata)
	if err != nil {
		printError(fmt.Sprintf("Authorization server URL not found in metadata: %v", err))
		os.Exit(1)
	}

	// Output results
	if *formatFlag == "json" {
		output := map[string]interface{}{
			"success":                  true,
			"authorization_server_url": authURL,
			"metadata":                 metadata,
		}
		outputJSON, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(outputJSON))
	} else {
		printSuccess("Successfully retrieved OAuth metadata\n")
		fmt.Printf("Authorization Server URL: %s\n", authURL)
		printMetadataDetails(metadata)
	}
}

// Made with Bob
