// Package scanner walks the music directory, probes media files with
// ffprobe and indexes them into the library database.
package scanner

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
)

// yearRegexp finds the first run of four or more digits (a year).
var yearRegexp = regexp.MustCompile(`\d{4,}`)

// TagWriter writes audio file tags. The scanner only needs WriteFields
// (the db_wins policy rewrites the file tags from the DB). Production
// passes a metadata.PythonTagWriter; tests pass a stub.
type TagWriter interface {
	WriteFields(path string, fields map[string]interface{}) error
}

// Scanner indexes media files found under a base directory.
type Scanner struct {
	lib        *library.Service
	jobManager *jobs.Manager
	basePath   string
	// Tags rewrites file tags from the DB under the db_wins policy.
	// Nil disables db_wins (the track is left untouched and logged).
	Tags TagWriter
	// probeFile probes one file; tests substitute a fake so no ffprobe
	// binary or real audio files are needed.
	probeFile func(path string) (probeInfo, error)
}

// NewScanner builds a Scanner for the given library, job manager and base
// directory.
func NewScanner(lib *library.Service, jobManager *jobs.Manager, basePath string) *Scanner {
	s := &Scanner{
		lib:        lib,
		jobManager: jobManager,
		basePath:   basePath,
	}
	s.probeFile = s.probeFFProbe
	return s
}

// probeFFProbe runs ffprobe on one file and parses the result.
func (s *Scanner) probeFFProbe(path string) (probeInfo, error) {
	cmd := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", path) //nolint:gosec // G204 - ffprobe is a fixed binary; path comes from the walk under basePath or from a finished download
	out, err := cmd.Output()
	if err != nil {
		return probeInfo{}, err
	}
	return parseFFProbe(out, path, s.basePath)
}

