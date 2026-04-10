package aiagent

// TaskRequest represents a task from the browser/frontend
type TaskRequest struct {
	UserID string                 `json:"user_id"`
	Task   string                 `json:"task"`
	Params map[string]interface{} `json:"params,omitempty"`
}

// TaskResponse represents the result of a task
type TaskResponse struct {
	Status  string                 `json:"status"`
	Message string                 `json:"message,omitempty"`
	Result  map[string]interface{} `json:"result,omitempty"`
}

// MCPRequest represents an MCP protocol request
type MCPRequest struct {
	UserID       string                 `json:"user_id"`
	MCPServerURL string                 `json:"mcp_server_url"`
	Method       string                 `json:"method"`
	Params       map[string]interface{} `json:"params,omitempty"`
}

// MCPResponse represents an MCP protocol response
type MCPResponse struct {
	Status int    `json:"status"`
	Body   []byte `json:"body"`
}

// Made with Bob
