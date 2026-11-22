package ytdlp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"yt-scraper/internal/jobs"
)

// sumsFor renders a one-entry SHA2-256SUMS file for the given content.
func sumsFor(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:]) + "  yt-dlp\n"
}

// waitFor polls cond until it is true or the test times out.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// newReleaseManager builds a manager and fake release server serving
// version with a valid checksum.
func newReleaseManager(t *testing.T, version string) (*Manager, *releaseState, *httptest.Server) {
	t.Helper()
	script := fakeBinary(version)
	st := &releaseState{tag: version, script: script, sums: sumsFor(script)}
	srv := newFakeReleaseServer(t, st)
	m := newTestManager(t, srv)
	return m, st, srv
}

func TestUpdateOnceInstallsVerifiedBinary(t *testing.T) {
	m, _, _ := newReleaseManager(t, "2025.1.15")

	res, err := m.UpdateOnce(context.Background(), false)
	if err != nil {
		t.Fatalf("UpdateOnce: %v", err)
	}
	if !strings.Contains(res, "2025.1.15") {
		t.Fatalf("result = %q, want the new version in it", res)
	}
	if !m.HasBinary() {
		t.Fatal("managed binary missing after install")
	}
	if got := m.Version(); got != "2025.1.15" {
		t.Fatalf("version = %q, want 2025.1.15", got)
	}
	if m.hasPrev() {
		t.Fatal("a first install must not leave a rollback candidate")
	}
	// The binary is executable.
	info, err := os.Stat(m.BinPath())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("binary mode = %v, want executable", info.Mode())
	}
	// last_result was stored.
	last, err := m.getSetting(keyLastResult)
	if err != nil {
		t.Fatalf("last_result: %v", err)
	}
	if !strings.Contains(last, "2025.1.15") {
		t.Fatalf("last_result = %q, want the version in it", last)
	}
}

func TestUpdateOnceNewerVersionKeepsRollback(t *testing.T) {
	m, st, _ := newReleaseManager(t, "2025.1.15")
	seedBinary(t, m, "2025.1.10")

	st.set("2025.1.15", fakeBinary("2025.1.15"), sumsFor(fakeBinary("2025.1.15")))
	if _, err := m.UpdateOnce(context.Background(), false); err != nil {
		t.Fatalf("UpdateOnce: %v", err)
	}
	if got := m.Version(); got != "2025.1.15" {
		t.Fatalf("version = %q, want 2025.1.15", got)
	}
	prev, err := os.ReadFile(m.prevPath())
	if err != nil {
		t.Fatalf("previous binary missing: %v", err)
	}
	if !strings.Contains(string(prev), "2025.1.10") {
		t.Fatalf("previous binary = %q, want it to hold 2025.1.10", prev)
	}
}

func TestUpdateOnceUpToDateSkipsDownload(t *testing.T) {
	m, st, _ := newReleaseManager(t, "2025.1.15")
	seedBinary(t, m, "2025.1.15")

	_, err := m.UpdateOnce(context.Background(), false)
	if !errors.Is(err, ErrUpToDate) {
		t.Fatalf("UpdateOnce = %v, want ErrUpToDate", err)
	}
	if hits := st.binDownloadHits(); hits != 0 {
		t.Fatalf("binary downloads = %d, want 0 when up to date", hits)
	}
	if got := m.Version(); got != "2025.1.15" {
		t.Fatalf("version = %q, want 2025.1.15 untouched", got)
	}
}

func TestUpdateOnceForceReinstallsSameVersion(t *testing.T) {
	m, st, _ := newReleaseManager(t, "2025.1.15")
	seedBinary(t, m, "2025.1.15")
	before := st.binDownloadHits()

	res, err := m.UpdateOnce(context.Background(), true)
	if err != nil {
		t.Fatalf("forced UpdateOnce: %v", err)
	}
	if !strings.Contains(res, "updated") {
		t.Fatalf("result = %q, want an update", res)
	}
	if hits := st.binDownloadHits(); hits != before+1 {
		t.Fatalf("binary downloads = %d, want %d", hits, before+1)
	}
}

