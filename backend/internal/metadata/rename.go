package metadata

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"

	"yt-scraper/internal/fsutil"
)

// maxRenameSuffixAttempts caps the " (n)" suffix search in
// renameFileNoOverwrite; a directory with a thousand colliding names is
// an error rather than an unbounded search.
const maxRenameSuffixAttempts = 1000

// renameResult is one track's rename outcome in the job's details JSON,
// so the UI can show which names had to be auto-numbered. The field
// names are the contract T16's UI consumes.
type renameResult struct {
	TrackID         string `json:"track_id"`
	RenameCollision bool   `json:"rename_collision"`
	FinalFilename   string `json:"final_filename"`
}

// applyOutcome is the rename outcome of one applied edit: the note for
// the job's progress text and the per-track result when a suffix was
// applied (both empty when no rename happened or none was suffixed).
type applyOutcome struct {
	note   string
	rename *renameResult
}

// renameFileNoOverwrite renames the track file from trackPath to
// <dir>/<base><ext>, adding " (2)", " (3)", ... before the extension
// until the target is free. It returns the final path and whether a
// suffix was applied. It never overwrites an existing file:
// fsutil.SafeRename refuses a target that is taken between the
// existence check and the move (by an earlier edit of this job or by a
// concurrent edit), and the loop then simply tries the next suffix.
func renameFileNoOverwrite(trackPath, dir, base, ext string) (string, bool, error) {
	for attempt := 0; attempt < maxRenameSuffixAttempts; attempt++ {
		name := base + ext
		if attempt > 0 {
			name = fmt.Sprintf("%s (%d)%s", base, attempt+1, ext)
		}
		finalPath := filepath.Join(dir, name)
		err := fsutil.SafeRename(trackPath, finalPath)
		if err == nil {
			return finalPath, attempt > 0, nil
		}
		var exists *fsutil.ExistsError
		if !errors.As(err, &exists) {
			return "", false, err
		}
	}
	return "", false, fmt.Errorf("no free name for %q: too many collisions", base+ext)
}

// renameNote builds the per-rename note for the job's progress text.
func renameNote(filename string) string {
	return fmt.Sprintf("renamed to %q (name taken)", filename)
}

// appendJobResults merges the per-track rename results into the job's
// details JSON so the UI can read them from the finished job. The jobs
// package is not part of this change, so the merge reads through the
// jobs manager and writes through the shared database handle.
func (s *Service) appendJobResults(jobID string, results []renameResult) {
	job, err := s.jm.GetJob(jobID)
	if err != nil {
		log.Printf("metadata: read job %s for results: %v", jobID, err)
		return
	}
	details := map[string]interface{}{}
	if job.Details != "" {
		if err := json.Unmarshal([]byte(job.Details), &details); err != nil {
			log.Printf("metadata: parse job %s details: %v", jobID, err)
			return
		}
	}
	details["results"] = results
	raw, err := json.Marshal(details)
	if err != nil {
		log.Printf("metadata: marshal job %s details: %v", jobID, err)
		return
	}
	if _, err := s.db.Exec("UPDATE jobs SET details = ? WHERE id = ?", string(raw), jobID); err != nil {
		log.Printf("metadata: update job %s details: %v", jobID, err)
	}
}
