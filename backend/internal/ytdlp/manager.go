// Package ytdlp manages the JellyIDE-owned copy of the official platform
// independent yt-dlp zipapp: checksum-verified downloads from a fixed
// https-only GitHub release allowlist, an atomic binary swap with
// rollback, the settings that drive it and the auto-update loop.
//
// A failure at any step leaves the previous binary in place, so the
// updater can never break downloads.
package ytdlp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"yt-scraper/internal/db"
	"yt-scraper/internal/jobs"
)

// Update channels and the settings-table keys.
const (
	ChannelStable  = "stable"
	ChannelNightly = "nightly"

	defaultAutoUpdate    = true
	defaultIntervalHours = 24
	minIntervalHours     = 6
	maxIntervalHours     = 168

	// smokeTimeout bounds the --version smoke test of a freshly
	// downloaded binary.
	smokeTimeout = 10 * time.Second
	// versionTimeout bounds --version calls on the binary in use.
	versionTimeout = 10 * time.Second

	binName  = "yt-dlp"
	prevName = "yt-dlp.prev"

	keyAutoUpdate    = "ytdlp_auto_update"
	keyIntervalHours = "ytdlp_update_interval_hours"
	keyChannel       = "ytdlp_channel"
	keyLastCheck     = "ytdlp_last_check"
	keyLastResult    = "ytdlp_last_result"
	keyLatestVersion = "ytdlp_latest"
)

// maxDownloadBytes caps any download (binary or checksum file).
const maxDownloadBytes = 100 << 20 // 100 MiB

// Settings are the admin-configurable updater options, stored in the
// settings table.
type Settings struct {
	AutoUpdate    bool
	IntervalHours int
	Channel       string
}

// ValidateSettings reports whether s is an admissible settings
// combination (interval 6–168 hours, channel stable or nightly).
func ValidateSettings(s Settings) error {
	if s.IntervalHours < minIntervalHours || s.IntervalHours > maxIntervalHours {
		return fmt.Errorf("interval_hours must be between %d and %d", minIntervalHours, maxIntervalHours)
	}
	if s.Channel != ChannelStable && s.Channel != ChannelNightly {
		return fmt.Errorf("channel must be %q or %q", ChannelStable, ChannelNightly)
	}
	return nil
}

// Gate serializes a binary swap with the download queue: Pause stops new
// downloads from starting, WaitForIdle waits for the running ones, Resume
// restarts the dispatcher.
type Gate interface {
	Pause()
	Resume()
	WaitForIdle(timeout time.Duration) error
}

// Ticker is the subset of time.Ticker the update loop needs; tests inject
// a manual implementation.
type Ticker interface {
	Chan() <-chan time.Time
	Reset(d time.Duration)
	Stop()
}

type realTicker struct{ *time.Ticker }

func (t *realTicker) Chan() <-chan time.Time { return t.Ticker.C }

// channelURLs are the fixed, https-only sources for one channel.
type channelURLs struct {
	binary string
	sums   string
	api    string
}

var defaultChannels = map[string]channelURLs{
	ChannelStable: {
		binary: "https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp",
		sums:   "https://github.com/yt-dlp/yt-dlp/releases/latest/download/SHA2-256SUMS",
		api:    "https://api.github.com/repos/yt-dlp/yt-dlp/releases/latest",
	},
	ChannelNightly: {
		binary: "https://github.com/yt-dlp/yt-dlp-nightly-builds/releases/latest/download/yt-dlp",
		sums:   "https://github.com/yt-dlp/yt-dlp-nightly-builds/releases/latest/download/SHA2-256SUMS",
		api:    "https://api.github.com/repos/yt-dlp/yt-dlp-nightly-builds/releases/latest",
	},
}

// Manager owns the managed yt-dlp binary and its settings.
type Manager struct {
	db      *db.DB
	jm      *jobs.Manager
	binDir  string
	managed bool

	client   *http.Client
	channels map[string]channelURLs
	gate     Gate
	// idleTimeout bounds how long a swap waits for running downloads.
	idleTimeout time.Duration
	now         func() time.Time
	newTicker   func(d time.Duration) Ticker

	mu       sync.Mutex
	updating bool
}