func TestUpdateChecksumMismatchKeepsBinary(t *testing.T) {
	m, st, _ := newReleaseManager(t, "2025.1.15")
	seedBinary(t, m, "2025.1.10")

	// Serve a new binary with a deliberately wrong checksum.
	badScript := fakeBinary("2025.1.15")
	st.set("2025.1.15", badScript, "0000000000000000000000000000000000000000000000000000000000000000  yt-dlp\n")

	_, err := m.UpdateOnce(context.Background(), true)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("UpdateOnce = %v, want ErrChecksumMismatch", err)
	}
	if got := m.Version(); got != "2025.1.10" {
		t.Fatalf("version = %q, want the old 2025.1.10 kept", got)
	}
	// No temp files may be left behind.
	entries, err := os.ReadDir(m.binDir)
	if err != nil {
		t.Fatalf("read bin dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != binName {
			t.Fatalf("leftover %q in the bin dir after a failed install", e.Name())
		}
	}
}

func TestUpdateChecksumMissingEntry(t *testing.T) {
	m, st, _ := newReleaseManager(t, "2025.1.15")
	seedBinary(t, m, "2025.1.10")

	script := fakeBinary("2025.1.15")
	st.set("2025.1.15", script, sha256Sum("x")+"  yt-dlp.exe\n")

	_, err := m.UpdateOnce(context.Background(), true)
	if !errors.Is(err, ErrChecksumMissing) {
		t.Fatalf("UpdateOnce = %v, want ErrChecksumMissing", err)
	}
	if got := m.Version(); got != "2025.1.10" {
		t.Fatalf("version = %q, want the old binary kept", got)
	}
}

func sha256Sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestUpdateDownloadFailureKeepsBinary(t *testing.T) {
	m, st, _ := newReleaseManager(t, "2025.1.15")
	seedBinary(t, m, "2025.1.10")

	st.mu.Lock()
	st.status = http.StatusNotFound
	st.mu.Unlock()

	_, err := m.UpdateOnce(context.Background(), true)
	if err == nil {
		t.Fatal("UpdateOnce = nil, want a download error")
	}
	if got := m.Version(); got != "2025.1.10" {
		t.Fatalf("version = %q, want the old binary kept", got)
	}
}

func TestUpdateSmokeTestFailureKeepsBinary(t *testing.T) {
	m, st, _ := newReleaseManager(t, "2025.1.15")
	seedBinary(t, m, "2025.1.10")

	// A "binary" that prints nothing: the --version smoke test must reject
	// it before the swap.
	broken := "#!/bin/sh\nexit 0\n"
	st.set("2025.1.15", broken, sumsFor(broken))

	_, err := m.UpdateOnce(context.Background(), true)
	if err == nil {
		t.Fatal("UpdateOnce = nil, want a smoke-test failure")
	}
	if got := m.Version(); got != "2025.1.10" {
		t.Fatalf("version = %q, want the old binary kept", got)
	}
}

func TestRollback(t *testing.T) {
	m, st, _ := newReleaseManager(t, "2025.1.15")
	seedBinary(t, m, "2025.1.10")

	// No previous binary yet: rollback is refused.
	if err := m.Rollback(); !errors.Is(err, ErrNoRollback) {
		t.Fatalf("Rollback without prev = %v, want ErrNoRollback", err)
	}

	st.set("2025.1.15", fakeBinary("2025.1.15"), sumsFor(fakeBinary("2025.1.15")))
	if _, err := m.UpdateOnce(context.Background(), false); err != nil {
		t.Fatalf("UpdateOnce: %v", err)
	}
	if err := m.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if got := m.Version(); got != "2025.1.10" {
		t.Fatalf("version after rollback = %q, want 2025.1.10", got)
	}
	if m.hasPrev() {
		t.Fatal("rollback must consume the previous binary")
	}
	if err := m.Rollback(); !errors.Is(err, ErrNoRollback) {
		t.Fatalf("second Rollback = %v, want ErrNoRollback", err)
	}
}

