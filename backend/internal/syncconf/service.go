// Package syncconf implements the T16 user-chosen resolution flows:
// resolving, dismissing and listing sync conflicts, relinking missing
// tracks, trashed-file restore and purge, and the sync_conflict_policy
// admin setting. It sits on the library accessors; the jobs package is
// used as-is (job creation plus a details merge, the T17 pattern).
package syncconf

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"

	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

// Resolution choices.
const (
	ChoiceDB     = "db"     // rewrite the file tags from the DB
	ChoiceFile   = "file"   // update the DB from the file
	ChoiceCustom = "custom" // write the supplied value to both
)

// ErrConflictClosed is a resolve on a conflict that is no longer open.
var ErrConflictClosed = errors.New("conflict is no longer open")

// ErrNotMissing is a relink/delete of a track that is not in the missing
// state.
var ErrNotMissing = errors.New("track is not missing")

// ErrInvalidResolution is a bad resolve payload (unknown choice, missing
// value or empty list).
var ErrInvalidResolution = errors.New("invalid resolution")

// ErrNoCandidate is a relink to a path that is not a matching candidate.
var ErrNoCandidate = errors.New("path is not a relink candidate for this track")

// TagWriter writes audio file tags (the db / custom resolution choices).
type TagWriter interface {
	WriteFields(path string, fields map[string]interface{}) error
}

// Service resolves sync conflicts and manages missing tracks and the
// trash.
type Service struct {
	lib  *library.Service
	jm   *jobs.Manager
	tags TagWriter
}

// NewService builds a syncconf Service. tags may be nil only in tests
// that never resolve with the db or custom choice.
func NewService(lib *library.Service, jm *jobs.Manager, tags TagWriter) *Service {
	return &Service{lib: lib, jm: jm, tags: tags}
}

// Policy returns the active sync conflict policy.
func (s *Service) Policy() string { return s.lib.SyncPolicy() }

// SetPolicy validates and stores the policy (admin endpoint).
func (s *Service) SetPolicy(policy string) error {
	return s.lib.SetSyncPolicy(policy)
}

// Summary is the sidebar badge payload of GET /api/sync/summary.
type Summary struct {
	OpenConflicts int `json:"open_conflicts"`
	MissingTracks int `json:"missing_tracks"`
}

// Summary counts open conflicts and missing tracks.
func (s *Service) Summary() (Summary, error) {
	open, err := s.lib.CountOpenConflicts("")
	if err != nil {
		return Summary{}, err
	}
	missing, err := s.lib.ListMissing()
	if err != nil {
		return Summary{}, err
	}
	return Summary{OpenConflicts: open, MissingTracks: len(missing)}, nil
}

// Conflicts lists conflict rows (default status "open"), optionally per
// album, grouped by track.
func (s *Service) Conflicts(status, albumID string) ([]library.ConflictTrack, error) {
	if status == "" {
		status = library.ConflictStatusOpen
	}
	return s.lib.ListConflicts(status, albumID)
}

// Resolution is one entry of POST /api/sync/conflicts/resolve.
type Resolution struct {
	ConflictID string `json:"conflict_id"`
	Choice     string `json:"choice"`
	Value      string `json:"value"` // required for custom
}

// validateResolution checks one resolution entry.
func validateResolution(r Resolution) error {
	switch r.Choice {
	case ChoiceDB, ChoiceFile:
	case ChoiceCustom:
		if r.Value == "" {
			return fmt.Errorf("%w: custom choice for conflict %s needs a value", ErrInvalidResolution, r.ConflictID)
		}
	default:
		return fmt.Errorf("%w: invalid choice %q (want db, file or custom)", ErrInvalidResolution, r.Choice)
	}
	if r.ConflictID == "" {
		return fmt.Errorf("%w: conflict_id is required", ErrInvalidResolution)
	}
	return nil
}