// NewManager builds a Manager for the binary in binDir. When managed is
// false the manager only reports the system binary on PATH and never
// updates.
func NewManager(database *db.DB, jm *jobs.Manager, binDir string, managed bool) *Manager {
	channels := make(map[string]channelURLs, len(defaultChannels))
	for k, v := range defaultChannels {
		channels[k] = v
	}
	return &Manager{
		db:          database,
		jm:          jm,
		binDir:      binDir,
		managed:     managed,
		client:      newHTTPClient(),
		channels:    channels,
		idleTimeout: 10 * time.Minute,
		now:         time.Now,
		newTicker:   func(d time.Duration) Ticker { return &realTicker{time.NewTicker(d)} },
	}
}

// SetGate wires the download queue so a binary swap waits for running
// downloads.
func (m *Manager) SetGate(g Gate) { m.gate = g }

// BinPath returns the managed binary path.
func (m *Manager) BinPath() string { return filepath.Join(m.binDir, binName) }

func (m *Manager) prevPath() string { return filepath.Join(m.binDir, prevName) }

// HasBinary reports whether the managed binary is in place.
func (m *Manager) HasBinary() bool {
	_, err := os.Stat(m.BinPath())
	return err == nil
}

// hasPrev reports whether a rollback candidate (yt-dlp.prev) exists.
func (m *Manager) hasPrev() bool {
	_, err := os.Stat(m.prevPath())
	return err == nil
}

// Path implements download.Resolver: the managed binary when managed and
// present, otherwise yt-dlp on PATH. It is consulted per job, so an update
// takes effect without a restart.
func (m *Manager) Path() string {
	if m.managed && m.HasBinary() {
		return m.BinPath()
	}
	return binName
}

