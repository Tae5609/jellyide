package metadata

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"yt-scraper/internal/jobs"
)

const (
	// DefaultHistoryLimit is the default page size of the history.
	DefaultHistoryLimit = 50
	// MaxHistoryLimit caps the history page size.
	MaxHistoryLimit = 200
)

// HistoryChange is one field edit inside a change set.
type HistoryChange struct {
	TrackID    string  `json:"track_id"`
	TrackTitle string  `json:"track_title"`
	Field      string  `json:"field"`
	OldValue   *string `json:"old_value"`
	NewValue   *string `json:"new_value"`
}

// HistoryEntry is one change set with its rows.
type HistoryEntry struct {
	ChangeSetID string          `json:"change_set_id"`
	CreatedAt   time.Time       `json:"created_at"`
	TrackCount  int             `json:"track_count"`
	Changes     []HistoryChange `json:"changes"`
}

// historyRow is one joined metadata_changes row.
type historyRow struct {
	ChangeSetID string    `db:"change_set_id"`
	TrackID     string    `db:"track_id"`
	TrackTitle  string    `db:"track_title"`
	Field       string    `db:"field"`
	OldValue    *string   `db:"old_value"`
	NewValue    *string   `db:"new_value"`
	Timestamp   time.Time `db:"timestamp"`
}

// History returns the change sets (newest first) whose rows belong to the
// given album (or all of them when albumID is empty), capped at limit.
func (s *Service) History(albumID string, limit int) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = DefaultHistoryLimit
	}
	if limit > MaxHistoryLimit {
		limit = MaxHistoryLimit
	}

	// Legacy rows from before change sets have a NULL change_set_id and
	// must not show up as one group.
	setQuery := "SELECT change_set_id FROM metadata_changes WHERE change_set_id IS NOT NULL"
	var args []interface{}
	if albumID != "" {
		setQuery += " AND EXISTS (SELECT 1 FROM tracks t WHERE t.id = metadata_changes.track_id AND t.album_id = ?)"
		args = append(args, albumID)
	}
	setQuery += " GROUP BY change_set_id ORDER BY MAX(timestamp) DESC, MAX(id) DESC LIMIT ?"
	args = append(args, limit)

	var setIDs []string
	if err := s.db.Select(&setIDs, setQuery, args...); err != nil {
		return nil, err
	}
	if len(setIDs) == 0 {
		return []HistoryEntry{}, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(setIDs)), ",")
	rowQuery := `
		SELECT mc.change_set_id, mc.track_id, COALESCE(t.title, '') AS track_title,
			mc.field, mc.old_value, mc.new_value, mc.timestamp
		FROM metadata_changes mc
		LEFT JOIN tracks t ON t.id = mc.track_id
		WHERE mc.change_set_id IN (` + placeholders + `)
		ORDER BY mc.timestamp ASC, mc.id ASC`
	rowArgs := make([]interface{}, len(setIDs))
	for i, id := range setIDs {
		rowArgs[i] = id
	}

	var rows []historyRow
	if err := s.db.Select(&rows, rowQuery, rowArgs...); err != nil {
		return nil, err
	}

	entries := map[string]*HistoryEntry{}
	trackSets := map[string]map[string]bool{}
	for _, row := range rows {
		entry, ok := entries[row.ChangeSetID]
		if !ok {
			entry = &HistoryEntry{
				ChangeSetID: row.ChangeSetID,
				CreatedAt:   row.Timestamp,
				Changes:     []HistoryChange{},
			}
			entries[row.ChangeSetID] = entry
			trackSets[row.ChangeSetID] = map[string]bool{}
		}
		if row.Timestamp.After(entry.CreatedAt) {
			entry.CreatedAt = row.Timestamp
		}
		entry.Changes = append(entry.Changes, HistoryChange{
			TrackID:    row.TrackID,
			TrackTitle: row.TrackTitle,
			Field:      row.Field,
			OldValue:   row.OldValue,
			NewValue:   row.NewValue,
		})
		trackSets[row.ChangeSetID][row.TrackID] = true
	}

	out := make([]HistoryEntry, 0, len(entries))
	for _, entry := range entries {
		entry.TrackCount = len(trackSets[entry.ChangeSetID])
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// RevertChangeSet applies the old values of a change set as a new change
// set (so the revert is itself revertible, including file renames). It
// returns a ConflictError when any affected track no longer exists.
func (s *Service) RevertChangeSet(changeSetID string) (string, string, error) {
	var rows []changeRow
	err := s.db.Select(&rows, `
		SELECT id, track_id, field, old_value, new_value, change_set_id
		FROM metadata_changes
		WHERE change_set_id = ?
		ORDER BY timestamp ASC, id ASC`, changeSetID)
	if err != nil {
		return "", "", err
	}
	if len(rows) == 0 {
		return "", "", &NotFoundError{Message: "change set not found"}
	}

	// Group the old values per track; a nil old value means "clear". The
	// stored text is converted back to the field's type (int fields are
	// stored as decimal text).
	editsByTrack := map[string]map[string]interface{}{}
	trackOrder := []string{}
	for _, row := range rows {
		if !isKnownField(row.Field) {
			continue // legacy rows from before field validation
		}
		if _, ok := editsByTrack[row.TrackID]; !ok {
			editsByTrack[row.TrackID] = map[string]interface{}{}
			trackOrder = append(trackOrder, row.TrackID)
		}
		var value interface{}
		switch {
		case row.OldValue == nil:
			value = nil
		case intFields[row.Field]:
			n, perr := strconv.Atoi(strings.TrimSpace(*row.OldValue))
			if perr != nil {
				return "", "", &ValidationError{
					Message: "stored value for field is not an integer",
					Field:   row.Field,
					TrackID: row.TrackID,
				}
			}
			value = n
		default:
			value = *row.OldValue
		}
		editsByTrack[row.TrackID][row.Field] = value
	}

	edits := make([]TrackEdit, 0, len(trackOrder))
	for _, trackID := range trackOrder {
		if len(editsByTrack[trackID]) == 0 {
			continue
		}
		edits = append(edits, TrackEdit{TrackID: trackID, Fields: editsByTrack[trackID]})
	}
	if len(edits) == 0 {
		return "", "", &NotFoundError{Message: "change set has no reversible fields"}
	}

	// Re-validate the stored values and require the tracks to still exist.
	normalized, err := ValidateEdits(edits)
	if err != nil {
		return "", "", err
	}
	for _, e := range normalized {
		if _, err := s.lib.GetTrack(e.TrackID); err != nil {
			return "", "", &ConflictError{Message: "track no longer exists", TrackID: e.TrackID}
		}
	}

	newSetID := uuid.New().String()
	job, err := s.jm.CreateJob(jobs.TypeMetadata, map[string]string{
		"action":        "revert_change_set",
		"change_set_id": newSetID,
		"reverts":       changeSetID,
	})
	if err != nil {
		return "", "", err
	}

	go s.runApplyJob(job.ID, newSetID, normalized)
	return job.ID, newSetID, nil
}
