package jobs

import (
	"path/filepath"
	"strings"
	"testing"

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
