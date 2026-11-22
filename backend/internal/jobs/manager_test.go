package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yt-scraper/internal/db"
)

// newTestManager builds a Manager on a temp-dir SQLite database.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	database, err := db.NewDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return NewManager(database)
}

func TestCreateJob(t *testing.T) {
	m := newTestManager(t)

	job, err := m.CreateJob(TypeDownload, map[string]string{"video_id": "abc"})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if job.ID == "" {
		t.Fatal("job id is empty")
	}
	if job.Type != TypeDownload {
		t.Fatalf("type = %s, want download", job.Type)
	}
	if job.Status != StatusQueued {
		t.Fatalf("status = %s, want queued", job.Status)
	}
	if !strings.Contains(job.Details, "abc") {
		t.Fatalf("details = %q, want the marshaled payload", job.Details)
	}

	// It must be visible through GetJob and GetAllJobs.
	got, err := m.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.ID != job.ID || got.Status != StatusQueued {
		t.Fatalf("GetJob returned %+v", got)
	}
	all, err := m.GetAllJobs()
	if err != nil {
		t.Fatalf("GetAllJobs: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("GetAllJobs = %d jobs, want 1", len(all))
	}
}

func TestBulkCreateJobs(t *testing.T) {
	m := newTestManager(t)

	created, err := m.BulkCreateJobs(TypeScan, []interface{}{"a", "b", "c"})
	if err != nil {
		t.Fatalf("BulkCreateJobs: %v", err)
	}
	if len(created) != 3 {
		t.Fatalf("created %d jobs, want 3", len(created))
	}
	ids := map[string]bool{}
	for _, j := range created {
		if ids[j.ID] {
			t.Fatalf("duplicate job id %s", j.ID)
		}
		ids[j.ID] = true
	}
}

func TestGetJobMissing(t *testing.T) {
	m := newTestManager(t)

	_, err := m.GetJob("no-such-job")
	if err == nil {
		t.Fatal("GetJob for a missing id should return an error")
	}
}

func TestUpdateJobStatus(t *testing.T) {
	m := newTestManager(t)

	job, err := m.CreateJob(TypeDownload, "x")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := m.UpdateJobStatus(job.ID, StatusRunning, "42%", ""); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	got, err := m.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != StatusRunning || got.Progress != "42%" || got.Error != "" {
		t.Fatalf("job = %+v, want running/42%%/no error", got)
	}

	if err := m.UpdateJobStatus(job.ID, StatusFailed, "", "it broke"); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	got, err = m.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != StatusFailed || got.Error != "it broke" {
		t.Fatalf("job = %+v, want failed with error", got)
	}

	// Updating a missing job is a no-op without error.
	if err := m.UpdateJobStatus("no-such-job", StatusRunning, "", ""); err != nil {
		t.Fatalf("UpdateJobStatus on missing job: %v", err)
	}
}

func TestClearCompleted(t *testing.T) {
	m := newTestManager(t)

	keep, err := m.CreateJob(TypeDownload, "queued")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	done, err := m.CreateJob(TypeDownload, "completed")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	broken, err := m.CreateJob(TypeDownload, "failed")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	running, err := m.CreateJob(TypeScan, "running")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := m.UpdateJobStatus(done.ID, StatusCompleted, "100%", ""); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	if err := m.UpdateJobStatus(broken.ID, StatusFailed, "", "err"); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	if err := m.UpdateJobStatus(running.ID, StatusRunning, "", ""); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}

	if err := m.ClearCompleted(); err != nil {
		t.Fatalf("ClearCompleted: %v", err)
	}

	remaining, err := m.GetAllJobs()
	if err != nil {
		t.Fatalf("GetAllJobs: %v", err)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining = %d jobs, want 2", len(remaining))
	}
	for _, j := range remaining {
		if j.ID == done.ID || j.ID == broken.ID {
			t.Fatalf("job %s (%s) should have been cleared", j.ID, j.Status)
		}
	}
	if _, err := m.GetJob(keep.ID); err != nil {
		t.Fatalf("queued job disappeared: %v", err)
	}
	if _, err := m.GetJob(running.ID); err != nil {
		t.Fatalf("running job disappeared: %v", err)
	}
}

