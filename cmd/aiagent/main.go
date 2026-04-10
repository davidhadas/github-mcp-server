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
)

// Logger for AI Agent
var agentLogger *slog.Logger

func init() {
	// Create separate log file for AI Agent
	logFile, err := os.OpenFile("/tmp/aiagent.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic(fmt.Sprintf("Failed to open aiagent log file: %v", err))
	}

	agentLogger = slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// TaskRequest represents a task from the browser
type TaskRequest struct {
	UserID       string                 `json:"user_id"`
	Task         string                 `json:"task"`
	MCPServerURL string                 `json:"mcp_server_url"`
	Params       map[string]interface{} `json:"params,omitempty"`
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
	authBridgeURL string // URL of AuthBridge MCP proxy
}

// NewAIAgent creates a new AI Agent instance
func NewAIAgent(authBridgeURL string) *AIAgent {
	return &AIAgent{
		authBridgeURL: authBridgeURL,
	}
}

// HandleTask processes a task request
func (agent *AIAgent) HandleTask(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	var taskReq TaskRequest
	if err := json.NewDecoder(r.Body).Decode(&taskReq); err != nil {
		agentLogger.Error("Invalid task request", "error", err.Error())
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	agentLogger.Info("Received task from AuthBridge",
		"user_id", taskReq.UserID,
		"task", taskReq.Task)

	// Convert task to MCP request
	mcpReq := agent.taskToMCPRequest(taskReq)
	agentLogger.Info("Task converted to MCP request",
		"method", mcpReq.Method,
		"params", mcpReq.Params)

	// Make MCP request to AuthBridge proxy
	agentLogger.Info("Sending MCP request to AuthBridge proxy",
		"url", agent.authBridgeURL,
		"user_id", mcpReq.UserID,
		"method", mcpReq.Method)

	mcpResp, err := agent.makeMCPRequest(mcpReq)
	if err != nil {
		agentLogger.Error("MCP request failed", "error", err.Error())
		http.Error(w, fmt.Sprintf("MCP request failed: %v", err), http.StatusInternalServerError)
		return
	}

	agentLogger.Info("Received response from AuthBridge proxy",
		"status", mcpResp.Status,
		"user_id", taskReq.UserID)

	// Convert MCP response to task result
	taskResp := agent.mcpResponseToTaskResult(mcpResp, taskReq, mcpReq, startTime)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(taskResp)
}

// taskToMCPRequest converts a task description to an MCP request
func (agent *AIAgent) taskToMCPRequest(task TaskRequest) MCPRequest {
	taskLower := strings.ToLower(task.Task)
	method := "tools/call"
	params := task.Params

	if params == nil {
		params = make(map[string]interface{})
	}

	// Detect task intent and map to appropriate MCP tool
	if params["name"] == nil {
		// Check for repository-related tasks first (more specific)
		if strings.Contains(taskLower, "repo") || strings.Contains(taskLower, "repositories") {
			params["name"] = "list_repos"
		} else if strings.Contains(taskLower, "list") && (strings.Contains(taskLower, "tool") || strings.Contains(taskLower, "available")) {
			// List tools
			method = "tools/list"
			params = make(map[string]interface{}) // tools/list doesn't need params
		} else if strings.Contains(taskLower, "profile") || strings.Contains(taskLower, "who am i") {
			// Get user profile
			params["name"] = "get_me"
		} else {
			// Default to get_me for tasks mentioning "me" or "my"
			params["name"] = "get_me"
		}
	}

	agentLogger.Info("Task intent detected",
		"task", task.Task,
		"method", method,
		"tool", params["name"])

	return MCPRequest{
		UserID:       task.UserID,
		MCPServerURL: task.MCPServerURL,
		Method:       method,
		Params:       params,
	}
}

// makeMCPRequest sends an MCP request to AuthBridge proxy
func (agent *AIAgent) makeMCPRequest(mcpReq MCPRequest) (*MCPResponse, error) {
	reqBody, err := json.Marshal(mcpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal MCP request: %w", err)
	}

	resp, err := http.Post(agent.authBridgeURL, "application/json", bytes.NewReader(reqBody))
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

	// Parse raw response
	var rawResult interface{}
	if err := json.Unmarshal(mcpResp.Body, &rawResult); err != nil {
		agentLogger.Warn("Failed to parse response as JSON", "error", err.Error())
		rawResult = string(mcpResp.Body)
	}

	// Format response based on task type
	formattedResult := agent.formatResponseByTaskType(rawResult, task.Task, mcpReq)

	// Build enhanced result with metadata
	result := map[string]interface{}{
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
			case "list_repos":
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

// formatToolsList formats a tools/list response
func (agent *AIAgent) formatToolsList(rawResult interface{}) interface{} {
	// For tools/list, just return the raw result (it's already well-formatted)
	return rawResult
}

// formatUserProfile formats a user profile response
func (agent *AIAgent) formatUserProfile(rawResult interface{}) interface{} {
	// Extract relevant user profile fields
	if resultMap, ok := rawResult.(map[string]interface{}); ok {
		formatted := make(map[string]interface{})
		relevantFields := []string{
			"login", "name", "email", "bio", "company", "location", "blog",
			"twitter", "avatar_url", "html_url",
			"public_repos", "public_gists", "followers", "following",
			"created_at", "updated_at",
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
	// Check if result is an array of repositories
	if resultArray, ok := rawResult.([]interface{}); ok {
		formatted := map[string]interface{}{
			"repositories":       resultArray,
			"total_repositories": len(resultArray),
		}
		return formatted
	}

	return rawResult
}

func main() {
	port := 8186
	authBridgeURL := "http://localhost:8185/mcp"

	agent := NewAIAgent(authBridgeURL)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Task endpoint
	r.Post("/task", agent.HandleTask)

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	agentLogger.Info("AI Agent starting",
		"port", port,
		"authbridge_url", authBridgeURL)

	log.Printf("AI Agent listening on :%d", port)
	log.Printf("AuthBridge MCP Proxy URL: %s", authBridgeURL)

	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), r); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// Made with Bob