// FFProbeOutput is the JSON shape of
// `ffprobe -print_format json -show_format` for one file.
type FFProbeOutput struct {
	Format struct {
		Duration string            `json:"duration"`
		BitRate  string            `json:"bit_rate"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

// probeInfo is the per-file metadata derived by parseFFProbe.
type probeInfo struct {
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	TrackNumber int
	DiscNumber  int
	Year        int
	Genre       string
	Composer    string
	Comment     string
	Duration    int
	Bitrate     int
}

// tagGet looks up a tag key case-insensitively, trying the candidate keys
// in order (ffprobe reports Vorbis comments lower-cased, but ID3-derived
// tags may differ).
func tagGet(tags map[string]string, candidates ...string) string {
	for key, value := range tags {
		lower := strings.ToLower(key)
		for _, c := range candidates {
			if lower == c {
				return value
			}
		}
	}
	return ""
}

// parseTrackNumber extracts the track number from values like "3" or
// "3/12"; it returns 0 when the value carries no usable number.
func parseTrackNumber(value string) int {
	value = strings.TrimSpace(value)
	if i := strings.IndexByte(value, '/'); i >= 0 {
		value = value[:i]
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// parseYear takes the first four digits of the first run of four or more
// digits in values like "2001", "2001-05-15" or "Spring 1973"; it returns 0
// when none is present.
func parseYear(value string) int {
	m := yearRegexp.FindString(value)
	if m == "" {
		return 0
	}
	n, err := strconv.Atoi(m[:4])
	if err != nil {
		return 0
	}
	return n
}

// parseFFProbe parses ffprobe JSON output and derives the track's tag
// fields, duration and bitrate for the file at path. Fallbacks: the title
// comes from the file name, artist and album from the directory layout
// relative to basePath.
func parseFFProbe(data []byte, path, basePath string) (probeInfo, error) {
	var out FFProbeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return probeInfo{}, fmt.Errorf("parse ffprobe output: %w", err)
	}

	tags := out.Format.Tags
	info := probeInfo{
		Title:       tagGet(tags, "title"),
		Artist:      tagGet(tags, "artist"),
		Album:       tagGet(tags, "album"),
		AlbumArtist: tagGet(tags, "album_artist", "albumartist"),
		Genre:       tagGet(tags, "genre"),
		Composer:    tagGet(tags, "composer"),
		Comment:     tagGet(tags, "comment"),
	}
	info.TrackNumber = parseTrackNumber(tagGet(tags, "track", "tracknumber", "track_number"))
	info.DiscNumber = parseTrackNumber(tagGet(tags, "disc", "discnumber", "disc_number"))
	info.Year = parseYear(tagGet(tags, "date", "year"))
	if info.Title == "" {
		info.Title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	rel, _ := filepath.Rel(basePath, path)
	parts := strings.Split(rel, string(os.PathSeparator))

	if info.Artist == "" {
		if len(parts) >= 2 {
			info.Artist = parts[0]
		} else {
			info.Artist = "Unknown Artist"
		}
	}
	if info.Album == "" {
		switch {
		case len(parts) >= 3:
			info.Album = parts[1]
		case len(parts) == 2:
			info.Album = parts[0]
		default:
			info.Album = "Unknown Album"
		}
	}

	duration, _ := strconv.ParseFloat(out.Format.Duration, 64)
	bitrate, _ := strconv.Atoi(out.Format.BitRate)
	info.Duration = int(duration)
	info.Bitrate = bitrate

	return info, nil
}

// Scan starts a scan job that walks the base directory in the background
// and indexes every supported media file it finds.
func (s *Scanner) Scan() error {
	job, err := s.jobManager.CreateJob(jobs.TypeScan, map[string]string{"path": s.basePath})
	if err != nil {
		return err
	}

	go func() {
		if err := s.jobManager.UpdateJobStatus(job.ID, jobs.StatusRunning, "Scanning...", ""); err != nil {
			log.Printf("scanner: update scan job %s: %v", job.ID, err)
		}

		// One change set per scan run: the file-wins rows the scan
		// records in the history can be reverted together.
		changeSetID := uuid.New().String()
		count := 0
		walkErr := filepath.Walk(s.basePath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				// Skip unreadable entries and keep scanning the rest.
				return nil //nolint:nilerr // walk param error is intentionally swallowed
			}
			if info.IsDir() {
				if info.Name() == ".jellyide" {
					return filepath.SkipDir
				}
				return nil
			}

			if !isSupportedAudioExt(path) {
				return nil
			}

			s.processFile(path, changeSetID)
			count++
			if count%10 == 0 {
				if uerr := s.jobManager.UpdateJobStatus(job.ID, jobs.StatusRunning, strconv.Itoa(count)+" tracks found", ""); uerr != nil {
					log.Printf("scanner: update scan job %s: %v", job.ID, uerr)
				}
			}
			return nil
		})

		if walkErr != nil {
			if err := s.jobManager.UpdateJobStatus(job.ID, jobs.StatusFailed, "", walkErr.Error()); err != nil {
				log.Printf("scanner: update scan job %s: %v", job.ID, err)
			}
			return
		}
		// Ghost pass: rows whose file no longer exists become
		// state='missing' (kept, never deleted).
		s.markGhosts()
		if err := s.jobManager.UpdateJobStatus(job.ID, jobs.StatusCompleted, "100%", ""); err != nil {
			log.Printf("scanner: update scan job %s: %v", job.ID, err)
		}
	}()

	return nil
}

// IndexFile indexes a single file (used after a download finishes).
func (s *Scanner) IndexFile(path string) {
	s.processFile(path, uuid.New().String())
}

// isSupportedAudioExt reports whether path has a media extension the
// scanner indexes.
func isSupportedAudioExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".flac", ".mp3", ".m4a", ".aac":
		return true
	default:
		return false
	}
}

// processFile probes one file and indexes it against the library (T16
// sync-aware upsert: see syncTrack).
func (s *Scanner) processFile(path, changeSetID string) {
	info, err := s.probeFile(path)
	if err != nil {
		// ffprobe failed (unreadable file, missing binary, corrupt media).
		// If a row exists its file may have been deleted: mark it missing.
		s.markGhost(path)
		return
	}
	s.syncTrack(path, info, changeSetID)
}

// syncTrack is the T16 sync-aware upsert for one probed file. A new file
// is inserted; an existing row is compared against the fingerprint stored
// at the last synchronization and the outcome follows the active
// sync_conflict_policy (settings table, env SYNC_CONFLICT_POLICY, default
// "ask"):
//
//	file unchanged since last sync            -> refresh synced_at, state ok
//	state=conflict, file now matches the DB   -> clear the conflict
//	file changed, no JellyIDE edit since sync -> file wins (as before), history
//	file changed, DB edited since sync:
//	  ask       -> one sync_conflicts row per differing field, state=conflict,
//	               DB and file untouched
//	  file_wins -> file wins, history
//	  db_wins   -> the file tags are rewritten from the DB
func (s *Scanner) syncTrack(path string, info probeInfo, changeSetID string) {
	st, err := os.Stat(path)
	if err != nil {
		s.markGhost(path)
		return
	}
	size, mtime := st.Size(), st.ModTime().Unix()
	fileValues := probeTagValues(info)
	fileHash := library.TagsHash(fileValues)

	artistID, err := s.lib.EnsureArtist(info.Artist)
	if err != nil {
		log.Printf("scanner: ensure artist %q: %v", info.Artist, err)
		return
	}
	albumID, err := s.lib.EnsureAlbum(artistID, info.Album)
	if err != nil {
		log.Printf("scanner: ensure album %q: %v", info.Album, err)
		return
	}

	s.detectCover(albumID, filepath.Dir(path))

	track, err := s.lib.GetTrackByPath(path)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if err := s.lib.CreateTrack(albumID, info.Title, path, info.Duration, info.Bitrate, trackMetaFromProbe(info), size, mtime, fileHash); err != nil {
			log.Printf("scanner: create track %s: %v", path, err)
		}
		return
	case err != nil:
		log.Printf("scanner: lookup track %s: %v", path, err)
		return
	}

	s.syncExistingTrack(track, info, albumID, fileValues, fileHash, size, mtime, changeSetID)
}

// syncExistingTrack applies the detection policy to a track whose file
// exists on disk.
func (s *Scanner) syncExistingTrack(track *library.Track, info probeInfo, albumID string, fileValues library.TagValues, fileHash string, size, mtime int64, changeSetID string) {
	// An unresolved conflict owns the row: the stored fingerprint is
	// stale by definition, so compare the file against the DB directly.
	if track.State == library.TrackStateConflict {
		if fileHash == library.TagsHash(s.lib.RowTagValues(track)) {
			// The file was brought back in line with the DB out of band:
			// the conflict is resolved by equality.
			if err := s.lib.SetSyncState(track.ID, size, mtime, fileHash, library.NowSQL(), library.TrackStateOK, false); err != nil {
				log.Printf("scanner: set sync state for %s: %v", track.ID, err)
			}
			if err := s.lib.AutoResolveConflicts(track.ID, "file_matches_db"); err != nil {
				log.Printf("scanner: auto-resolve conflicts for %s: %v", track.ID, err)
			}
		}
		return
	}

	// The file is exactly what the last synchronization saw: DB and file
	// are known-equal. This also re-adopts a file that reappears (a
	// state='missing' row returns to ok).
	if s.fingerprintEqual(track, fileHash, size, mtime) {
		if err := s.lib.SetSyncState(track.ID, size, mtime, fileHash, library.NowSQL(), library.TrackStateOK, false); err != nil {
			log.Printf("scanner: set sync state for %s: %v", track.ID, err)
		}
		return
	}

	// The fingerprint changed since the last sync.
	var since string
	if track.SyncedAt != nil {
		since = *track.SyncedAt
	}
	edited, _ := s.lib.EditedSince(track.ID, since)
	switch {
	case !edited || s.lib.SyncPolicy() == library.SyncPolicyFileWins:
		// No JellyIDE edit since the last sync (or the policy is
		// file_wins): the file wins, as today — but every overwritten
		// field is recorded in the history.
		if _, err := s.lib.SyncFileToDB(track, fileValues, info.Duration, info.Bitrate, albumID, changeSetID); err != nil {
			log.Printf("scanner: sync file to DB for %s: %v", track.ID, err)
			return
		}
		if err := s.lib.SetSyncState(track.ID, size, mtime, fileHash, library.NowSQL(), library.TrackStateOK, false); err != nil {
			log.Printf("scanner: set sync state for %s: %v", track.ID, err)
		}
	case s.lib.SyncPolicy() == library.SyncPolicyDBWins:
		s.applyDBToFile(track)
	default:
		// ask: report every differing field and wait for a user
		// decision. Both the DB and the file stay untouched; the
		// fingerprint is not refreshed, so the next scan re-enters the
		// state=conflict branch (no duplicate rows).
		dbValues := s.lib.RowTagValues(track)
		for _, field := range library.DifferingFields(dbValues, fileValues) {
			if _, err := s.lib.InsertConflict(track.ID, field,
				library.TagValueText(field, dbValues[field]),
				library.TagValueText(field, fileValues[field]),
				library.ConflictSourceScan); err != nil {
				log.Printf("scanner: record conflict %s/%s: %v", track.ID, field, err)
			}
		}
		if err := s.lib.SetTrackState(track.ID, library.TrackStateConflict); err != nil {
			log.Printf("scanner: set state for %s: %v", track.ID, err)
		}
	}
}

// applyDBToFile rewrites the file tags from the DB (db_wins policy) and
// re-synchronizes the fingerprint.
func (s *Scanner) applyDBToFile(track *library.Track) {
	if s.Tags == nil {
		log.Printf("scanner: db_wins policy needs a tag writer; leaving %s untouched", track.Path)
		return
	}
	dbValues := s.lib.RowTagValues(track)
	fields := make(map[string]interface{}, len(dbValues))
	for field, value := range dbValues {
		fields[field] = value
	}
	if err := s.Tags.WriteFields(track.Path, fields); err != nil {
		log.Printf("scanner: rewrite tags from DB for %s: %v", track.Path, err)
		return
	}
	st, err := os.Stat(track.Path)
	if err != nil {
		log.Printf("scanner: re-stat %s: %v", track.Path, err)
		return
	}
	// The file was just written with the DB values, so the DB map is the
	// file's normalized tag map.
	dbHash := library.TagsHash(dbValues)
	if err := s.lib.SetSyncState(track.ID, st.Size(), st.ModTime().Unix(), dbHash, library.NowSQL(), library.TrackStateOK, false); err != nil {
		log.Printf("scanner: set sync state for %s: %v", track.ID, err)
	}
}

// fingerprintEqual reports whether the file matches the stored sync
// fingerprint (size, mtime and tags hash all set and equal).
func (s *Scanner) fingerprintEqual(track *library.Track, hash string, size, mtime int64) bool {
	return track.FileSize != nil && *track.FileSize == size &&
		track.FileMtime != nil && *track.FileMtime == mtime &&
		track.TagsHash != nil && *track.TagsHash == hash
}

// markGhosts marks every track whose file no longer exists as
// state='missing' (the row and its history are kept, never deleted).
func (s *Scanner) markGhosts() {
	tracks, err := s.lib.ListTracks()
	if err != nil {
		log.Printf("scanner: list tracks for ghost pass: %v", err)
		return
	}
	for _, t := range tracks {
		if t.State == library.TrackStateMissing {
			continue
		}
		if _, err := os.Stat(t.Path); err != nil {
			if merr := s.lib.MarkMissing(t.ID); merr != nil {
				log.Printf("scanner: mark %s missing: %v", t.ID, merr)
			}
		}
	}
}

// markGhost marks the track at path (if any) as missing.
func (s *Scanner) markGhost(path string) {
	track, err := s.lib.GetTrackByPath(path)
	if err != nil {
		return // unknown path: nothing to mark
	}
	if track.State == library.TrackStateMissing {
		return
	}
	if err := s.lib.MarkMissing(track.ID); err != nil {
		log.Printf("scanner: mark %s missing: %v", track.ID, err)
	}
}

// probeTagValues is the normalized tag map of a probe result — the same
// normalization the Python --read helper and the DB-row side use (missing
// text -> "", missing int -> 0), so the hashes are comparable.
func probeTagValues(info probeInfo) library.TagValues {
	return library.NormalizeTagValues(map[string]interface{}{
		"title":        info.Title,
		"album":        info.Album,
		"artist":       info.Artist,
		"album_artist": info.AlbumArtist,
		"track_number": info.TrackNumber,
		"disc_number":  info.DiscNumber,
		"year":         info.Year,
		"genre":        info.Genre,
		"composer":     info.Composer,
		"comment":      info.Comment,
	})
}

// trackMetaFromProbe converts the optional probe fields into a library
// TrackMeta (zero/empty values become NULL).
func trackMetaFromProbe(info probeInfo) library.TrackMeta {
	return library.TrackMeta{
		Artist:      ptrIfNotEmpty(info.Artist),
		AlbumArtist: ptrIfNotEmpty(info.AlbumArtist),
		TrackNumber: ptrIfNotZero(info.TrackNumber),
		DiscNumber:  ptrIfNotZero(info.DiscNumber),
		Year:        ptrIfNotZero(info.Year),
		Genre:       ptrIfNotEmpty(info.Genre),
		Composer:    ptrIfNotEmpty(info.Composer),
		Comment:     ptrIfNotEmpty(info.Comment),
	}
}

func ptrIfNotEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptrIfNotZero(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func (s *Scanner) detectCover(albumID string, dir string) {
	coverNames := []string{"cover.jpg", "cover.png", "folder.jpg", "front.jpg", "album.jpg"}

	for _, name := range coverNames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			rel, _ := filepath.Rel(s.basePath, path)
			if err := s.lib.SetAlbumCover(albumID, "/media/"+filepath.ToSlash(rel)); err != nil {
				log.Printf("scanner: set cover for album %s: %v", albumID, err)
			}
			return
		}
	}

	files, err := os.ReadDir(dir)
	if err == nil {
		for _, f := range files {
			if !f.IsDir() {
				ext := strings.ToLower(filepath.Ext(f.Name()))
				if ext == ".jpg" || ext == ".jpeg" || ext == ".png" {
					path := filepath.Join(dir, f.Name())
					rel, _ := filepath.Rel(s.basePath, path)
					if err := s.lib.SetAlbumCover(albumID, "/media/"+filepath.ToSlash(rel)); err != nil {
						log.Printf("scanner: set cover for album %s: %v", albumID, err)
					}
					return
				}
			}
		}
	}
}
