// Package metadata analyzes track titles for common patterns and applies
// user-confirmed title rewrites (tagged via the Python helper, renamed on
// disk and recorded in the database).
package metadata

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"yt-scraper/internal/db"
	"yt-scraper/internal/library"
)

// Service applies and previews metadata changes.
type Service struct {
	db  *db.DB
	lib *library.Service
}

// NewService builds a metadata Service.
func NewService(db *db.DB, lib *library.Service) *Service {
	return &Service{db: db, lib: lib}
}

// Pattern is one candidate shared substring with its score.
type Pattern struct {
	Text  string  `json:"text"`
	Count int     `json:"count"`
	Score float64 `json:"score"`
}

// AnalysisResult is the /metadata/analyze response for an album.
type AnalysisResult struct {
	Suggestions      []Pattern `json:"suggestions"`
	CommonSubstrings []Pattern `json:"common_substrings"`
}

// AnalyzeAlbum finds the significant shared patterns among the album's
// track titles.
func (s *Service) AnalyzeAlbum(albumID string) (*AnalysisResult, error) {
	tracks, err := s.lib.GetTracksByAlbum(albumID)
	if err != nil || len(tracks) == 0 {
		return &AnalysisResult{Suggestions: []Pattern{}}, err
	}

	titles := make([]string, len(tracks))
	for i, t := range tracks {
		titles[i] = t.Title
	}

	allPatterns := findSignificantPatterns(titles)

	res := &AnalysisResult{
		Suggestions:      allPatterns,
		CommonSubstrings: allPatterns,
	}
	return res, nil
}

func findSignificantPatterns(titles []string) []Pattern {
	totalTracks := len(titles)
	counts := make(map[string]int)
	seenInTitle := make(map[string]map[int]bool)

	for i, title := range titles {
		runes := []rune(title)
		for length := 3; length <= 50 && length <= len(runes); length++ {
			for start := 0; start <= len(runes)-length; start++ {
				sub := string(runes[start : start+length])
				if isJunk(sub) {
					continue
				}
				if seenInTitle[sub] == nil {
					seenInTitle[sub] = make(map[int]bool)
				}
				seenInTitle[sub][i] = true
			}
		}
	}

	for sub, titleSet := range seenInTitle {
		count := len(titleSet)
		threshold := 0.8
		if float64(count)/float64(totalTracks) < threshold {
			continue
		}
		counts[sub] = count
	}

	var candidates []Pattern
	for sub, count := range counts {
		isMaximal := true
		for otherSub, otherCount := range counts {
			if sub == otherSub {
				continue
			}
			if strings.Contains(otherSub, sub) && otherCount >= count {
				isMaximal = false
				break
			}
		}
		if isMaximal {
			score := float64(len(sub)) * float64(count)
			candidates = append(candidates, Pattern{
				Text:  sub,
				Count: count,
				Score: score,
			})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})

	if len(candidates) > 10 {
		candidates = candidates[:10]
	}

	return candidates
}

// isJunk reports whether a candidate substring is noise (too short, or
// shorter than three characters with no letters at all).
func isJunk(s string) bool {
	trimmed := strings.TrimSpace(s)
	if len(trimmed) == 0 {
		return true
	}
	if len(trimmed) < 3 {
		allJunk := true
		for _, r := range trimmed {
			if unicode.IsLetter(r) {
				allJunk = false
				break
			}
		}
		return allJunk
	}
	return false
}

// PreviewRequest is the /metadata/preview request body.
type PreviewRequest struct {
	Action   string   `json:"action"`
	Target   string   `json:"target"`
	Value    string   `json:"value"`
	TrackIDs []string `json:"track_ids"`
}

// PreviewResponse is one track's before/after title.
type PreviewResponse struct {
	TrackID  string `json:"track_id"`
	OldTitle string `json:"old_title"`
	NewTitle string `json:"new_title"`
}

// PreviewChanges computes the new title for each track without touching
// anything. Unknown track ids are skipped; an invalid regex leaves the
// title unchanged.
func (s *Service) PreviewChanges(req PreviewRequest) ([]PreviewResponse, error) {
	var results []PreviewResponse
	for _, id := range req.TrackIDs {
		track, err := s.lib.GetTrack(id)
		if err != nil {
			continue
		}

		newTitle := track.Title
		switch req.Action {
		case "replace":
			newTitle = strings.ReplaceAll(track.Title, req.Target, req.Value)
		case "prefix":
			newTitle = req.Value + track.Title
		case "suffix":
			newTitle = track.Title + req.Value
		case "regex":
			re, err := regexp.Compile(req.Target)
			if err == nil {
				newTitle = re.ReplaceAllString(track.Title, req.Value)
			}
		}

		results = append(results, PreviewResponse{
			TrackID:  id,
			OldTitle: track.Title,
			NewTitle: strings.TrimSpace(newTitle),
		})
	}
	return results, nil
}

// ApplyChanges writes the previewed titles: tag the file (Python helper),
// rename it on disk, update the database row and record the change.
func (s *Service) ApplyChanges(previews []PreviewResponse) error {
	for _, p := range previews {
		track, err := s.lib.GetTrack(p.TrackID)
		if err != nil {
			log.Printf("Metadata: Track %s not found", p.TrackID)
			continue
		}

		if track.Title == p.NewTitle {
			continue
		}

		// 1. Update metadata in-place using the Python script. The `--`
		// stops option parsing so a file path starting with '-' cannot be
		// read as an option.
		cmd := exec.Command("python3", "update_metadata.py", "--title", p.NewTitle, "--", track.Path) //nolint:gosec // G204 - python3 and the script name are fixed; track.Path is a file path rooted in the music dir, after `--`
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Printf("Metadata: Failed to update title for %s: %v. Output: %s", track.Path, err, string(out))
			continue
		}

		// 2. Physically rename the file on disk
		newFilename := sanitizeFilename(p.NewTitle) + filepath.Ext(track.Path)
		newPath := filepath.Join(filepath.Dir(track.Path), newFilename)

		if track.Path != newPath {
			if err := os.Rename(track.Path, newPath); err != nil {
				log.Printf("Metadata: Failed to rename file from %s to %s: %v", track.Path, newPath, err)
				// Even if rename fails, we already updated the metadata in-place
			} else {
				// 3. Update Database with the new path AND new title
				_, err = s.db.Exec("UPDATE tracks SET title = ?, path = ? WHERE id = ?", p.NewTitle, newPath, p.TrackID)
				if err != nil {
					log.Printf("Metadata: Failed to update DB for track %s: %v", p.TrackID, err)
				}
			}
		} else {
			// Just update title in DB
			_, err = s.db.Exec("UPDATE tracks SET title = ? WHERE id = ?", p.NewTitle, p.TrackID)
			if err != nil {
				log.Printf("Metadata: Failed to update DB title for track %s: %v", p.TrackID, err)
			}
		}

		// 4. Log change
		changeID := "change_" + p.TrackID
		if _, err := s.db.Exec("INSERT INTO metadata_changes (id, track_id, field, old_value, new_value) VALUES (?, ?, 'title', ?, ?)",
			changeID, p.TrackID, p.OldTitle, p.NewTitle); err != nil {
			log.Printf("Metadata: Failed to log change for track %s: %v", p.TrackID, err)
		}
	}

	return nil
}

func sanitizeFilename(s string) string {
	replacer := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "-", "\"", "-", "<", "-", ">", "-", "|", "-",
	)
	return replacer.Replace(s)
}