// makeJobWithStatus creates a job of the given type and moves it to the
// given status with an error message (when failing).
func makeJobWithStatus(t *testing.T, m *Manager, jobType JobType, status JobStatus, errMsg string) *Job {
	t.Helper()
	job, err := m.CreateJob(jobType, map[string]string{"video_id": "abc"})
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := m.UpdateJobStatus(job.ID, status, "10%", errMsg); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	return job
}

func TestRetryJobFailedDownload(t *testing.T) {
	m := newTestManager(t)

	job := makeJobWithStatus(t, m, TypeDownload, StatusFailed, "HTTP Error 503")

	got, err := m.RetryJob(job.ID)
	if err != nil {
		t.Fatalf("RetryJob: %v", err)
	}
	if got.Status != StatusQueued {
		t.Fatalf("status = %s, want queued", got.Status)
	}
	if got.Progress != "" {
		t.Fatalf("progress = %q, want cleared", got.Progress)
	}
	if got.Error != "" {
		t.Fatalf("error = %q, want cleared", got.Error)
	}
	if got.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", got.Attempts)
	}
	if got.NextRetryAt.Valid {
		t.Fatalf("next_retry_at = %v, want cleared", got.NextRetryAt.Time)
	}
	if !strings.Contains(got.Details, "abc") {
		t.Fatalf("details = %q, want kept", got.Details)
	}
	if got.Type != TypeDownload || got.ID != job.ID {
		t.Fatalf("job identity changed: %+v", got)
	}
}

func TestRetryJobStateRules(t *testing.T) {
	m := newTestManager(t)

	cases := []struct {
		name    string
		jobType JobType
		status  JobStatus
	}{
		{"unknown id", TypeDownload, StatusFailed},
		{"failed scan", TypeScan, StatusFailed},
		{"failed metadata", TypeMetadata, StatusFailed},
		{"queued download", TypeDownload, StatusQueued},
		{"running download", TypeDownload, StatusRunning},
		{"completed download", TypeDownload, StatusCompleted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var id string
			if tc.name != "unknown id" {
				id = makeJobWithStatus(t, m, tc.jobType, tc.status, "err").ID
			} else {
				id = "no-such-job"
			}
			_, err := m.RetryJob(id)
			var wantErr error
			if tc.name == "unknown id" {
				wantErr = ErrJobNotFound
			} else {
				wantErr = ErrNotRetryable
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("RetryJob err = %v, want %v", err, wantErr)
			}
		})
	}
}

func TestRetryJobIncrementsAttemptsAcrossRetries(t *testing.T) {
	m := newTestManager(t)

	job := makeJobWithStatus(t, m, TypeDownload, StatusFailed, "err 1")
	if _, err := m.RetryJob(job.ID); err != nil {
		t.Fatalf("first retry: %v", err)
	}
	// Simulate the retry failing again, then retry manually once more.
	if err := m.UpdateJobStatus(job.ID, StatusFailed, "", "err 2"); err != nil {
		t.Fatalf("UpdateJobStatus: %v", err)
	}
	got, err := m.RetryJob(job.ID)
	if err != nil {
		t.Fatalf("second retry: %v", err)
	}
	if got.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", got.Attempts)
	}
}

