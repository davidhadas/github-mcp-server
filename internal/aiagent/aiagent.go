package aiagent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Logger for AIAgent with separate log file
var agentLogger *slog.Logger

func init() {
	// Create separate log file for AIAgent
	logFile, err := os.OpenFile("/tmp/aiagent.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic(fmt.Sprintf("Failed to open aiagent log file: %v", err))
	}

	agentLogger = slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// AuthBridge interface defines what AIAgent needs from AuthBridge
type AuthBridge interface {
	HandleMCPRequest(req MCPRequest) (*MCPResponse, error)
}

// AIAgent represents a KAgentI AI Agent that orchestrates tasks
// This is a mock implementation that mimics how a real KAgentI agent would work
type AIAgent struct {
	authBridge AuthBridge
	userID     string
	mu         sync.RWMutex
}

// NewAIAgent creates a new AI Agent instance
func NewAIAgent(authBridge AuthBridge, userID string) *AIAgent {
	agentLogger.Info("Creating new AIAgent instance", "user_id", userID)
	return &AIAgent{
		authBridge: authBridge,
		userID:     userID,
	}
}

// TaskRequest represents a task from the browser
type TaskRequest struct {
	UserID       string                 `json:"user_id"`
	Task         string                 `json:"task"`
	MCPServerURL string                 `json:"mcp_server_url"`
	Params       map[string]interface{} `json:"params,omitempty"`
	Code         string                 `json:"code,omitempty"`          // OAuth code from redirect
	CodeVerifier string                 `json:"code_verifier,omitempty"` // PKCE code verifier
}

// TaskResponse represents the response to a task
type TaskResponse struct {
	Status  string      `json:"status"`
	Message string      `json:"message,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// MCPRequest represents an internal MCP request from AIAgent to AuthBridge
type MCPRequest struct {
	UserID       string                 `json:"user_id"`
	MCPServerURL string                 `json:"mcp_server_url"`
	Method       string                 `json:"method"`
	Params       map[string]interface{} `json:"params"`
}

// MCPResponse represents the response from MCP server via AuthBridge
type MCPResponse struct {
	Status       int
	Body         []byte
	NeedsAuth    bool
	AuthURL      string
	CodeVerifier string
}

// ExecuteTask processes a task from the browser
// Flow: Browser → AuthBridge → AIAgent (this method) → AuthBridge → MCP Server
func (agent *AIAgent) ExecuteTask(task TaskRequest) (*TaskResponse, error) {
	startTime := time.Now()

	agentLogger.Info("Received task from browser",
		"user_id", agent.userID,
		"task", task.Task,
		"mcp_server", task.MCPServerURL)

	// Convert task to MCP request
	// In a real implementation, this would use NLP/LLM to determine the right MCP method
	agentLogger.Info("Converting task to MCP request", "task", task.Task)
	mcpReq := agent.taskToMCPRequest(task)
	agentLogger.Info("Task converted to MCP request",
		"method", mcpReq.Method,
		"params", mcpReq.Params)

	// Make MCP request through AuthBridge
	agentLogger.Info("Sending MCP request to AuthBridge",
		"user_id", mcpReq.UserID,
		"mcp_server", mcpReq.MCPServerURL,
		"method", mcpReq.Method)

	mcpResp, err := agent.authBridge.HandleMCPRequest(mcpReq)
	if err != nil {
		// Propagate the error up - AuthBridge.ExecuteTask will handle AuthRequiredError
		agentLogger.Info("AuthBridge returned error, propagating up",
			"error", err.Error(),
			"user_id", agent.userID)
		return nil, err
	}

	agentLogger.Info("Received response from AuthBridge",
		"user_id", agent.userID,
		"status", mcpResp.Status,
		"body_size", len(mcpResp.Body))

	// Process response and convert to task result
	agentLogger.Info("Processing response into task result", "task", task.Task)
	taskResult := agent.mcpResponseToTaskResult(mcpResp, task, mcpReq, startTime)

	agentLogger.Info("Task completed",
		"user_id", agent.userID,
		"result_status", taskResult.Status,
		"duration_ms", time.Since(startTime).Milliseconds())

	return taskResult, nil
}

// taskToMCPRequest converts a task description to an MCP request
// This is where the AI agent's intelligence would go
func (agent *AIAgent) taskToMCPRequest(task TaskRequest) MCPRequest {
	// Enhanced mock implementation with task intent detection
	// Real implementation would use LLM to parse task and determine MCP method

	taskLower := strings.ToLower(task.Task)
	method := "tools/call"
	params := task.Params

	if params == nil {
		params = make(map[string]interface{})
	}

	// Detect task intent and map to appropriate MCP tool
	if params["name"] == nil {
		// List tools
		if strings.Contains(taskLower, "list") && (strings.Contains(taskLower, "tool") || strings.Contains(taskLower, "available")) {
			method = "tools/list"
			params = make(map[string]interface{}) // tools/list doesn't need params
		} else if strings.Contains(taskLower, "list") && strings.Contains(taskLower, "repo") {
			// List repositories
			params["name"] = "list_repos"
		} else if strings.Contains(taskLower, "profile") || strings.Contains(taskLower, "me") || strings.Contains(taskLower, "my") {
			// Get user profile
			params["name"] = "get_me"
		} else {
			// Default to get_me
			params["name"] = "get_me"
		}
	}

	agentLogger.Info("Task intent detected",
		"task", task.Task,
		"method", method,
		"tool", params["name"])

	return MCPRequest{
		UserID:       agent.userID,
		MCPServerURL: task.MCPServerURL,
		Method:       method,
		Params:       params,
	}
}

// mcpResponseToTaskResult converts MCP response to user-friendly task result
func (agent *AIAgent) mcpResponseToTaskResult(mcpResp *MCPResponse, task TaskRequest, mcpReq MCPRequest, startTime time.Time) *TaskResponse {
	executionTime := time.Since(startTime).Milliseconds()

	if mcpResp.Status != http.StatusOK {
		agentLogger.Warn("Request failed",
			"status", mcpResp.Status)
		return &TaskResponse{
			Status:  "error",
			Message: fmt.Sprintf("Request failed with status %d", mcpResp.Status),
			Error:   string(mcpResp.Body),
		}
	}

	// Parse raw response
	var rawResult interface{}
	if err := json.Unmarshal(mcpResp.Body, &rawResult); err != nil {
		agentLogger.Warn("Failed to parse response as JSON",
			"error", err.Error())
		rawResult = string(mcpResp.Body)
	}

	// Format response based on task type
	formattedResult := agent.formatResponseByTaskType(rawResult, task.Task, mcpReq)

	// Add execution metadata
	result := map[string]interface{}{
		"task_description":  task.Task,
		"executed_by_user":  agent.userID,
		"mcp_method":        mcpReq.Method,
		"execution_time_ms": executionTime,
		"data":              formattedResult,
	}

	// Add tool name if it was a tool call
	if toolName, ok := mcpReq.Params["name"].(string); ok {
		result["mcp_tool"] = toolName
	}

	return &TaskResponse{
		Status:  "success",
		Message: fmt.Sprintf("Task completed successfully"),
		Result:  result,
	}
}

// formatResponseByTaskType formats the response based on the task intent
func (agent *AIAgent) formatResponseByTaskType(rawResult interface{}, taskDesc string, mcpReq MCPRequest) interface{} {
	// Handle tools/list response
	if mcpReq.Method == "tools/list" {
		return agent.formatToolsList(rawResult)
	}

	// Handle tool call responses
	if mcpReq.Method == "tools/call" {
		toolName, _ := mcpReq.Params["name"].(string)

		switch toolName {
		case "get_me":
			return agent.formatUserProfile(rawResult)
		case "list_repos":
			return agent.formatRepositoriesList(rawResult)
		default:
			return rawResult
		}
	}

	return rawResult
}

// formatToolsList formats the tools/list response
func (agent *AIAgent) formatToolsList(rawResult interface{}) interface{} {
	resultMap, ok := rawResult.(map[string]interface{})
	if !ok {
		return rawResult
	}

	tools, ok := resultMap["tools"].([]interface{})
	if !ok {
		return rawResult
	}

	formatted := make([]map[string]interface{}, 0, len(tools))
	for _, tool := range tools {
		toolMap, ok := tool.(map[string]interface{})
		if !ok {
			continue
		}

		formatted = append(formatted, map[string]interface{}{
			"name":        toolMap["name"],
			"description": toolMap["description"],
		})
	}

	return map[string]interface{}{
		"total_tools": len(formatted),
		"tools":       formatted,
	}
}

// formatUserProfile extracts key user profile information
func (agent *AIAgent) formatUserProfile(rawResult interface{}) interface{} {
	profile, ok := rawResult.(map[string]interface{})
	if !ok {
		return rawResult
	}

	return map[string]interface{}{
		"login":        profile["login"],
		"name":         profile["name"],
		"bio":          profile["bio"],
		"company":      profile["company"],
		"location":     profile["location"],
		"email":        profile["email"],
		"blog":         profile["blog"],
		"twitter":      profile["twitter_username"],
		"followers":    profile["followers"],
		"following":    profile["following"],
		"public_repos": profile["public_repos"],
		"public_gists": profile["public_gists"],
		"avatar_url":   profile["avatar_url"],
		"html_url":     profile["html_url"],
		"created_at":   profile["created_at"],
		"updated_at":   profile["updated_at"],
	}
}

// formatRepositoriesList formats repository list response
func (agent *AIAgent) formatRepositoriesList(rawResult interface{}) interface{} {
	repos, ok := rawResult.([]interface{})
	if !ok {
		return rawResult
	}

	formatted := make([]map[string]interface{}, 0, len(repos))
	for _, repo := range repos {
		repoMap, ok := repo.(map[string]interface{})
		if !ok {
			continue
		}

		formatted = append(formatted, map[string]interface{}{
			"name":        repoMap["name"],
			"full_name":   repoMap["full_name"],
			"description": repoMap["description"],
			"private":     repoMap["private"],
			"html_url":    repoMap["html_url"],
			"stars":       repoMap["stargazers_count"],
			"forks":       repoMap["forks_count"],
			"language":    repoMap["language"],
			"created_at":  repoMap["created_at"],
			"updated_at":  repoMap["updated_at"],
		})
	}

	return map[string]interface{}{
		"total_repositories": len(formatted),
		"repositories":       formatted,
	}
}

// Made with Bob
