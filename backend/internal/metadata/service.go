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

type Service struct {
	db  *db.DB
	lib *library.Service
}

func NewService(db *db.DB, lib *library.Service) *Service {
	return &Service{db: db, lib: lib}
}

type Pattern struct {
	Text  string  `json:"text"`
	Count int     `json:"count"`
	Score float64 `json:"score"`
}

type AnalysisResult struct {
	Suggestions      []Pattern `json:"suggestions"`
	CommonSubstrings []Pattern `json:"common_substrings"`
}

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

type PreviewRequest struct {
	Action   string   `json:"action"`
	Target   string   `json:"target"`
	Value    string   `json:"value"`
	TrackIDs []string `json:"track_ids"`
}

type PreviewResponse struct {
	TrackID  string `json:"track_id"`
	OldTitle string `json:"old_title"`
	NewTitle string `json:"new_title"`
}

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

		// 1. Update metadata in-place using the Python script
		cmd := exec.Command("python3", "update_metadata.py", track.Path, "--title", p.NewTitle)
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
		s.db.Exec("INSERT INTO metadata_changes (id, track_id, field, old_value, new_value) VALUES (?, ?, 'title', ?, ?)",
			changeID, p.TrackID, p.OldTitle, p.NewTitle)
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
