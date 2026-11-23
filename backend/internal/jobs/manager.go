// Package jobs implements the persistent job queue stored in SQLite:
// download, scan and metadata jobs with status tracking.
package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
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

// NullTime is a database-nullable time.Time. It marshals to an RFC 3339
// string in JSON (or null when unset).
type NullTime struct {
	sql.NullTime
}

// MarshalJSON implements json.Marshaler.
func (n NullTime) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(n.Time)
}

// UnmarshalJSON implements json.Unmarshaler.
func (n *NullTime) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		n.NullTime = sql.NullTime{}
		return nil
	}
	var t time.Time
	if err := json.Unmarshal(b, &t); err != nil {
		return err
	}
	n.NullTime = sql.NullTime{Valid: true, Time: t}
	return nil
}

// Job is one row in the jobs table.
type Job struct {
	ID          string    `json:"id" db:"id"`
	Type        JobType   `json:"type" db:"type"`
	Status      JobStatus `json:"status" db:"status"`
	Details     string    `json:"details" db:"details"`
	Progress    string    `json:"progress" db:"progress"`
	Error       string    `json:"error,omitempty" db:"error"`
	Attempts    int       `json:"attempts" db:"attempts"`
	NextRetryAt NullTime  `json:"next_retry_at" db:"next_retry_at"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Errors returned by the retry operations.
var (
	// ErrJobNotFound is returned when a job id does not exist.
	ErrJobNotFound = errors.New("job not found")
	// ErrNotRetryable is returned when a job exists but is not a failed
	// download and therefore cannot be retried.
	ErrNotRetryable = errors.New("job is not a failed download")
)

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
	query := `SELECT id, type, status, details, COALESCE(progress, '') as progress, COALESCE(error, '') as error, COALESCE(attempts, 0) as attempts, next_retry_at, created_at, updated_at FROM jobs WHERE id = ?`
	err := m.db.Get(&job, query, id)
	return &job, err
}

// GetAllJobs returns all jobs, newest first.
func (m *Manager) GetAllJobs() ([]*Job, error) {
	var jobs []*Job
	query := `SELECT id, type, status, details, COALESCE(progress, '') as progress, COALESCE(error, '') as error, COALESCE(attempts, 0) as attempts, next_retry_at, created_at, updated_at FROM jobs ORDER BY created_at DESC`
	err := m.db.Select(&jobs, query)
	return jobs, err
}

// RetryJob re-queues one failed download job. It is the manual-retry path:
// the job must exist, be of type download and have status failed. The
// failed→queued transition is a single conditional UPDATE, so exactly one
// concurrent caller wins: the loser sees zero affected rows, re-reads the
// job and reports ErrJobNotFound (row gone) or ErrNotRetryable (row exists
// but is no longer a failed download, including losing the race). On
// success the status resets to queued, progress and error are cleared,
// details are kept, next_retry_at is cleared and attempts is incremented.
// It returns the updated job.
func (m *Manager) RetryJob(id string) (*Job, error) {
	res, err := m.db.Exec(`UPDATE jobs SET status = ?, progress = '', error = '', attempts = attempts + 1, next_retry_at = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ? AND type = ? AND status = ?`,
		StatusQueued, id, TypeDownload, StatusFailed)
	if err != nil {
		return nil, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		_, err := m.GetJob(id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrJobNotFound
		}
		if err != nil {
			return nil, err
		}
		return nil, ErrNotRetryable
	}
	return m.GetJob(id)
}

// RetryFailedDownloads retries every failed download job and returns the
// jobs that were re-queued. Jobs that changed state in the meantime are
// skipped, not errors.
func (m *Manager) RetryFailedDownloads() ([]*Job, error) {
	var ids []string
	query := `SELECT id FROM jobs WHERE type = ? AND status = ?`
	if err := m.db.Select(&ids, query, TypeDownload, StatusFailed); err != nil {
		return nil, err
	}
	var retried []*Job
	for _, id := range ids {
		job, err := m.RetryJob(id)
		if err != nil {
			if errors.Is(err, ErrJobNotFound) || errors.Is(err, ErrNotRetryable) {
				continue
			}
			return nil, err
		}
		retried = append(retried, job)
	}
	return retried, nil
}

// ScheduleRetry arms an automatic retry of a failed download: it sets the
// given attempts count, moves the job back to queued with cleared progress,
// stores errMsg (the "retrying (n/max): ..." message) and arms
// nextRetryAt so the dispatcher defers the run until that instant.
func (m *Manager) ScheduleRetry(id string, attempts int, nextRetryAt time.Time, errMsg string) error {
	_, err := m.db.Exec(`UPDATE jobs SET status = ?, progress = '', error = ?, attempts = ?, next_retry_at = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		StatusQueued, errMsg, attempts, nextRetryAt, id)
	return err
}

// ClearCompleted deletes all completed and failed jobs.
func (m *Manager) ClearCompleted() error {
	_, err := m.db.Exec("DELETE FROM jobs WHERE status IN (?, ?)", StatusCompleted, StatusFailed)
	return err
}