// conflictPlan is one conflict row with its chosen resolution.
type conflictPlan struct {
	conflict library.Conflict
	choice   string
	value    string
}

// Resolve applies the requested resolutions through a metadata job (so
// the outcome is recorded in the history and can be reverted through the
// normal change-set flow) and returns the job id. db rewrites the file
// tags from the DB, file updates the DB from the file, custom writes the
// supplied value to both.
func (s *Service) Resolve(resolutions []Resolution, resolvedBy string) (string, error) {
	if len(resolutions) == 0 {
		return "", fmt.Errorf("%w: resolutions must not be empty", ErrInvalidResolution)
	}
	byTrack := map[string][]conflictPlan{}
	trackOrder := []string{}
	for _, r := range resolutions {
		if err := validateResolution(r); err != nil {
			return "", err
		}
		c, err := s.lib.GetConflict(r.ConflictID)
		if err != nil {
			return "", fmt.Errorf("conflict not found: %w", err)
		}
		if c.Status != library.ConflictStatusOpen {
			return "", ErrConflictClosed
		}
		if _, ok := byTrack[c.TrackID]; !ok {
			trackOrder = append(trackOrder, c.TrackID)
		}
		byTrack[c.TrackID] = append(byTrack[c.TrackID], conflictPlan{conflict: *c, choice: r.Choice, value: r.Value})
	}

	changeSetID := uuid.New().String()
	job, err := s.jm.CreateJob(jobs.TypeMetadata, map[string]string{
		"action":        "resolve_conflicts",
		"change_set_id": changeSetID,
		"resolutions":   strconv.Itoa(len(resolutions)),
	})
	if err != nil {
		return "", err
	}

	plans := map[string][]conflictPlan{}
	for _, trackID := range trackOrder {
		plans[trackID] = byTrack[trackID]
	}
	go s.runResolveJob(job.ID, changeSetID, plans, resolvedBy)
	return job.ID, nil
}

// runResolveJob is the resolve job body.
func (s *Service) runResolveJob(jobID, changeSetID string, plans map[string][]conflictPlan, resolvedBy string) {
	if err := s.jm.UpdateJobStatus(jobID, jobs.StatusRunning, "resolving", ""); err != nil {
		log.Printf("syncconf: update job %s: %v", jobID, err)
	}
	results := []interface{}{}
	for trackID, trackPlans := range plans {
		result := s.resolveTrack(trackID, trackPlans, changeSetID, resolvedBy)
		results = append(results, result)
	}
	s.appendJobResults(jobID, results)
	if err := s.jm.UpdateJobStatus(jobID, jobs.StatusCompleted, "100%", ""); err != nil {
		log.Printf("syncconf: update job %s: %v", jobID, err)
	}
}

