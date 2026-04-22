// Package envoy provides the backend implementation for the Envoy-based architecture.
package envoy

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"
)

// SessionManager manages user sessions with the Token Broker.
type SessionManager struct {
	tokenBrokerURL string
	sessions       map[string]*UserSession // userID -> session
	stateToUser    map[string]string       // OAuth state -> userID (for callback routing)
	mu             sync.RWMutex
	client         *TokenBrokerClient
	jobManager     *JobManager // Reference to job manager for OAuth events
}

// UserSession represents an active user session.
type UserSession struct {
	UserID          string
	SessionKey      string
	CreatedAt       time.Time
	EventChan       chan Event
	StopChan        chan struct{}
	AgentURL        string
	eventPollerDone chan struct{}
	currentJobID    string // Current job being processed
	mu              sync.RWMutex
}

// Event represents an event from the Token Broker.
type Event struct {
	Type         string `json:"type"`
	MCPServerURL string `json:"mcp_server_url,omitempty"`
	AuthURL      string `json:"auth_url,omitempty"`
	Message      string `json:"message,omitempty"`
	Code         string `json:"code,omitempty"`
}

// NewSessionManager creates a new session manager.
func NewSessionManager(tokenBrokerURL string) *SessionManager {
	return &SessionManager{
		tokenBrokerURL: tokenBrokerURL,
		sessions:       make(map[string]*UserSession),
		stateToUser:    make(map[string]string),
		client:         NewTokenBrokerClient(tokenBrokerURL),
	}
}

// SetJobManager sets the job manager reference for OAuth event handling.
func (sm *SessionManager) SetJobManager(jobManager *JobManager) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.jobManager = jobManager
}

// LinkJobToSession links a job to a user session for OAuth event handling.
func (sm *SessionManager) LinkJobToSession(userID, jobID string) {
	sm.mu.RLock()
	session, ok := sm.sessions[userID]
	sm.mu.RUnlock()

	if ok {
		session.mu.Lock()
		session.currentJobID = jobID
		session.mu.Unlock()
	}
}

// CreateSession creates a new session for a user.
func (sm *SessionManager) CreateSession(ctx context.Context, userID, agentURL string) (*UserSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if user already has a session
	if existing, ok := sm.sessions[userID]; ok {
		return existing, nil
	}

	// Create session with Token Broker
	sessionKey, err := sm.client.CreateSession(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to create session with Token Broker: %w", err)
	}

	// Create user session
	session := &UserSession{
		UserID:          userID,
		SessionKey:      sessionKey,
		CreatedAt:       time.Now(),
		EventChan:       make(chan Event, 10),
		StopChan:        make(chan struct{}),
		AgentURL:        agentURL,
		eventPollerDone: make(chan struct{}),
	}

	sm.sessions[userID] = session

	// Start event poller
	go sm.pollEvents(session)

	return session, nil
}

// GetSession retrieves a session for a user.
func (sm *SessionManager) GetSession(userID string) (*UserSession, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[userID]
	if !ok {
		return nil, fmt.Errorf("session not found for user: %s", userID)
	}

	return session, nil
}

// EndSession terminates a session.
func (sm *SessionManager) EndSession(ctx context.Context, userID string) error {
	sm.mu.Lock()
	session, ok := sm.sessions[userID]
	if !ok {
		sm.mu.Unlock()
		return fmt.Errorf("session not found for user: %s", userID)
	}
	delete(sm.sessions, userID)
	sm.mu.Unlock()

	// Stop event poller
	close(session.StopChan)
	<-session.eventPollerDone

	// End session with Token Broker
	if err := sm.client.EndSession(ctx, session.SessionKey, userID); err != nil {
		return fmt.Errorf("failed to end session with Token Broker: %w", err)
	}

	return nil
}

// pollEvents continuously polls the Token Broker for events.
func (sm *SessionManager) pollEvents(session *UserSession) {
	defer close(session.eventPollerDone)

	for {
		select {
		case <-session.StopChan:
			return
		default:
			// Long-poll for events (300s timeout)
			ctx, cancel := context.WithTimeout(context.Background(), 310*time.Second)
			event, err := sm.client.PollEvents(ctx, session.SessionKey, session.UserID)
			cancel()

			if err != nil {
				// Check if session was stopped
				select {
				case <-session.StopChan:
					return
				default:
					// Log error and retry after a short delay
					time.Sleep(5 * time.Second)
					continue
				}
			}

			// Handle OAuth events for jobs
			if event.Type == "oauth_required" && event.AuthURL != "" && sm.jobManager != nil {
				session.mu.RLock()
				jobID := session.currentJobID
				session.mu.RUnlock()

				if jobID != "" {
					// Update job status to oauth_required
					sm.jobManager.SetJobOAuthRequired(jobID, event.AuthURL)

					// Register OAuth state for callback routing
					if authURL, err := url.Parse(event.AuthURL); err == nil {
						if state := authURL.Query().Get("state"); state != "" {
							sm.RegisterOAuthState(state, session.UserID)
						}
					}
				}
			}

			// Send event to session's event channel (for SSE)
			select {
			case session.EventChan <- *event:
			case <-session.StopChan:
				return
			}
		}
	}
}

// RegisterOAuthState maps an OAuth state parameter to a userID so the callback can find the session.
func (sm *SessionManager) RegisterOAuthState(state, userID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.stateToUser[state] = userID
}

// LookupUserByState returns the userID for an OAuth state parameter.
func (sm *SessionManager) LookupUserByState(state string) (string, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	userID, ok := sm.stateToUser[state]
	return userID, ok
}

// CompleteOAuth completes an OAuth flow.
func (sm *SessionManager) CompleteOAuth(ctx context.Context, userID, code, state string) error {
	sm.mu.Lock()
	delete(sm.stateToUser, state)
	sm.mu.Unlock()

	session, err := sm.GetSession(userID)
	if err != nil {
		return err
	}

	return sm.client.CompleteOAuth(ctx, session.SessionKey, userID, code, state)
}

// GetSessionKey returns the session key for a user.
func (us *UserSession) GetSessionKey() string {
	us.mu.RLock()
	defer us.mu.RUnlock()
	return us.SessionKey
}

// WaitForEvent waits for an event with a timeout.
func (us *UserSession) WaitForEvent(timeout time.Duration) (*Event, error) {
	select {
	case event := <-us.EventChan:
		return &event, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for event")
	case <-us.StopChan:
		return nil, fmt.Errorf("session stopped")
	}
}

// Made with Bob
