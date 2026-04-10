package aiagent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
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
	taskResult := agent.mcpResponseToTaskResult(mcpResp, task.Task)

	agentLogger.Info("Task completed",
		"user_id", agent.userID,
		"result_status", taskResult.Status)

	return taskResult, nil
}

// taskToMCPRequest converts a task description to an MCP request
// This is where the AI agent's intelligence would go
func (agent *AIAgent) taskToMCPRequest(task TaskRequest) MCPRequest {
	// Simple mock implementation
	// Real implementation would use LLM to parse task and determine MCP method

	method := "tools/call"
	params := task.Params

	if params == nil {
		params = make(map[string]interface{})
	}

	// Example: if task contains "get user", call get_me
	// This is a simplified mock - real agent would be much smarter
	if params["name"] == nil {
		params["name"] = "get_me" // default tool
	}

	return MCPRequest{
		UserID:       agent.userID,
		MCPServerURL: task.MCPServerURL,
		Method:       method,
		Params:       params,
	}
}

// mcpResponseToTaskResult converts response to task result
func (agent *AIAgent) mcpResponseToTaskResult(mcpResp *MCPResponse, taskDesc string) *TaskResponse {
	if mcpResp.Status != http.StatusOK {
		agentLogger.Warn("Request failed",
			"status", mcpResp.Status)
		return &TaskResponse{
			Status:  "error",
			Message: fmt.Sprintf("Request failed with status %d", mcpResp.Status),
			Error:   string(mcpResp.Body),
		}
	}

	// Parse response
	var result interface{}
	if err := json.Unmarshal(mcpResp.Body, &result); err != nil {
		agentLogger.Warn("Failed to parse response as JSON",
			"error", err.Error())
		result = string(mcpResp.Body)
	}

	return &TaskResponse{
		Status:  "success",
		Message: fmt.Sprintf("Task completed: %s", taskDesc),
		Result:  result,
	}
}

// Made with Bob