// resolveTrack applies every resolution of one track and returns the
// per-track job result.
func (s *Service) resolveTrack(trackID string, trackPlans []conflictPlan, changeSetID, resolvedBy string) map[string]interface{} {
	result := map[string]interface{}{"track_id": trackID}
	errs := []string{}
	resolvedCount := 0
	track, err := s.lib.GetTrack(trackID)
	if err != nil {
		return map[string]interface{}{"track_id": trackID, "reason": "track_missing"}
	}
	for _, plan := range trackPlans {
		c := plan.conflict
		var applyErr error
		switch plan.choice {
		case ChoiceDB:
			// The file must carry the DB value.
			if s.tags == nil {
				applyErr = errors.New("no tag writer available")
			} else {
				dbValue, derr := s.dbFieldValue(track, c.Field)
				if derr != nil {
					applyErr = derr
				} else {
					applyErr = s.tags.WriteFields(track.Path, map[string]interface{}{c.Field: dbValue})
				}
			}
			if applyErr == nil {
				s.lib.RecordChange(trackID, c.Field,
					textOf(c.FileValue), textOf(c.DBValue), changeSetID)
			}
		case ChoiceFile:
			// The DB must carry the file value (the file already has
			// it, so only the row is updated — the normal edit path
			// semantics: one history row, version bump).
			applyErr = s.lib.ApplyFieldText(track, c.Field, textOf(c.FileValue))
			if applyErr == nil {
				s.lib.RecordChange(trackID, c.Field,
					textOf(c.DBValue), textOf(c.FileValue), changeSetID)
			}
		case ChoiceCustom:
			if s.tags != nil {
				applyErr = s.tags.WriteFields(track.Path, map[string]interface{}{c.Field: plan.value})
			}
			if applyErr == nil {
				applyErr = s.lib.ApplyFieldText(track, c.Field, plan.value)
			}
			if applyErr == nil {
				s.lib.RecordChange(trackID, c.Field,
					textOf(c.DBValue), plan.value, changeSetID)
			}
		}
		if applyErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", c.Field, applyErr))
			// The conflict stays open for another attempt.
			continue
		}
		if err := s.lib.MarkConflictStatus(c.ID, library.ConflictStatusResolved, plan.choice, resolvedBy); err != nil {
			log.Printf("syncconf: mark %s resolved: %v", c.ID, err)
		}
		resolvedCount++
	}
	result["resolved"] = resolvedCount
	if len(errs) > 0 {
		result["errors"] = errs
	}

	// The divergence is settled: refresh the fingerprint and clear the
	// conflict state. A failed field keeps the state (and is still
	// visible through its open conflict row).
	if len(errs) == 0 {
		s.finishTrackSync(trackID)
	}
	return result
}

// finishTrackSync re-stats the file, stores the fresh fingerprint (the
// DB values, which both sides now carry) and returns the track to ok.
func (s *Service) finishTrackSync(trackID string) {
	track, err := s.lib.GetTrack(trackID)
	if err != nil {
		return
	}
	st, err := os.Stat(track.Path)
	if err != nil {
		// The file is gone (e.g. a 'file' resolution on a file that was
		// deleted meanwhile): mark it missing rather than in sync.
		if merr := s.lib.MarkMissing(trackID); merr != nil {
			log.Printf("syncconf: mark %s missing: %v", trackID, merr)
		}
		return
	}
	hash := library.TagsHash(s.lib.RowTagValues(track))
	if err := s.lib.SetSyncState(trackID, st.Size(), st.ModTime().Unix(), hash, library.NowSQL(), library.TrackStateOK, false); err != nil {
		log.Printf("syncconf: set sync state for %s: %v", trackID, err)
	}
}

// dbFieldValue is the typed DB value of a field for a file tag write
// (unset values map to nil, which clears the tag).
func (s *Service) dbFieldValue(track *library.Track, field string) (interface{}, error) {
	deref := func(p *string) interface{} {
		if p == nil {
			return nil
		}
		return *p
	}
	derefInt := func(p *int) interface{} {
		if p == nil {
			return nil
		}
		return *p
	}
	switch field {
	case "title":
		return track.Title, nil
	case "album":
		return s.lib.RowTagValues(track)["album"], nil
	case "artist":
		return deref(track.Artist), nil
	case "album_artist":
		return deref(track.AlbumArtist), nil
	case "genre":
		return deref(track.Genre), nil
	case "composer":
		return deref(track.Composer), nil
	case "comment":
		return deref(track.Comment), nil
	case "track_number":
		return derefInt(track.TrackNumber), nil
	case "disc_number":
		return derefInt(track.DiscNumber), nil
	case "year":
		return derefInt(track.Year), nil
	default:
		return nil, fmt.Errorf("unknown field %q", field)
	}
}

