// Package jobs implements the persistent job queue stored in SQLite:
// download, scan and metadata jobs with status tracking.
package jobs

import (
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"

	"yt-scraper/internal/db"
)

// JobStatus is the lifecycle state of a job.
type JobStatus string

// JobType is the kind of work a job performs.
type JobType string

// Job statuses and types persisted in the jobs table.
const (
	StatusQueued      JobStatus = "queued"
	StatusRunning     JobStatus = "running"
	StatusDownloading JobStatus = "downloading" // For backward compatibility
	StatusCompleted   JobStatus = "completed"
	StatusFailed      JobStatus = "failed"

	TypeDownload JobType = "download"
	TypeScan     JobType = "scan"
	TypeMetadata JobType = "metadata"
)

// Job is one row in the jobs table.
type Job struct {
	ID        string    `json:"id" db:"id"`
	Type      JobType   `json:"type" db:"type"`
	Status    JobStatus `json:"status" db:"status"`
	Details   string    `json:"details" db:"details"`
	Progress  string    `json:"progress" db:"progress"`
	Error     string    `json:"error,omitempty" db:"error"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// Manager creates and queries jobs.
type Manager struct {
	db *db.DB
}

// NewManager builds a Manager on top of the given database.
func NewManager(db *db.DB) *Manager {
	return &Manager{db: db}
}

// CreateJob creates a single queued job and returns it.
func (m *Manager) CreateJob(jobType JobType, details interface{}) (*Job, error) {
	jobs, err := m.BulkCreateJobs(jobType, []interface{}{details})
	if err != nil || len(jobs) == 0 {
		return nil, err
	}
	return jobs[0], nil
}

// BulkCreateJobs creates one queued job per details entry in a single
// transaction.
func (m *Manager) BulkCreateJobs(jobType JobType, detailsList []interface{}) ([]*Job, error) {
	tx, err := m.db.Beginx()
	if err != nil {
		return nil, err
	}

	var createdJobs []*Job
	query := `INSERT INTO jobs (id, type, status, details, created_at, updated_at) VALUES (:id, :type, :status, :details, :created_at, :updated_at)`

	now := time.Now()
	for _, details := range detailsList {
		detailsBytes, _ := json.Marshal(details)
		job := &Job{
			ID:        uuid.New().String(),
			Type:      jobType,
			Status:    StatusQueued,
			Details:   string(detailsBytes),
			CreatedAt: now,
			UpdatedAt: now,
		}

		if _, err := tx.NamedExec(query, job); err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				log.Printf("jobs: rollback after insert error: %v", rbErr)
			}
			return nil, err
		}
		createdJobs = append(createdJobs, job)
	}

	err = tx.Commit()
	return createdJobs, err
}

// UpdateJobStatus sets the status, progress and error message of a job.
func (m *Manager) UpdateJobStatus(id string, status JobStatus, progress string, errorMsg string) error {
	query := `UPDATE jobs SET status = ?, progress = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	_, err := m.db.Exec(query, status, progress, errorMsg, id)
	return err
}

// GetJob returns the job with the given id (sql.ErrNoRows when missing).
func (m *Manager) GetJob(id string) (*Job, error) {
	var job Job
	query := `SELECT id, type, status, details, COALESCE(progress, '') as progress, COALESCE(error, '') as error, created_at, updated_at FROM jobs WHERE id = ?`
	err := m.db.Get(&job, query, id)
	return &job, err
}

// GetAllJobs returns all jobs, newest first.
func (m *Manager) GetAllJobs() ([]*Job, error) {
	var jobs []*Job
	query := `SELECT id, type, status, details, COALESCE(progress, '') as progress, COALESCE(error, '') as error, created_at, updated_at FROM jobs ORDER BY created_at DESC`
	err := m.db.Select(&jobs, query)
	return jobs, err
}

// ClearCompleted deletes all completed and failed jobs.
func (m *Manager) ClearCompleted() error {
	_, err := m.db.Exec("DELETE FROM jobs WHERE status IN (?, ?)", StatusCompleted, StatusFailed)
	return err
}