func TestScheduleRetry(t *testing.T) {
	m := newTestManager(t)

	job := makeJobWithStatus(t, m, TypeDownload, StatusFailed, "err")
	nextAt := time.Now().Add(30 * time.Second)

	if err := m.ScheduleRetry(job.ID, 1, nextAt, "retrying (1/2): err"); err != nil {
		t.Fatalf("ScheduleRetry: %v", err)
	}

	got, err := m.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != StatusQueued {
		t.Fatalf("status = %s, want queued", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", got.Attempts)
	}
	if got.Error != "retrying (1/2): err" {
		t.Fatalf("error = %q, want the retrying message", got.Error)
	}
	if !got.NextRetryAt.Valid {
		t.Fatal("next_retry_at not set")
	}
	if diff := got.NextRetryAt.Time.Sub(nextAt); diff < -time.Second || diff > time.Second {
		t.Fatalf("next_retry_at = %v, want ≈ %v", got.NextRetryAt.Time, nextAt)
	}
}

func TestRetryFailedDownloads(t *testing.T) {
	m := newTestManager(t)

	failedDownload := makeJobWithStatus(t, m, TypeDownload, StatusFailed, "a")
	failedDownload2 := makeJobWithStatus(t, m, TypeDownload, StatusFailed, "b")
	failedScan := makeJobWithStatus(t, m, TypeScan, StatusFailed, "c")
	completedDownload := makeJobWithStatus(t, m, TypeDownload, StatusCompleted, "")

	retried, err := m.RetryFailedDownloads()
	if err != nil {
		t.Fatalf("RetryFailedDownloads: %v", err)
	}
	if len(retried) != 2 {
		t.Fatalf("retried = %d jobs, want 2", len(retried))
	}
	for _, j := range retried {
		if j.ID != failedDownload.ID && j.ID != failedDownload2.ID {
			t.Fatalf("unexpected job %s retried", j.ID)
		}
		if j.Status != StatusQueued || j.Attempts != 1 {
			t.Fatalf("job %s = %+v, want queued with one attempt", j.ID, j)
		}
	}
	if scan, err := m.GetJob(failedScan.ID); err == nil && scan.Status == StatusQueued {
		t.Fatal("failed scan job was retried")
	}
	if done, err := m.GetJob(completedDownload.ID); err == nil && done.Status == StatusQueued {
		t.Fatal("completed download job was retried")
	}

	// Nothing left to retry.
	again, err := m.RetryFailedDownloads()
	if err != nil {
		t.Fatalf("second RetryFailedDownloads: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second pass retried %d jobs, want 0", len(again))
	}
}

func TestJobJSONRetryFields(t *testing.T) {
	m := newTestManager(t)

	fresh, err := m.CreateJob(TypeDownload, "x")
	if err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	// attempts defaults to 0, next_retry_at is null.
	b, err := json.Marshal(fresh)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round struct {
		Attempts    int    `json:"attempts"`
		NextRetryAt string `json:"next_retry_at"`
	}
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.Attempts != 0 {
		t.Fatalf("attempts = %d, want 0", round.Attempts)
	}

	// Scheduled retry: next_retry_at serialises as RFC 3339.
	nextAt := time.Date(2026, 11, 22, 12, 0, 30, 0, time.UTC)
	if err := m.ScheduleRetry(fresh.ID, 1, nextAt, "retrying (1/2): x"); err != nil {
		t.Fatalf("ScheduleRetry: %v", err)
	}
	got, err := m.GetJob(fresh.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	b, err = json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var withRetry struct {
		Attempts    int       `json:"attempts"`
		NextRetryAt time.Time `json:"next_retry_at"`
	}
	if err := json.Unmarshal(b, &withRetry); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if withRetry.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", withRetry.Attempts)
	}
	if withRetry.NextRetryAt.UTC() != nextAt {
		t.Fatalf("next_retry_at = %v, want %v", withRetry.NextRetryAt, nextAt)
	}
}

// TestNullTimeJSON covers the marshal/unmarshal contract of the nullable
// time field directly.
func TestNullTimeJSON(t *testing.T) {
	var n NullTime
	if b, err := json.Marshal(n); err != nil || string(b) != "null" {
		t.Fatalf("Marshal(NullTime{}) = %s, %v; want null", b, err)
	}

	v := NullTime{NullTime: sql.NullTime{Valid: true, Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back NullTime
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !back.Valid || !back.Time.Equal(v.Time) {
		t.Fatalf("round trip = %+v, want %v", back, v.Time)
	}

	var nulled NullTime
	if err := json.Unmarshal([]byte("null"), &nulled); err != nil {
		t.Fatalf("Unmarshal(null): %v", err)
	}
	if nulled.Valid {
		t.Fatal("null unmarshal must stay invalid")
	}
}