// fakeGate records pause/resume calls and reports a configurable
// WaitForIdle outcome.
type fakeGate struct {
	mu      sync.Mutex
	waitErr error
	paused  bool
	resumed bool
}

func (g *fakeGate) Pause() {
	g.mu.Lock()
	g.paused = true
	g.mu.Unlock()
}

func (g *fakeGate) Resume() {
	g.mu.Lock()
	g.resumed = true
	g.mu.Unlock()
}

func (g *fakeGate) WaitForIdle(time.Duration) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.waitErr
}

func (g *fakeGate) state() (paused, resumed bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.paused, g.resumed
}

func TestUpdateQuiescesThroughGate(t *testing.T) {
	m, st, _ := newReleaseManager(t, "2025.1.15")
	seedBinary(t, m, "2025.1.10")

	g := &fakeGate{}
	m.gate = g
	m.idleTimeout = time.Second

	st.set("2025.1.15", fakeBinary("2025.1.15"), sumsFor(fakeBinary("2025.1.15")))
	if _, err := m.UpdateOnce(context.Background(), false); err != nil {
		t.Fatalf("UpdateOnce: %v", err)
	}
	paused, resumed := g.state()
	if !paused || !resumed {
		t.Fatalf("gate state = paused:%v resumed:%v, want both true", paused, resumed)
	}
}

func TestUpdateAbortsWhileGateBusy(t *testing.T) {
	m, st, _ := newReleaseManager(t, "2025.1.15")
	seedBinary(t, m, "2025.1.10")

	g := &fakeGate{waitErr: errors.New("downloads still running")}
	m.gate = g
	m.idleTimeout = time.Second

	st.set("2025.1.15", fakeBinary("2025.1.15"), sumsFor(fakeBinary("2025.1.15")))
	_, err := m.UpdateOnce(context.Background(), true)
	if err == nil || !strings.Contains(err.Error(), "wait for running downloads") {
		t.Fatalf("UpdateOnce = %v, want the busy-gate error", err)
	}
	if got := m.Version(); got != "2025.1.10" {
		t.Fatalf("version = %q, want the old binary kept", got)
	}
	// The dispatcher must have been resumed even though the swap aborted.
	if _, resumed := g.state(); !resumed {
		t.Fatal("gate was not resumed after an aborted swap")
	}
	entries, err := os.ReadDir(m.binDir)
	if err != nil {
		t.Fatalf("read bin dir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != binName {
			t.Fatalf("leftover %q after an aborted swap", e.Name())
		}
	}
}

// manualTicker drives the update loop by hand.
type manualTicker struct {
	c       chan time.Time
	stopped bool
}

func (m *manualTicker) Chan() <-chan time.Time { return m.c }
func (m *manualTicker) Reset(time.Duration)    {}
func (m *manualTicker) Stop()                  { m.stopped = true }