// ParseManagedEnv interprets the YTDLP_MANAGED variable; anything that is
// not an explicit negative keeps managed mode on (the default).
func ParseManagedEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// Version runs <binary> --version and returns the trimmed output; it
// returns "" when the binary is missing or fails.
func (m *Manager) Version() string {
	ctx, cancel := context.WithTimeout(context.Background(), versionTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, m.Path(), "--version").Output() //nolint:gosec // G204 - m.Path() is the fixed managed binary or "yt-dlp"
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Status is the JSON shape of GET /api/admin/ytdlp.
type Status struct {
	Version         string  `json:"version"`
	Path            string  `json:"path"`
	Managed         bool    `json:"managed"`
	Channel         string  `json:"channel"`
	LatestVersion   *string `json:"latest_version"`
	UpdateAvailable bool    `json:"update_available"`
	LastCheck       string  `json:"last_check"`
	LastResult      string  `json:"last_result"`
	AutoUpdate      bool    `json:"auto_update"`
	IntervalHours   int     `json:"interval_hours"`
	Updating        bool    `json:"updating"`
	HasRollback     bool    `json:"has_rollback"`
}

// Status assembles the current state of the updater.
func (m *Manager) Status() (Status, error) {
	st, err := m.GetSettings()
	if err != nil {
		return Status{}, err
	}
	latest, err := m.getSetting(keyLatestVersion)
	if err != nil {
		return Status{}, err
	}
	lastCheck, err := m.getSetting(keyLastCheck)
	if err != nil {
		return Status{}, err
	}
	lastResult, err := m.getSetting(keyLastResult)
	if err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	updating := m.updating
	m.mu.Unlock()

	s := Status{
		Version:       m.Version(),
		Path:          m.Path(),
		Managed:       m.managed,
		Channel:       st.Channel,
		LastCheck:     lastCheck,
		LastResult:    lastResult,
		AutoUpdate:    st.AutoUpdate,
		IntervalHours: st.IntervalHours,
		Updating:      updating,
		HasRollback:   m.hasPrev(),
	}
	if latest != "" {
		s.LatestVersion = &latest
		s.UpdateAvailable = IsNewer(s.Version, latest)
	}
	return s, nil
}

// ---------------------------------------------------------------------
// Settings (settings table)
// ---------------------------------------------------------------------

func (m *Manager) getSetting(key string) (string, error) {
	var v string
	err := m.db.Get(&v, `SELECT value FROM settings WHERE key = ?`, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (m *Manager) setSetting(key, value string) error {
	_, err := m.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

// GetSettings returns the effective settings, applying the defaults for
// missing rows.
func (m *Manager) GetSettings() (Settings, error) {
	s := Settings{
		AutoUpdate:    defaultAutoUpdate,
		IntervalHours: defaultIntervalHours,
		Channel:       ChannelStable,
	}
	if v, err := m.getSetting(keyAutoUpdate); err != nil {
		return s, err
	} else if v == "false" {
		s.AutoUpdate = false
	} else if v == "true" {
		s.AutoUpdate = true
	}
	if v, err := m.getSetting(keyIntervalHours); err != nil {
		return s, err
	} else if v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return s, fmt.Errorf("corrupt %s setting: %w", keyIntervalHours, err)
		}
		s.IntervalHours = n
	}
	if v, err := m.getSetting(keyChannel); err != nil {
		return s, err
	} else if v != "" {
		s.Channel = v
	}
	return s, nil
}

// SetSettings validates and persists the three admin options.
func (m *Manager) SetSettings(s Settings) error {
	if err := ValidateSettings(s); err != nil {
		return err
	}
	if err := m.setSetting(keyAutoUpdate, strconv.FormatBool(s.AutoUpdate)); err != nil {
		return err
	}
	if err := m.setSetting(keyIntervalHours, strconv.Itoa(s.IntervalHours)); err != nil {
		return err
	}
	return m.setSetting(keyChannel, s.Channel)
}

// ---------------------------------------------------------------------
// Update slot and result bookkeeping
// ---------------------------------------------------------------------

// beginUpdate claims the single update slot; it returns false when an
// update (manual or automatic) is already running.
func (m *Manager) beginUpdate() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.updating {
		return false
	}
	m.updating = true
	return true
}

func (m *Manager) endUpdate() {
	m.mu.Lock()
	m.updating = false
	m.mu.Unlock()
}

// storeResult records the last check time, the latest known release and a
// short human-readable result in the settings table.
func (m *Manager) storeResult(latest, result string) {
	_ = m.setSetting(keyLastCheck, m.now().UTC().Format(time.RFC3339))
	if latest != "" {
		_ = m.setSetting(keyLatestVersion, latest)
	}
	_ = m.setSetting(keyLastResult, result)
}

// ---------------------------------------------------------------------
// Auto-update loop
// ---------------------------------------------------------------------

// interval returns the current check interval (with a sane lower bound).
func (m *Manager) interval() time.Duration {
	st, err := m.GetSettings()
	if err != nil || st.IntervalHours < 1 {
		st.IntervalHours = defaultIntervalHours
	}
	return time.Duration(st.IntervalHours) * time.Hour
}

// Loop runs the auto-update lifecycle: an initial install when the
// managed binary is missing, then one check per configured interval. A
// failure never breaks downloads because the previous binary stays in
// place.
func (m *Manager) Loop(ctx context.Context) {
	if m.managed && !m.HasBinary() {
		m.runAutoUpdate(ctx, "startup install", true)
	}
	ticker := m.newTicker(m.interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.Chan():
			m.runAutoUpdate(ctx, "scheduled check", false)
			ticker.Reset(m.interval())
		}
	}
}

// runAutoUpdate performs one automatic update cycle, logging the outcome.
// The result is persisted by UpdateOnce itself.
func (m *Manager) runAutoUpdate(ctx context.Context, reason string, force bool) {
	if !m.managed {
		return
	}
	st, err := m.GetSettings()
	if err != nil {
		log.Printf("[ytdlp] %s: read settings: %v", reason, err)
		return
	}
	// Scheduled ticks honour the auto-update toggle; the forced startup
	// install and manual updates do not.
	if !force && !st.AutoUpdate {
		return
	}
	if !m.beginUpdate() {
		log.Printf("[ytdlp] %s: an update is already running, skipping", reason)
		return
	}
	defer m.endUpdate()

	res, err := m.UpdateOnce(ctx, force)
	if err != nil && !errors.Is(err, ErrUpToDate) {
		log.Printf("[ytdlp] %s failed: %v", reason, err)
		return
	}
	log.Printf("[ytdlp] %s: %s", reason, res)
}
