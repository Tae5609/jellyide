package metadata

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"yt-scraper/internal/db"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

// stubTagWriter records calls instead of invoking Python. readByPath
// configures the apply-time re-read: a missing path returns an error,
// which the service treats as "cannot verify" and proceeds (the tag
// write would fail on an unreadable file anyway).
type stubTagWriter struct {
	mu            sync.Mutex
	writeErr      error
	readErr       error
	embedErr      error
	readByPath    map[string]map[string]interface{}
	extractByPath map[string][]byte // path -> embedded cover bytes; missing = none

	writeCalls []stubWriteCall
	embedCalls []string
}

type stubWriteCall struct {
	Path   string
	Fields map[string]interface{}
}

// setReadTags configures what ReadFields returns for path.
func (s *stubTagWriter) setReadTags(path string, fields map[string]interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readByPath == nil {
		s.readByPath = map[string]map[string]interface{}{}
	}
	s.readByPath[path] = fields
}

func (s *stubTagWriter) ReadFields(path string) (map[string]interface{}, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return nil, s.readErr
	}
	fields, ok := s.readByPath[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	copied := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	return copied, nil
}

func (s *stubTagWriter) WriteFields(path string, fields map[string]interface{}) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := make(map[string]interface{}, len(fields))
	for k, v := range fields {
		copied[k] = v
	}
	s.writeCalls = append(s.writeCalls, stubWriteCall{Path: path, Fields: copied})
	return s.writeErr
}

func (s *stubTagWriter) EmbedCover(path, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.embedCalls = append(s.embedCalls, path)
	return s.embedErr
}

func (s *stubTagWriter) ExtractCover(path, outPath string) (bool, error) {
	s.mu.Lock()
	data, ok := s.extractByPath[path]
	s.mu.Unlock()
	if !ok {
		return false, nil
	}
	if err := os.WriteFile(outPath, data, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// newJobService builds a Service backed by a temp-dir database, a jobs
// manager and the stub tag writer.
func newJobService(t *testing.T) (*Service, *library.Service, *jobs.Manager, *stubTagWriter, string) {
	t.Helper()
	dir := t.TempDir()
	database, err := db.NewDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	lib := library.NewService(database, dir)
	jm := jobs.NewManager(database)
	stub := &stubTagWriter{extractByPath: map[string][]byte{}}
	svc := NewServiceWith(Deps{DB: database, Library: lib, Jobs: jm, Tags: stub})
	return svc, lib, jm, stub, dir
}

// waitForJob polls the job until it has the wanted status.
func waitForJob(t *testing.T, jm *jobs.Manager, jobID string, want jobs.JobStatus) *jobs.Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := jm.GetJob(jobID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if job.Status == want {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s in time", jobID, want)
	return nil
}