// ResolveAll resolves every open conflict (optionally restricted to one
// album) with one choice (db or file; custom has no global value).
func (s *Service) ResolveAll(albumID, choice, resolvedBy string) (string, error) {
	if choice != ChoiceDB && choice != ChoiceFile {
		return "", fmt.Errorf("%w: resolve-all choice must be db or file, got %q", ErrInvalidResolution, choice)
	}
	groups, err := s.lib.ListConflicts(library.ConflictStatusOpen, albumID)
	if err != nil {
		return "", err
	}
	resolutions := []Resolution{}
	for _, g := range groups {
		for _, f := range g.Fields {
			resolutions = append(resolutions, Resolution{ConflictID: f.ID, Choice: choice})
		}
	}
	if len(resolutions) == 0 {
		return "", fmt.Errorf("%w: no open conflicts to resolve", ErrInvalidResolution)
	}
	return s.Resolve(resolutions, resolvedBy)
}

// Dismiss marks one conflict dismissed (the divergence is acknowledged
// but left in place).
func (s *Service) Dismiss(conflictID, resolvedBy string) (*library.Conflict, error) {
	c, err := s.lib.GetConflict(conflictID)
	if err != nil {
		return nil, err
	}
	if c.Status != library.ConflictStatusOpen {
		return nil, ErrConflictClosed
	}
	if err := s.lib.MarkConflictStatus(conflictID, library.ConflictStatusDismissed, "", resolvedBy); err != nil {
		return nil, err
	}
	return s.lib.GetConflict(conflictID)
}

// RelinkCandidate is one file the missing track could be re-pointed at.
type RelinkCandidate struct {
	TrackID  string `json:"track_id"`
	Path     string `json:"path"`
	Title    string `json:"title"`
	Duration int    `json:"duration"`
}

// MissingTrack is a missing row with its relink candidates.
type MissingTrack struct {
	TrackID    string            `json:"track_id"`
	AlbumID    string            `json:"album_id"`
	Title      string            `json:"title"`
	Artist     string            `json:"artist"`
	Album      string            `json:"album"`
	Path       string            `json:"path"`
	Duration   int               `json:"duration"`
	Size       int64             `json:"size"`
	Candidates []RelinkCandidate `json:"candidates"`
}

// Missing lists the missing tracks with their relink candidates: rows
// whose file still exists and whose stored fingerprint matches (same
// size, duration within 1 s and the same tags hash).
func (s *Service) Missing() ([]MissingTrack, error) {
	missing, err := s.lib.ListMissing()
	if err != nil {
		return nil, err
	}
	all, err := s.lib.ListTracks()
	if err != nil {
		return nil, err
	}
	out := make([]MissingTrack, 0, len(missing))
	for _, m := range missing {
		album, _ := s.lib.GetAlbum(m.AlbumID)
		size := int64(0)
		if m.FileSize != nil {
			size = *m.FileSize
		}
		entry := MissingTrack{
			TrackID:    m.ID,
			AlbumID:    m.AlbumID,
			Title:      m.Title,
			Artist:     strFrom(m.Artist),
			Album:      album.Title,
			Path:       m.Path,
			Duration:   m.Duration,
			Size:       size,
			Candidates: []RelinkCandidate{},
		}
		for _, t := range all {
			if t.ID == m.ID || t.State == library.TrackStateMissing {
				continue
			}
			if m.FileSize == nil || t.FileSize == nil || *m.FileSize != *t.FileSize {
				continue
			}
			dd := t.Duration - m.Duration
			if dd < 0 {
				dd = -dd
			}
			if dd > 1 {
				continue
			}
			if m.TagsHash == nil || t.TagsHash == nil || *m.TagsHash != *t.TagsHash {
				continue
			}
			if _, err := os.Stat(t.Path); err != nil {
				continue
			}
			entry.Candidates = append(entry.Candidates, RelinkCandidate{
				TrackID:  t.ID,
				Path:     t.Path,
				Title:    t.Title,
				Duration: t.Duration,
			})
		}
		out = append(out, entry)
	}
	return out, nil
}