func TestLoopStartupInstallAutoUpdateAndCancel(t *testing.T) {
	m, st, _ := newReleaseManager(t, "2025.1.15")
	mt := &manualTicker{c: make(chan time.Time, 2)}
	m.newTicker = func(time.Duration) Ticker { return mt }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Loop(ctx)
		close(done)
	}()

	// 1. Startup install: no binary at boot, managed on.
	waitFor(t, m.HasBinary, "the startup install")
	if got := m.Version(); got != "2025.1.15" {
		t.Fatalf("version after startup = %q, want 2025.1.15", got)
	}

	// 2. Scheduled check with auto-update on picks up a newer release.
	st.set("2025.2.2", fakeBinary("2025.2.2"), sumsFor(fakeBinary("2025.2.2")))
	mt.c <- time.Now()
	waitFor(t, func() bool { return m.Version() == "2025.2.2" }, "the scheduled auto-update")

	// 3. Auto-update off: a newer release is left alone.
	if err := m.SetSettings(Settings{AutoUpdate: false, IntervalHours: 24, Channel: ChannelStable}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	st.set("2025.3.3", fakeBinary("2025.3.3"), sumsFor(fakeBinary("2025.3.3")))
	mt.c <- time.Now()
	time.Sleep(300 * time.Millisecond)
	if got := m.Version(); got != "2025.2.2" {
		t.Fatalf("version = %q, want 2025.2.2 (auto-update is off)", got)
	}

	// 4. Context cancel stops the loop.
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not return after context cancel")
	}
	if !mt.stopped {
		t.Fatal("the ticker was not stopped")
	}
}

func TestLoopWithoutManagedSkipsEverything(t *testing.T) {
	m, _, _ := newReleaseManager(t, "2025.1.15")
	m.managed = false

	mt := &manualTicker{c: make(chan time.Time, 1)}
	m.newTicker = func(time.Duration) Ticker { return mt }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Loop(ctx)
		close(done)
	}()
	mt.c <- time.Now()
	time.Sleep(200 * time.Millisecond)
	if m.HasBinary() {
		t.Fatal("an unmanaged manager must not install a binary")
	}
	cancel()
	<-done
}

func TestStartManualUpdate(t *testing.T) {
	m, _, _ := newReleaseManager(t, "2025.1.15")

	job, err := m.StartManualUpdate(context.Background(), false)
	if err != nil {
		t.Fatalf("StartManualUpdate: %v", err)
	}
	if job.Type != jobs.TypeYTDLPUpdate {
		t.Fatalf("job type = %s, want %s", job.Type, jobs.TypeYTDLPUpdate)
	}

	waitFor(t, func() bool {
		j, err := m.jm.GetJob(job.ID)
		return err == nil && (j.Status == jobs.StatusCompleted || j.Status == jobs.StatusFailed)
	}, "the manual update job to finish")
	j, err := m.jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if j.Status != jobs.StatusCompleted {
		t.Fatalf("job = %s (error: %s), want completed", j.Status, j.Error)
	}
	if !m.HasBinary() {
		t.Fatal("binary missing after a completed manual update")
	}

	// A second manual update without force is refused: already up to date.
	if _, err := m.StartManualUpdate(context.Background(), false); !errors.Is(err, ErrUpToDate) {
		t.Fatalf("second StartManualUpdate = %v, want ErrUpToDate", err)
	}

	// While an update is running, even a forced one is refused.
	m.beginUpdate()
	if _, err := m.StartManualUpdate(context.Background(), true); !errors.Is(err, ErrUpdateRunning) {
		t.Fatalf("StartManualUpdate while busy = %v, want ErrUpdateRunning", err)
	}
	m.endUpdate()
}

func TestStartManualUpdateFailureMarksJobFailed(t *testing.T) {
	m, st, _ := newReleaseManager(t, "2025.1.15")

	// Break the checksum so the update must fail.
	badScript := fakeBinary("2025.1.15")
	st.set("2025.1.15", badScript, "0000000000000000000000000000000000000000000000000000000000000000  yt-dlp\n")

	job, err := m.StartManualUpdate(context.Background(), false)
	if err != nil {
		t.Fatalf("StartManualUpdate: %v", err)
	}
	waitFor(t, func() bool {
		j, err := m.jm.GetJob(job.ID)
		return err == nil && j.Status == jobs.StatusFailed
	}, "the manual update job to fail")
	j, err := m.jm.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if !strings.Contains(j.Error, "checksum") {
		t.Fatalf("job error = %q, want the checksum failure in it", j.Error)
	}
	if m.HasBinary() {
		t.Fatal("a failed manual update must not leave a broken binary behind")
	}
}
