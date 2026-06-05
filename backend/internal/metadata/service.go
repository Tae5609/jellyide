package metadata

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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
	Text  string `json:"text"`
	Count int    `json:"count"`
	Score int    `json:"score"`
}

type AnalysisResult struct {
	Suggestions []Pattern `json:"suggestions"`
}

func (s *Service) AnalyzeAlbum(albumID int) (*AnalysisResult, error) {
	tracks, err := s.lib.GetTracksByAlbum(albumID)
	if err != nil || len(tracks) == 0 {
		return &AnalysisResult{}, err
	}

	titles := make([]string, len(tracks))
	for i, t := range tracks {
		titles[i] = t.Title
	}

	res := &AnalysisResult{
		Suggestions: findBestPatterns(titles),
	}
	return res, nil
}

func findBestPatterns(titles []string) []Pattern {
	totalTracks := len(titles)
	minFreq := totalTracks / 2
	if minFreq < 2 {
		minFreq = 2
	}

	counts := make(map[string]int)
	seenInTitle := make(map[string]map[int]bool)

	for i, title := range titles {
		runes := []rune(title)
		for length := 2; length <= len(runes); length++ {
			for start := 0; start <= len(runes)-length; start++ {
				sub := string(runes[start : start+length])
				if seenInTitle[sub] == nil {
					seenInTitle[sub] = make(map[int]bool)
				}
				seenInTitle[sub][i] = true
			}
		}
	}

	for sub, titleSet := range seenInTitle {
		if len(titleSet) >= minFreq {
			counts[sub] = len(titleSet)
		}
	}

	var result []Pattern
	for sub, count := range counts {
		isMaximal := true
		for otherSub, otherCount := range counts {
			if len(otherSub) > len(sub) && strings.Contains(otherSub, sub) && otherCount == count {
				isMaximal = false
				break
			}
		}
		if isMaximal {
			score := count * len([]rune(sub))
			result = append(result, Pattern{Text: sub, Count: count, Score: score})
		}
	}

	// Sort by descending score
	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})

	return result
}

type PreviewRequest struct {
	Action   string `json:"action"` // replace, prefix, suffix, regex
	Target   string `json:"target"`
	Value    string `json:"value"`
	TrackIDs []int  `json:"track_ids"`
}

type PreviewResponse struct {
	TrackID  int    `json:"track_id"`
	OldTitle string `json:"old_title"`
	NewTitle string `json:"new_title"`
}

func (s *Service) PreviewChanges(req PreviewRequest) ([]PreviewResponse, error) {
	var results []PreviewResponse
	for _, id := range req.TrackIDs {
		// Fetch track
		var track library.Track
		err := s.db.Get(&track, "SELECT * FROM tracks WHERE id = ?", id)
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
	log.Printf("Metadata: Applying %d changes", len(previews))
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}

	for _, p := range previews {
		// Get track current details using the transaction
		var track library.Track
		err := tx.Get(&track, "SELECT id, album_id, title, path, duration, bitrate FROM tracks WHERE id = ?", p.TrackID)
		if err != nil {
			log.Printf("Metadata: Error fetching track %d: %v", p.TrackID, err)
			continue
		}

		// 1. Update physical metadata tags using mutagen helper
		cmd := exec.Command("python3", "update_metadata.py", track.Path, "--title", p.NewTitle)
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("Failed to update file metadata for %s: %v, output: %s", track.Path, err, string(output))
			continue
		}

		// 2. Physically rename the file on disk to match the new title
		ext := filepath.Ext(track.Path)
		newFileName := library.Sanitize(p.NewTitle) + ext
		newPath := filepath.Join(filepath.Dir(track.Path), newFileName)

		if track.Path != newPath {
			err = os.Rename(track.Path, newPath)
			if err != nil {
				log.Printf("Failed to rename file from %s to %s: %v", track.Path, newPath, err)
				// If physical rename fails, we should probably stop for this track to avoid DB/FS mismatch
				continue
			}
		}

		// 3. Update DB (both title and path)
		_, err = tx.Exec("UPDATE tracks SET title = ?, path = ? WHERE id = ?", p.NewTitle, newPath, p.TrackID)
		if err != nil {
			tx.Rollback()
			return err
		}
		// 4. Log change
		_, err = tx.Exec("INSERT INTO metadata_changes (track_id, field, old_value, new_value) VALUES (?, 'title', ?, ?)", p.TrackID, p.OldTitle, p.NewTitle)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	log.Printf("Metadata: Committing changes")
	return tx.Commit()
}