// Relink re-points a missing track at the candidate's file: the id and
// the history rows are kept, the duplicate candidate row is removed (its
// history goes with it), and the fingerprint is adopted from the file.
func (s *Service) Relink(trackID, newPath string) (*library.Track, error) {
	track, err := s.lib.GetTrack(trackID)
	if err != nil {
		return nil, err
	}
	if track.State != library.TrackStateMissing {
		return nil, ErrNotMissing
	}
	cand, err := s.lib.GetTrackByPath(newPath)
	if err != nil {
		return nil, ErrNoCandidate
	}
	if track.FileSize == nil || cand.FileSize == nil || *track.FileSize != *cand.FileSize {
		return nil, ErrNoCandidate
	}
	dd := cand.Duration - track.Duration
	if dd < 0 {
		dd = -dd
	}
	if dd > 1 {
		return nil, ErrNoCandidate
	}
	if track.TagsHash == nil || cand.TagsHash == nil || *track.TagsHash != *cand.TagsHash {
		return nil, ErrNoCandidate
	}

	// Remove the duplicate row the scan created for the same file. Its
	// history rows must go first on this base (T12 adds the FK cascade);
	// the open conflict rows cascade via their FK.
	if err := s.lib.DeleteTrackHistory(cand.ID); err != nil {
		return nil, fmt.Errorf("remove candidate history: %w", err)
	}
	if err := s.lib.DeleteTrack(cand.ID); err != nil {
		return nil, fmt.Errorf("remove candidate row: %w", err)
	}
	if err := s.lib.RepointTrack(trackID, newPath, *cand.FileSize, candMtime(cand), *cand.TagsHash); err != nil {
		return nil, err
	}
	updated, err := s.lib.GetTrack(trackID)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func candMtime(t *library.Track) int64 {
	if t.FileMtime == nil {
		return 0
	}
	return *t.FileMtime
}

func strFrom(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func textOf(p *string) string {
	return strFrom(p)
}

// DeleteMissing removes a missing row (the file is gone; the history
// rows are removed with it — T12's FK cascade makes the explicit delete
// redundant but harmless).
func (s *Service) DeleteMissing(trackID string) error {
	track, err := s.lib.GetTrack(trackID)
	if err != nil {
		return err
	}
	if track.State != library.TrackStateMissing {
		return ErrNotMissing
	}
	if err := s.lib.DeleteTrackHistory(trackID); err != nil {
		return err
	}
	return s.lib.DeleteTrack(trackID)
}

// Trash returns all trash entries, oldest first.
func (s *Service) Trash() ([]library.TrashEntry, error) {
	return s.lib.ListTrash()
}

// Restore moves a trashed file back to its original path.
func (s *Service) Restore(id string) error {
	return s.lib.RestoreTrash(id)
}

// TrashRetention is how long displaced files stay restorable.
const TrashRetention = 30 * 24 * time.Hour

// Purge drops trash entries older than the retention (file and row).
// Called once at startup.
func (s *Service) Purge() error {
	return s.lib.PurgeTrash(TrashRetention)
}

// appendJobResults merges the per-track results into the job's details
// JSON (the T17 pattern; the jobs package itself is untouched).
func (s *Service) appendJobResults(jobID string, results []interface{}) {
	job, err := s.jm.GetJob(jobID)
	if err != nil {
		log.Printf("syncconf: read job %s for results: %v", jobID, err)
		return
	}
	details := map[string]interface{}{}
	if job.Details != "" {
		if err := json.Unmarshal([]byte(job.Details), &details); err != nil {
			log.Printf("syncconf: parse job %s details: %v", jobID, err)
			return
		}
	}
	details["results"] = results
	raw, err := json.Marshal(details)
	if err != nil {
		log.Printf("syncconf: marshal job %s details: %v", jobID, err)
		return
	}
	if err := s.lib.UpdateJobDetails(jobID, string(raw)); err != nil {
		log.Printf("syncconf: update job %s details: %v", jobID, err)
	}
}
