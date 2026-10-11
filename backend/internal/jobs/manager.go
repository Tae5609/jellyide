package jobs

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"yt-scraper/internal/db"
)

type JobStatus string
type JobType string

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

type Manager struct {
	db *db.DB
}

func NewManager(db *db.DB) *Manager {
	return &Manager{db: db}
}

func (m *Manager) CreateJob(jobType JobType, details interface{}) (*Job, error) {
	jobs, err := m.BulkCreateJobs(jobType, []interface{}{details})
	if err != nil || len(jobs) == 0 {
		return nil, err
	}
	return jobs[0], nil
}

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

		_, err := tx.NamedExec(query, job)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		createdJobs = append(createdJobs, job)
	}

	err = tx.Commit()
	return createdJobs, err
}

func (m *Manager) UpdateJobStatus(id string, status JobStatus, progress string, errorMsg string) error {
	query := `UPDATE jobs SET status = ?, progress = ?, error = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`
	_, err := m.db.Exec(query, status, progress, errorMsg, id)
	return err
}

func (m *Manager) GetJob(id string) (*Job, error) {
	var job Job
	query := `SELECT id, type, status, details, COALESCE(progress, '') as progress, COALESCE(error, '') as error, created_at, updated_at FROM jobs WHERE id = ?`
	err := m.db.Get(&job, query, id)
	return &job, err
}

func (m *Manager) GetAllJobs() ([]*Job, error) {
	var jobs []*Job
	query := `SELECT id, type, status, details, COALESCE(progress, '') as progress, COALESCE(error, '') as error, created_at, updated_at FROM jobs ORDER BY created_at DESC`
	err := m.db.Select(&jobs, query)
	return jobs, err
}

func (m *Manager) ClearCompleted() error {
	_, err := m.db.Exec("DELETE FROM jobs WHERE status IN (?, ?)", StatusCompleted, StatusFailed)
	return err
}
