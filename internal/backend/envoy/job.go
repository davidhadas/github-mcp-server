package envoy

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// JobStatus represents the state of a job.
type JobStatus string

const (
	JobStatusPending       JobStatus = "pending"        // Job created, not started
	JobStatusOAuthRequired JobStatus = "oauth_required" // Waiting for OAuth
	JobStatusProcessing    JobStatus = "processing"     // Task executing
	JobStatusCompleted     JobStatus = "completed"      // Task done
	JobStatusFailed        JobStatus = "failed"         // Task failed
)

// Job represents an asynchronous task job.
type Job struct {
	ID        string             `json:"job_id"`
	UserID    string             `json:"user_id"`
	Task      string             `json:"task"`
	Status    JobStatus          `json:"status"`
	Result    json.RawMessage    `json:"result,omitempty"`
	Error     string             `json:"error,omitempty"`
	AuthURL   string             `json:"auth_url,omitempty"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	ctx       context.Context    // Context for the job execution
	cancel    context.CancelFunc // Cancel function for the job
	mu        sync.RWMutex       // Protects job fields
}

// JobManager manages asynchronous jobs.
type JobManager struct {
	jobs   map[string]*Job // jobID -> Job
	mu     sync.RWMutex
	logger interface {
		Info(msg string, args ...interface{})
		Error(msg string, args ...interface{})
	}
}

// NewJobManager creates a new job manager.
func NewJobManager(logger interface {
	Info(msg string, args ...interface{})
	Error(msg string, args ...interface{})
}) *JobManager {
	return &JobManager{
		jobs:   make(map[string]*Job),
		logger: logger,
	}
}

// CreateJob creates a new job and returns its ID.
func (jm *JobManager) CreateJob(userID, task string) *Job {
	jobID := uuid.New().String()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)

	job := &Job{
		ID:        jobID,
		UserID:    userID,
		Task:      task,
		Status:    JobStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		ctx:       ctx,
		cancel:    cancel,
	}

	jm.mu.Lock()
	jm.jobs[jobID] = job
	jm.mu.Unlock()

	jm.logger.Info("Job created", "job_id", jobID, "user_id", userID, "task", task)

	return job
}

// GetJob retrieves a job by ID.
func (jm *JobManager) GetJob(jobID string) (*Job, error) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	job, ok := jm.jobs[jobID]
	if !ok {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}

	return job, nil
}

// UpdateJobStatus updates the status of a job.
func (jm *JobManager) UpdateJobStatus(jobID string, status JobStatus) error {
	job, err := jm.GetJob(jobID)
	if err != nil {
		return err
	}

	job.mu.Lock()
	job.Status = status
	job.UpdatedAt = time.Now()
	job.mu.Unlock()

	jm.logger.Info("Job status updated", "job_id", jobID, "status", status)

	return nil
}

// SetJobOAuthRequired marks a job as requiring OAuth and stores the auth URL.
func (jm *JobManager) SetJobOAuthRequired(jobID, authURL string) error {
	job, err := jm.GetJob(jobID)
	if err != nil {
		return err
	}

	job.mu.Lock()
	job.Status = JobStatusOAuthRequired
	job.AuthURL = authURL
	job.UpdatedAt = time.Now()
	job.mu.Unlock()

	jm.logger.Info("Job requires OAuth", "job_id", jobID, "auth_url", authURL)

	return nil
}

// SetJobResult marks a job as completed with a result.
func (jm *JobManager) SetJobResult(jobID string, result json.RawMessage) error {
	job, err := jm.GetJob(jobID)
	if err != nil {
		return err
	}

	job.mu.Lock()
	job.Status = JobStatusCompleted
	job.Result = result
	job.UpdatedAt = time.Now()
	if job.cancel != nil {
		job.cancel()
	}
	job.mu.Unlock()

	jm.logger.Info("Job completed", "job_id", jobID)

	return nil
}

// SetJobError marks a job as failed with an error.
func (jm *JobManager) SetJobError(jobID string, errorMsg string) error {
	job, err := jm.GetJob(jobID)
	if err != nil {
		return err
	}

	job.mu.Lock()
	job.Status = JobStatusFailed
	job.Error = errorMsg
	job.UpdatedAt = time.Now()
	if job.cancel != nil {
		job.cancel()
	}
	job.mu.Unlock()

	jm.logger.Error("Job failed", "job_id", jobID, "error", errorMsg)

	return nil
}

// GetJobStatus returns the current status of a job for API responses.
func (jm *JobManager) GetJobStatus(jobID string) (map[string]interface{}, error) {
	job, err := jm.GetJob(jobID)
	if err != nil {
		return nil, err
	}

	job.mu.RLock()
	defer job.mu.RUnlock()

	response := map[string]interface{}{
		"job_id":     job.ID,
		"status":     job.Status,
		"created_at": job.CreatedAt,
		"updated_at": job.UpdatedAt,
	}

	switch job.Status {
	case JobStatusOAuthRequired:
		response["auth_url"] = job.AuthURL
	case JobStatusCompleted:
		if job.Result != nil {
			var result interface{}
			if err := json.Unmarshal(job.Result, &result); err == nil {
				response["result"] = result
			}
		}
	case JobStatusFailed:
		response["error"] = job.Error
	}

	return response, nil
}

// CleanupOldJobs removes jobs older than the specified duration.
func (jm *JobManager) CleanupOldJobs(maxAge time.Duration) {
	jm.mu.Lock()
	defer jm.mu.Unlock()

	now := time.Now()
	for jobID, job := range jm.jobs {
		job.mu.RLock()
		age := now.Sub(job.CreatedAt)
		isTerminal := job.Status == JobStatusCompleted || job.Status == JobStatusFailed
		job.mu.RUnlock()

		if isTerminal && age > maxAge {
			if job.cancel != nil {
				job.cancel()
			}
			delete(jm.jobs, jobID)
			jm.logger.Info("Cleaned up old job", "job_id", jobID, "age", age)
		}
	}
}

// StartCleanupRoutine starts a background routine to clean up old jobs.
func (jm *JobManager) StartCleanupRoutine(interval, maxAge time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			jm.CleanupOldJobs(maxAge)
		}
	}()
}

// GetJobContext returns the context for a job.
func (j *Job) GetContext() context.Context {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.ctx
}

// Made with Bob
