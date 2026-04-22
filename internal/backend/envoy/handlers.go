package envoy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// Handler handles HTTP requests for the Envoy-based backend.
type Handler struct {
	sessionManager *SessionManager
	jobManager     *JobManager
	logger         *slog.Logger
}

// NewHandler creates a new handler.
func NewHandler(sessionManager *SessionManager, jobManager *JobManager, logger *slog.Logger) *Handler {
	return &Handler{
		sessionManager: sessionManager,
		jobManager:     jobManager,
		logger:         logger,
	}
}

// HandleTask handles task requests from the browser.
// Returns a job_id immediately and processes the task asynchronously.
func (h *Handler) HandleTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID   string `json:"user_id"`
		Task     string `json:"task"`
		AgentURL string `json:"agent_url,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("Invalid task request", "error", err)
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.UserID == "" || req.Task == "" {
		h.logger.Error("Missing required fields", "user_id", req.UserID, "task", req.Task)
		http.Error(w, "Missing user_id or task", http.StatusBadRequest)
		return
	}

	// Default agent URL if not provided
	if req.AgentURL == "" {
		req.AgentURL = "http://aiagent-service:8186/task"
	}

	h.logger.Info("Received task request",
		"user_id", req.UserID,
		"task", req.Task,
		"agent_url", req.AgentURL)

	// Create job immediately
	job := h.jobManager.CreateJob(req.UserID, req.Task)

	// Return job_id immediately
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"job_id": job.ID,
	})

	// Process task asynchronously
	go h.processTask(job, req.AgentURL)
}

// processTask processes a task asynchronously.
func (h *Handler) processTask(job *Job, agentURL string) {
	// Update status to processing
	h.jobManager.UpdateJobStatus(job.ID, JobStatusProcessing)

	// Get or create session
	session, err := h.sessionManager.GetSession(job.UserID)
	if err != nil {
		// Create new session
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		session, err = h.sessionManager.CreateSession(ctx, job.UserID, agentURL)
		if err != nil {
			h.logger.Error("Failed to create session", "error", err, "user_id", job.UserID, "job_id", job.ID)
			h.jobManager.SetJobError(job.ID, fmt.Sprintf("Failed to create session: %v", err))
			return
		}

		h.logger.Info("Session created",
			"user_id", job.UserID,
			"session_key", session.SessionKey,
			"job_id", job.ID)
	}

	// Link job to session for OAuth event handling
	h.sessionManager.LinkJobToSession(job.UserID, job.ID)

	// Forward request to AI Agent with session key
	taskBody, _ := json.Marshal(map[string]string{
		"user_id": job.UserID,
		"task":    job.Task,
	})

	resp, err := h.sessionManager.client.ForwardToAgent(
		job.GetContext(),
		agentURL,
		session.SessionKey,
		job.UserID,
		taskBody,
	)
	if err != nil {
		h.logger.Error("Failed to forward request to Agent", "error", err, "user_id", job.UserID, "job_id", job.ID)
		h.jobManager.SetJobError(job.ID, fmt.Sprintf("Failed to forward request: %v", err))
		return
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		h.logger.Error("Failed to read Agent response", "error", err, "user_id", job.UserID, "job_id", job.ID)
		h.jobManager.SetJobError(job.ID, "Failed to read response")
		return
	}

	h.logger.Info("Received response from Agent",
		"user_id", job.UserID,
		"job_id", job.ID,
		"status_code", resp.StatusCode,
		"content_type", resp.Header.Get("Content-Type"),
		"body_length", len(body))

	// Handle error responses
	if resp.StatusCode >= 400 {
		h.logger.Error("Agent returned error",
			"user_id", job.UserID,
			"job_id", job.ID,
			"status_code", resp.StatusCode,
			"body", string(body))

		// Try to parse as JSON first
		var jsonErr map[string]interface{}
		if err := json.Unmarshal(body, &jsonErr); err == nil {
			// Valid JSON error
			errorMsg := fmt.Sprintf("Agent error (status %d)", resp.StatusCode)
			if msg, ok := jsonErr["message"].(string); ok {
				errorMsg = msg
			} else if errStr, ok := jsonErr["error"].(string); ok {
				errorMsg = errStr
			}
			h.jobManager.SetJobError(job.ID, errorMsg)
			return
		}

		// Not JSON - use raw body
		errorMsg := string(body)
		if errorMsg == "" {
			errorMsg = fmt.Sprintf("Request failed with status %d", resp.StatusCode)
		}
		h.jobManager.SetJobError(job.ID, errorMsg)
		return
	}

	// Success response - store result
	h.jobManager.SetJobResult(job.ID, json.RawMessage(body))
}

// HandleJobStatus handles GET /job/{job_id} requests to check job status.
func (h *Handler) HandleJobStatus(w http.ResponseWriter, r *http.Request) {
	// Extract job_id from URL path
	jobID := r.URL.Path[len("/job/"):]
	if jobID == "" {
		h.logger.Error("Missing job_id in status request")
		http.Error(w, "Missing job_id", http.StatusBadRequest)
		return
	}

	h.logger.Info("Job status request", "job_id", jobID)

	status, err := h.jobManager.GetJobStatus(jobID)
	if err != nil {
		h.logger.Error("Job not found", "job_id", jobID, "error", err)
		http.Error(w, fmt.Sprintf("Job not found: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(status)
}

// HandleOAuthCallback handles OAuth callbacks from the OAuth provider.
// The callback URL receives code and state from the OAuth provider.
// The userID is resolved from the state parameter via the stateToUser map
// (registered when the Backend relays oauth_required events to the browser).
func (h *Handler) HandleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if code == "" {
		h.logger.Error("OAuth callback missing code")
		http.Error(w, "Missing code parameter", http.StatusBadRequest)
		return
	}

	if state == "" {
		h.logger.Error("OAuth callback missing state")
		http.Error(w, "Missing state parameter", http.StatusBadRequest)
		return
	}

	userID, ok := h.sessionManager.LookupUserByState(state)
	if !ok {
		h.logger.Error("OAuth callback unknown state", "state_length", len(state))
		http.Error(w, "Unknown OAuth state — session may have expired", http.StatusBadRequest)
		return
	}

	h.logger.Info("OAuth callback received",
		"user_id", userID,
		"has_code", code != "",
		"state_length", len(state))

	// Complete OAuth with Token Broker
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := h.sessionManager.CompleteOAuth(ctx, userID, code, state); err != nil {
		h.logger.Error("Failed to complete OAuth", "error", err, "user_id", userID)
		http.Error(w, fmt.Sprintf("Failed to complete OAuth: %v", err), http.StatusInternalServerError)
		return
	}

	h.logger.Info("OAuth completed successfully", "user_id", userID)

	// Redirect to demo page with success message
	redirectURL := fmt.Sprintf("/demo?oauth_success=true&user_id=%s", userID)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// HandleEvents handles Server-Sent Events (SSE) for OAuth events.
// This allows the browser to receive real-time OAuth events.
func (h *Handler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		h.logger.Error("Missing user_id in events request")
		http.Error(w, "Missing user_id parameter", http.StatusBadRequest)
		return
	}

	// Wait for session to be created (SSE connect may race with POST /task)
	var session *UserSession
	for i := 0; i < 10; i++ {
		session, _ = h.sessionManager.GetSession(userID)
		if session != nil {
			break
		}
		select {
		case <-time.After(500 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
	}
	if session == nil {
		h.logger.Error("Session not found for events after waiting", "user_id", userID)
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	h.logger.Info("SSE connection established", "user_id", userID)

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Flush headers
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Send events to browser
	for {
		select {
		case event := <-session.EventChan:
			h.logger.Info("Sending event to browser",
				"user_id", userID,
				"event_type", event.Type)

			// For oauth_required events, extract state from auth_url and register mapping
			if event.Type == "oauth_required" && event.AuthURL != "" {
				if authURL, err := url.Parse(event.AuthURL); err == nil {
					if state := authURL.Query().Get("state"); state != "" {
						h.sessionManager.RegisterOAuthState(state, userID)
						h.logger.Info("Registered OAuth state",
							"user_id", userID,
							"state_length", len(state))
					}
				}
			}

			// Format as SSE
			eventData, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", eventData)

			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}

		case <-r.Context().Done():
			h.logger.Info("SSE connection closed", "user_id", userID)
			return

		case <-session.StopChan:
			h.logger.Info("Session stopped", "user_id", userID)
			return
		}
	}
}

// HandleEndSession handles session termination requests.
func (h *Handler) HandleEndSession(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	if userID == "" {
		h.logger.Error("Missing user_id in end session request")
		http.Error(w, "Missing user_id parameter", http.StatusBadRequest)
		return
	}

	h.logger.Info("Ending session", "user_id", userID)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := h.sessionManager.EndSession(ctx, userID); err != nil {
		h.logger.Error("Failed to end session", "error", err, "user_id", userID)
		http.Error(w, fmt.Sprintf("Failed to end session: %v", err), http.StatusInternalServerError)
		return
	}

	h.logger.Info("Session ended successfully", "user_id", userID)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// HandleHealth handles health check requests.
func (h *Handler) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// Made with Bob
