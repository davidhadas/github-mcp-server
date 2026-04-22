package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/viper"
)

// Build-time variables
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

// Logger for AI Agent
var agentLogger *slog.Logger

func init() {
	// Log to stdout for Kubernetes (like backend does)
	agentLogger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// TaskRequest represents a task from the browser
type TaskRequest struct {
	UserID       string                 `json:"user_id"`
	Task         string                 `json:"task"`
	Params       map[string]interface{} `json:"params,omitempty"`
	OAuthCode    string                 `json:"oauth_code,omitempty"`     // OAuth authorization code from callback
	CodeVerifier string                 `json:"code_verifier,omitempty"`  // PKCE code verifier
	MCPServerURL string                 `json:"mcp_server_url,omitempty"` // MCP server URL for OAuth
}

// TaskResponse represents the response to a task
type TaskResponse struct {
	Status  string      `json:"status"`
	Message string      `json:"message,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// MCPRequest represents an MCP request to AuthBridge proxy
type MCPRequest struct {
	UserID       string                 `json:"user_id"`
	MCPServerURL string                 `json:"mcp_server_url"`
	Method       string                 `json:"method"`
	Params       map[string]interface{} `json:"params"`
}

// MCPResponse represents the response from MCP server via AuthBridge
type MCPResponse struct {
	Status int
	Body   []byte
}

// AIAgent handles task orchestration
type AIAgent struct {
	mcpServerURL string // URL of MCP server - sidecar will intercept traffic transparently
}

// NewAIAgent creates a new AI Agent instance
func NewAIAgent(mcpServerURL string) *AIAgent {
	return &AIAgent{
		mcpServerURL: mcpServerURL,
	}
}

// HandleTask processes a task request
func (agent *AIAgent) HandleTask(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	// Extract headers set by Backend/sidecar
	userIDFromHeader := r.Header.Get("X-User-ID")
	resumeKey := r.Header.Get("X-Authbridge-Resume")
	oauthSessionKey := r.Header.Get("X-OAuth-Session-Key")

	var taskReq TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&taskReq); err != nil {
		agentLogger.Error("Invalid task request", "error", err.Error())
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Use header user ID if task doesn't have one
	if taskReq.UserID == "" && userIDFromHeader != "" {
		taskReq.UserID = userIDFromHeader
	}

	agentLogger.Info("Received task",
		"user_id", taskReq.UserID,
		"user_id_from_header", userIDFromHeader,
		"resume_key", resumeKey,
		"oauth_session_key", oauthSessionKey,
		"task", taskReq.Task,
		"has_oauth_code", taskReq.OAuthCode != "")

	var (
		taskResp *TaskResponse
		err      error
	)

	if agent.isRepositoryTask(taskReq) {
		taskResp, err = agent.handleListRepositoriesTask(taskReq, resumeKey, oauthSessionKey, startTime)
	} else {
		// Convert task to MCP request
		mcpReq := agent.taskToMCPRequest(taskReq)
		agentLogger.Info("Task converted to MCP request",
			"method", mcpReq.Method,
			"params", mcpReq.Params)

		// Make MCP request directly to MCP server (sidecar will intercept)
		agentLogger.Info("Sending MCP request to MCP server (sidecar will intercept)",
			"url", agent.mcpServerURL,
			"user_id", mcpReq.UserID,
			"resume_key", resumeKey,
			"method", mcpReq.Method)

		var mcpResp *MCPResponse
		mcpResp, err = agent.makeMCPRequestWithOAuth(mcpReq, resumeKey, oauthSessionKey, taskReq.OAuthCode, taskReq.CodeVerifier, taskReq.MCPServerURL)
		if err == nil {
			agentLogger.Info("Received response from AuthBridge proxy",
				"status", mcpResp.Status,
				"user_id", taskReq.UserID)

			// Convert MCP response to task result
			taskResp = agent.mcpResponseToTaskResult(mcpResp, taskReq, mcpReq, startTime)
		}
	}

	if err != nil {
		agentLogger.Error("MCP request failed", "error", err.Error())
		http.Error(w, fmt.Sprintf("MCP request failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(taskResp)
}

// taskToMCPRequest converts a task description to an MCP request
func (agent *AIAgent) taskToMCPRequest(task TaskRequest) MCPRequest {
	taskLower := strings.ToLower(task.Task)
	method := "tools/call"
	var toolName string
	var toolArguments map[string]interface{}

	// Check if params already has the tool name
	if task.Params != nil && task.Params["name"] != nil {
		if name, ok := task.Params["name"].(string); ok {
			toolName = name
		}
		if args, ok := task.Params["arguments"].(map[string]interface{}); ok {
			toolArguments = args
		}
	}

	// Detect task intent and map to appropriate MCP tool if not already set
	if toolName == "" {
		// Check for repository-related tasks first (more specific)
		if strings.Contains(taskLower, "repo") || strings.Contains(taskLower, "repositories") {
			toolName = "search_repositories"
			if toolArguments == nil {
				toolArguments = map[string]interface{}{}
			}
			if _, exists := toolArguments["query"]; !exists {
				toolArguments["query"] = "mcp"
			}
			agentLogger.Info("Repository task detected - using search_repositories", "query", toolArguments["query"])
		} else if strings.Contains(taskLower, "list") && (strings.Contains(taskLower, "tool") || strings.Contains(taskLower, "available")) {
			// List tools
			method = "tools/list"
		} else if strings.Contains(taskLower, "profile") || strings.Contains(taskLower, "who am i") || strings.Contains(taskLower, "me") {
			// Get user profile - use correct tool name from GitHub MCP server
			toolName = "get_me"
		} else {
			// Default to get_me
			toolName = "get_me"
		}
	}

	// Build params based on method
	var params map[string]interface{}
	if method == "tools/list" {
		params = map[string]interface{}{} // tools/list has empty params
	} else {
		// tools/call requires name and arguments
		if toolArguments == nil {
			toolArguments = map[string]interface{}{}
		}
		params = map[string]interface{}{
			"name":      toolName,
			"arguments": toolArguments,
		}
	}

	agentLogger.Info("Task intent detected",
		"task", task.Task,
		"method", method,
		"tool", toolName)

	return MCPRequest{
		UserID:       task.UserID,
		MCPServerURL: agent.mcpServerURL,
		Method:       method,
		Params:       params,
	}
}

func (agent *AIAgent) isRepositoryTask(task TaskRequest) bool {
	taskLower := strings.ToLower(task.Task)
	return strings.Contains(taskLower, "repo") || strings.Contains(taskLower, "repositories")
}

func (agent *AIAgent) handleListRepositoriesTask(taskReq TaskRequest, resumeKey, oauthSessionKey string, startTime time.Time) (*TaskResponse, error) {
	getMeReq := MCPRequest{
		UserID:       taskReq.UserID,
		MCPServerURL: agent.mcpServerURL,
		Method:       "tools/call",
		Params: map[string]interface{}{
			"name":      "get_me",
			"arguments": map[string]interface{}{},
		},
	}

	agentLogger.Info("Resolving authenticated GitHub user before listing repositories",
		"user_id", taskReq.UserID,
		"resume_key", resumeKey)

	getMeResp, err := agent.makeMCPRequestWithOAuth(getMeReq, resumeKey, oauthSessionKey, taskReq.OAuthCode, taskReq.CodeVerifier, taskReq.MCPServerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve authenticated user: %w", err)
	}
	if getMeResp.Status != http.StatusOK {
		return agent.mcpResponseToTaskResult(getMeResp, taskReq, getMeReq, startTime), nil
	}

	rawProfile, err := parseSSEResponse(getMeResp.Body)
	if err != nil {
		if jsonErr := json.Unmarshal(getMeResp.Body, &rawProfile); jsonErr != nil {
			return nil, fmt.Errorf("failed to parse get_me response: %w", err)
		}
	}

	actualProfile := extractMCPContent(rawProfile)
	profileMap, ok := actualProfile.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("get_me response did not contain a user object")
	}

	loginValue, ok := profileMap["login"].(string)
	if !ok || loginValue == "" {
		return nil, fmt.Errorf("get_me response did not include login")
	}

	repoReq := MCPRequest{
		UserID:       taskReq.UserID,
		MCPServerURL: agent.mcpServerURL,
		Method:       "tools/call",
		Params: map[string]interface{}{
			"name": "search_repositories",
			"arguments": map[string]interface{}{
				"query": fmt.Sprintf("user:%s", loginValue),
			},
		},
	}

	agentLogger.Info("Listing repositories scoped to authenticated user",
		"user_id", taskReq.UserID,
		"github_login", loginValue,
		"query", fmt.Sprintf("user:%s", loginValue))

	repoResp, err := agent.makeMCPRequestWithOAuth(repoReq, resumeKey, oauthSessionKey, taskReq.OAuthCode, taskReq.CodeVerifier, taskReq.MCPServerURL)
	if err != nil {
		return nil, err
	}

	agentLogger.Info("Received repository search response from AuthBridge proxy",
		"status", repoResp.Status,
		"user_id", taskReq.UserID,
		"github_login", loginValue)

	// Parse the response to check for 422 validation errors
	if repoResp.Status == http.StatusOK {
		// Try to parse the response to detect MCP errors
		rawResult, parseErr := parseSSEResponse(repoResp.Body)
		if parseErr != nil {
			// Fallback to plain JSON parsing
			if jsonErr := json.Unmarshal(repoResp.Body, &rawResult); jsonErr != nil {
				// If we can't parse, continue with normal error handling
				agentLogger.Warn("Failed to parse response for error detection",
					"parse_error", parseErr.Error(),
					"json_error", jsonErr.Error())
			}
		}

		// Check if this is an MCP error response with 422 validation failure
		if rawResult != nil {
			if resultMap, ok := rawResult.(map[string]interface{}); ok {
				if content, hasContent := resultMap["content"].([]interface{}); hasContent && len(content) > 0 {
					if firstContent, ok := content[0].(map[string]interface{}); ok {
						if isError, hasError := resultMap["isError"].(bool); hasError && isError {
							if text, hasText := firstContent["text"].(string); hasText {
								// Check if this is a 422 validation error for user search
								if strings.Contains(text, "422") && strings.Contains(text, "Validation Failed") &&
									(strings.Contains(text, "cannot be searched") || strings.Contains(text, "do not exist")) {
									agentLogger.Info("User has no repositories (422 validation error in MCP response)",
										"user_id", taskReq.UserID,
										"github_login", loginValue)

									executionTime := time.Since(startTime).Milliseconds()
									return &TaskResponse{
										Status:  "success",
										Message: "No repositories found for the authenticated user",
										Result: map[string]interface{}{
											"session_id":        taskReq.UserID,
											"task_description":  taskReq.Task,
											"executed_by_user":  taskReq.UserID,
											"mcp_method":        repoReq.Method,
											"mcp_tool":          "search_repositories",
											"execution_time_ms": executionTime,
											"github_user":       loginValue,
											"repository_query":  fmt.Sprintf("user:%s", loginValue),
											"data": map[string]interface{}{
												"total_count":  0,
												"repositories": []interface{}{},
												"count":        0,
												"message":      "No repositories found for the authenticated user",
											},
										},
									}, nil
								}
							}
						}
					}
				}
			}
		}
	}

	taskResp := agent.mcpResponseToTaskResult(repoResp, taskReq, repoReq, startTime)
	if taskResp.Status == "success" {
		if resultMap, ok := taskResp.Result.(map[string]interface{}); ok {
			resultMap["github_user"] = loginValue
			resultMap["repository_query"] = fmt.Sprintf("user:%s", loginValue)
		}
	}

	return taskResp, nil
}

// makeMCPRequestWithOAuth sends an MCP JSON-RPC request directly to MCP server
// The sidecar will transparently intercept this request via iptables
// If OAuth parameters are provided, they are passed as headers for the sidecar to detect
func (agent *AIAgent) makeMCPRequestWithOAuth(mcpReq MCPRequest, resumeKey, oauthSessionKey, oauthCode, codeVerifier, mcpServerURL string) (*MCPResponse, error) {
	// Send raw MCP JSON-RPC request directly to MCP server
	mcpJSONRPC := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  mcpReq.Method,
		"params":  mcpReq.Params,
	}

	reqBody, err := json.Marshal(mcpJSONRPC)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal MCP JSON-RPC request: %w", err)
	}

	agentLogger.Info("Sending MCP JSON-RPC request",
		"url", mcpReq.MCPServerURL,
		"method", mcpReq.Method,
		"user_id", mcpReq.UserID,
		"has_oauth_code", oauthCode != "")

	// Create HTTP request with user context header for sidecar
	httpReq, err := http.NewRequest("POST", mcpReq.MCPServerURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream") // MCP server requires both
	httpReq.Header.Set("X-User-ID", mcpReq.UserID)

	// Propagate OAuth session key for Envoy ext_authz → Token Broker
	if oauthSessionKey != "" {
		httpReq.Header.Set("X-OAuth-Session-Key", oauthSessionKey)
	}

	// Pass resume key for correlation (REQUIRED for OAuth flow)
	if resumeKey != "" {
		httpReq.Header.Set("X-Authbridge-Resume", resumeKey)
		agentLogger.Info("Added resume key header for sidecar", "resume_key", resumeKey)
	}

	// Pass OAuth parameters as headers for sidecar to detect
	if oauthCode != "" {
		httpReq.Header.Set("X-OAuth-Code", oauthCode)
		agentLogger.Info("Added OAuth code header for sidecar")
	}
	if codeVerifier != "" {
		httpReq.Header.Set("X-Code-Verifier", codeVerifier)
		agentLogger.Info("Added code verifier header for sidecar")
	}
	if mcpServerURL != "" {
		httpReq.Header.Set("X-MCP-Server-URL", mcpServerURL)
		agentLogger.Info("Added MCP server URL header for sidecar", "url", mcpServerURL)
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send MCP request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read MCP response: %w", err)
	}

	return &MCPResponse{
		Status: resp.StatusCode,
		Body:   body,
	}, nil
}

// parseSSEResponse parses Server-Sent Events format and extracts JSON-RPC response
func parseSSEResponse(body []byte) (interface{}, error) {
	bodyStr := string(body)

	// Check if this looks like SSE format (contains "data: " prefix or "event: " prefix)
	if !strings.Contains(bodyStr, "data: ") && !strings.Contains(bodyStr, "event: ") {
		return nil, fmt.Errorf("not SSE format")
	}

	lines := strings.Split(bodyStr, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip event: lines
		if strings.HasPrefix(line, "event: ") {
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			jsonData := strings.TrimPrefix(line, "data: ")

			// Skip empty data lines
			if jsonData == "" {
				continue
			}

			var result interface{}
			if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
				return nil, fmt.Errorf("failed to parse SSE data as JSON: %w", err)
			}

			// Check if it's a JSON-RPC response
			if resultMap, ok := result.(map[string]interface{}); ok {
				// If there's an error field in the JSON-RPC response, return it as an error
				if errorData, hasError := resultMap["error"]; hasError {
					if errorMap, ok := errorData.(map[string]interface{}); ok {
						errorMsg := "Unknown MCP error"
						if msg, ok := errorMap["message"].(string); ok {
							errorMsg = msg
						}
						errorCode := 0
						if code, ok := errorMap["code"].(float64); ok {
							errorCode = int(code)
						}
						return nil, fmt.Errorf("MCP server error (code %d): %s", errorCode, errorMsg)
					}
					return nil, fmt.Errorf("MCP error: %v", errorData)
				}
				// If there's a result field, return it
				if resultData, hasResult := resultMap["result"]; hasResult {
					return resultData, nil
				}
			}

			return result, nil
		}
	}

	return nil, fmt.Errorf("no data field found in SSE response")
}

// mcpResponseToTaskResult converts MCP response to user-friendly task result
func (agent *AIAgent) mcpResponseToTaskResult(mcpResp *MCPResponse, task TaskRequest, mcpReq MCPRequest, startTime time.Time) *TaskResponse {
	executionTime := time.Since(startTime).Milliseconds()

	if mcpResp.Status != http.StatusOK {
		agentLogger.Warn("Request failed", "status", mcpResp.Status)
		return &TaskResponse{
			Status:  "error",
			Message: fmt.Sprintf("Request failed with status %d", mcpResp.Status),
			Error:   string(mcpResp.Body),
		}
	}

	// Try to parse as SSE first (MCP protocol uses SSE)
	rawResult, err := parseSSEResponse(mcpResp.Body)
	if err != nil {
		agentLogger.Warn("Failed to parse SSE response, trying plain JSON",
			"error", err.Error(),
			"body", string(mcpResp.Body))
		// Fallback to plain JSON parsing
		if jsonErr := json.Unmarshal(mcpResp.Body, &rawResult); jsonErr != nil {
			agentLogger.Error("Failed to parse response",
				"sse_error", err.Error(),
				"json_error", jsonErr.Error(),
				"body", string(mcpResp.Body))
			return &TaskResponse{
				Status:  "error",
				Message: "Failed to parse MCP response",
				Error:   fmt.Sprintf("SSE parse error: %v, JSON parse error: %v", err, jsonErr),
			}
		}
	}

	agentLogger.Info("Parsed MCP response",
		"raw_result_type", fmt.Sprintf("%T", rawResult),
		"raw_result", rawResult)

	// Format response based on task type
	formattedResult := agent.formatResponseByTaskType(rawResult, task.Task, mcpReq)

	// Build enhanced result with metadata
	result := map[string]interface{}{
		"session_id":        task.UserID,
		"task_description":  task.Task,
		"executed_by_user":  task.UserID,
		"mcp_method":        mcpReq.Method,
		"execution_time_ms": executionTime,
		"data":              formattedResult,
	}

	// Add tool name if it's a tools/call
	if mcpReq.Method == "tools/call" {
		if toolName, ok := mcpReq.Params["name"].(string); ok {
			result["mcp_tool"] = toolName
		}
	}

	// Extract GitHub login for get_me tool
	if mcpReq.Method == "tools/call" {
		if toolName, ok := mcpReq.Params["name"].(string); ok && toolName == "get_me" {
			if resultMap, ok := formattedResult.(map[string]interface{}); ok {
				if login, exists := resultMap["login"]; exists {
					result["github_user"] = login
				}
			}
		}
	}

	agentLogger.Info("Task completed successfully",
		"user_id", task.UserID,
		"execution_time_ms", executionTime)

	return &TaskResponse{
		Status:  "success",
		Message: "Task completed successfully",
		Result:  result,
	}
}

// formatResponseByTaskType formats the response based on task intent
func (agent *AIAgent) formatResponseByTaskType(rawResult interface{}, task string, mcpReq MCPRequest) interface{} {
	taskLower := strings.ToLower(task)

	// Check if this is a tools/list response
	if mcpReq.Method == "tools/list" {
		return agent.formatToolsList(rawResult)
	}

	// Check tool name for tools/call
	if mcpReq.Method == "tools/call" {
		if toolName, ok := mcpReq.Params["name"].(string); ok {
			switch toolName {
			case "get_me":
				return agent.formatUserProfile(rawResult)
			case "search_repositories":
				return agent.formatRepositoriesList(rawResult)
			}
		}
	}

	// Check task keywords as fallback
	if strings.Contains(taskLower, "repo") || strings.Contains(taskLower, "repositories") {
		return agent.formatRepositoriesList(rawResult)
	} else if strings.Contains(taskLower, "profile") || strings.Contains(taskLower, "me") {
		return agent.formatUserProfile(rawResult)
	} else if strings.Contains(taskLower, "tool") {
		return agent.formatToolsList(rawResult)
	}

	// Return raw result if no specific formatting applies
	return rawResult
}

// extractMCPContent extracts the actual data from MCP content structure
// MCP responses wrap data in: {content: [{type: "text", text: "...JSON..."}]}
func extractMCPContent(rawResult interface{}) interface{} {
	resultMap, ok := rawResult.(map[string]interface{})
	if !ok {
		return rawResult
	}

	// Check if this has the MCP content structure
	contentArray, hasContent := resultMap["content"]
	if !hasContent {
		return rawResult
	}

	// Extract content array
	contents, ok := contentArray.([]interface{})
	if !ok || len(contents) == 0 {
		return rawResult
	}

	// Get first content item
	firstContent, ok := contents[0].(map[string]interface{})
	if !ok {
		return rawResult
	}

	// Extract text field
	textData, hasText := firstContent["text"]
	if !hasText {
		return rawResult
	}

	// If text is a string, try to parse it as JSON
	if textStr, ok := textData.(string); ok {
		var parsed interface{}
		if err := json.Unmarshal([]byte(textStr), &parsed); err == nil {
			return parsed
		}
		// If not JSON, return the string
		return textStr
	}

	// If text is already an object, return it
	return textData
}

// formatToolsList formats a tools/list response
func (agent *AIAgent) formatToolsList(rawResult interface{}) interface{} {
	// For tools/list, just return the raw result (it's already well-formatted)
	return rawResult
}

// formatUserProfile formats a user profile response
func (agent *AIAgent) formatUserProfile(rawResult interface{}) interface{} {
	// Extract data from MCP content structure if present
	actualData := extractMCPContent(rawResult)

	// Extract relevant user profile fields
	if resultMap, ok := actualData.(map[string]interface{}); ok {
		formatted := make(map[string]interface{})
		relevantFields := []string{
			"login", "name", "email", "bio", "company", "location", "blog",
			"twitter", "avatar_url", "html_url", "profile_url",
			"public_repos", "public_gists", "followers", "following",
			"created_at", "updated_at", "id", "details",
		}

		for _, field := range relevantFields {
			if value, exists := resultMap[field]; exists {
				formatted[field] = value
			}
		}

		return formatted
	}

	return rawResult
}

// formatRepositoriesList formats a repositories list response
func (agent *AIAgent) formatRepositoriesList(rawResult interface{}) interface{} {
	// Extract data from MCP content structure if present
	actualData := extractMCPContent(rawResult)

	// Check if this is a search_repositories result with items array
	if resultMap, ok := actualData.(map[string]interface{}); ok {
		if items, hasItems := resultMap["items"].([]interface{}); hasItems {
			// Format each repository with key fields only
			formattedRepos := make([]map[string]interface{}, 0, len(items))
			for _, item := range items {
				if repo, ok := item.(map[string]interface{}); ok {
					formatted := map[string]interface{}{
						"name":        repo["name"],
						"full_name":   repo["full_name"],
						"description": repo["description"],
						"html_url":    repo["html_url"],
						"language":    repo["language"],
						"stars":       repo["stargazers_count"],
						"forks":       repo["forks_count"],
						"private":     repo["private"],
					}
					formattedRepos = append(formattedRepos, formatted)
				}
			}

			result := map[string]interface{}{
				"total_count":  resultMap["total_count"],
				"repositories": formattedRepos,
				"count":        len(formattedRepos),
			}

			// Add message for empty list
			if len(formattedRepos) == 0 {
				result["message"] = "No repositories found"
			}

			return result
		}
	}

	// Check if result is an array of repositories (list_repositories format)
	if resultArray, ok := actualData.([]interface{}); ok {
		// Format each repository with key fields only
		formattedRepos := make([]map[string]interface{}, 0, len(resultArray))
		for _, item := range resultArray {
			if repo, ok := item.(map[string]interface{}); ok {
				formatted := map[string]interface{}{
					"name":        repo["name"],
					"full_name":   repo["full_name"],
					"description": repo["description"],
					"html_url":    repo["html_url"],
					"language":    repo["language"],
					"stars":       repo["stargazers_count"],
					"forks":       repo["forks_count"],
					"private":     repo["private"],
				}
				formattedRepos = append(formattedRepos, formatted)
			}
		}

		result := map[string]interface{}{
			"repositories": formattedRepos,
			"count":        len(formattedRepos),
		}

		// Add message for empty list
		if len(formattedRepos) == 0 {
			result["message"] = "No repositories found for the authenticated user"
		}

		return result
	}

	return rawResult
}

func main() {
	// Log build info
	agentLogger.Info("AI Agent starting",
		"version", version,
		"commit", commit,
		"build_time", date)

	// Load configuration
	viper.SetConfigName("aiagent-config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/config") // Kubernetes ConfigMap mount
	viper.AddConfigPath(".")
	viper.AddConfigPath("./cmd/aiagent")

	if err := viper.ReadInConfig(); err != nil {
		agentLogger.Error("Error reading config file", "error", err)
		os.Exit(1)
	}

	port := viper.GetInt("port")
	if port == 0 {
		port = 8186
	}

	mcpServerURL := viper.GetString("mcp_server_url")
	if mcpServerURL == "" {
		agentLogger.Error("mcp_server_url must be configured in aiagent-config.yaml")
		os.Exit(1)
	}

	agent := NewAIAgent(mcpServerURL)

	// Setup HTTP server with access log to stdout (like backend does)
	r := chi.NewRouter()
	r.Use(middleware.RequestLogger(&middleware.DefaultLogFormatter{Logger: log.New(os.Stdout, "", log.LstdFlags)}))
	r.Use(middleware.Recoverer)

	// Task endpoint
	r.Post("/task", agent.HandleTask)

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	agentLogger.Info("AI Agent starting (sidecar mode)",
		"port", port,
		"mcp_server_url", mcpServerURL)

	agentLogger.Info("AI Agent listening (sidecar mode)",
		"port", port,
		"mcp_server_url", mcpServerURL)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), r); err != nil {
		agentLogger.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}

// Made with Bob
