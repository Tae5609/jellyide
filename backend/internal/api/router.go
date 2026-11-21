// Package api wires the HTTP routes of the JellyIDE backend onto a
// gorilla/mux router.
package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os/exec"
	"strings"

	"github.com/gorilla/mux"

	"yt-scraper/internal/download"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
	"yt-scraper/internal/metadata"
	"yt-scraper/internal/scanner"
)

// RegisterRoutes registers all API routes to the provided router.
func RegisterRoutes(r *mux.Router, d *download.Downloader, lib *library.Service, meta *metadata.Service, jm *jobs.Manager, s *scanner.Scanner) {
	// Download APIs
	r.HandleFunc("/info", getInfo(d)).Methods("GET")
	r.HandleFunc("/queue", queueDownloads(d)).Methods("POST")

	// Job APIs
	r.HandleFunc("/jobs", getJobs(jm)).Methods("GET")
	r.HandleFunc("/jobs/clear", clearJobs(jm)).Methods("POST")

	// Config APIs
	r.HandleFunc("/config", getConfig(d)).Methods("GET")
	r.HandleFunc("/config", setConfig(d)).Methods("POST")

	// Library APIs
	r.HandleFunc("/library/artists", getArtists(lib)).Methods("GET")
	r.HandleFunc("/library/artists/{id}/albums", getAlbums(lib)).Methods("GET")
	r.HandleFunc("/library/albums/{id}/tracks", getTracks(lib)).Methods("GET")
	r.HandleFunc("/library/albums/{id}", getAlbum(lib)).Methods("GET")
	r.HandleFunc("/library/albums/{id}/rename", renameAlbum(lib)).Methods("POST")
	r.HandleFunc("/library/albums/{id}", deleteAlbum(lib)).Methods("DELETE")
	r.HandleFunc("/library/tracks/{id}", deleteTrack(lib)).Methods("DELETE")
	r.HandleFunc("/library/scan", triggerScan(s)).Methods("POST")

	// Metadata APIs
	r.HandleFunc("/metadata/analyze/{album_id}", analyzeAlbum(meta)).Methods("GET")
	r.HandleFunc("/metadata/preview", previewMetadata(meta)).Methods("POST")
	r.HandleFunc("/metadata/apply", applyMetadata(meta)).Methods("POST")
}

// writeJSON encodes v as the JSON response body. Write failures (e.g. the
// client went away) are logged; there is nothing left to do for them.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: writing response: %v", err)
	}
}

func sendJSONError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": message}); err != nil {
		log.Printf("api: writing error response: %v", err)
	}
}

// infoArgs assembles the yt-dlp arguments for fetching playlist info. The
// user-supplied URL is placed after the `--` separator so it can never be
// parsed as a yt-dlp option (e.g. a value starting with `-`).
func infoArgs(url string) []string {
	return []string{"--dump-single-json", "--flat-playlist", "--playlist-end", "200", "--", url}
}

func getInfo(_ *download.Downloader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		url := r.URL.Query().Get("url")
		if url == "" {
			url = r.URL.Query().Get("id")
		}
		if url == "" {
			sendJSONError(w, "missing url or id", http.StatusBadRequest)
			return
		}

		if !strings.Contains(url, "youtube.com") && !strings.Contains(url, "youtu.be") {
			if len(url) == 11 {
				url = "https://www.youtube.com/watch?v=" + url
			} else {
				url = "https://www.youtube.com/playlist?list=" + url
			}
		}

		cmd := exec.Command("yt-dlp", infoArgs(url)...) //nolint:gosec // G204 - "yt-dlp" is a fixed binary and the URL is after the `--` separator (see infoArgs)
		out, err := cmd.Output()
		if err != nil {
			sendJSONError(w, "failed to fetch info", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(out); err != nil {
			log.Printf("api: writing info response: %v", err)
		}
	}
}

func queueDownloads(d *download.Downloader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req []download.DownloadDetails
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSONError(w, "invalid json", http.StatusBadRequest)
			return
		}

		created, err := d.BulkQueueDownloads(req)
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var jobIDs []string
		for _, j := range created {
			jobIDs = append(jobIDs, j.ID)
		}

		writeJSON(w, map[string]interface{}{
			"queued":  len(jobIDs),
			"job_ids": jobIDs,
		})
	}
}

func getJobs(jm *jobs.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		jobsList, err := jm.GetAllJobs()
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, jobsList)
	}
}

func clearJobs(jm *jobs.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if err := jm.ClearCompleted(); err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func getConfig(d *download.Downloader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]interface{}{
			"concurrent_limit": d.GetLimit(),
			"proxy":            d.GetProxy(),
		})
	}
}

func setConfig(d *download.Downloader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ConcurrentLimit int    `json:"concurrent_limit"`
			Proxy           string `json:"proxy"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		d.SetLimit(req.ConcurrentLimit)
		d.SetProxy(req.Proxy)
		w.WriteHeader(http.StatusOK)
	}
}

// Library Handlers
func getArtists(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		res, err := lib.GetArtists()
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	}
}

func getAlbums(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		res, err := lib.GetAlbumsByArtist(id)
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	}
}

func getTracks(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		res, err := lib.GetTracksByAlbum(id)
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	}
}

func getAlbum(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		res, err := lib.GetAlbum(id)
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, res)
	}
}

func renameAlbum(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		var req struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSONError(w, "invalid json", http.StatusBadRequest)
			return
		}
		err := lib.RenameAlbum(id, req.Title)
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func deleteAlbum(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		err := lib.DeleteAlbum(id)
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func deleteTrack(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		err := lib.DeleteTrack(id)
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func triggerScan(s *scanner.Scanner) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		err := s.Scan()
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// Metadata Handlers
func analyzeAlbum(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["album_id"]
		res, err := meta.AnalyzeAlbum(id)
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	}
}

func previewMetadata(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req metadata.PreviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSONError(w, "invalid json", http.StatusBadRequest)
			return
		}
		res, err := meta.PreviewChanges(req)
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	}
}

func applyMetadata(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req []metadata.PreviewResponse
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSONError(w, "invalid json", http.StatusBadRequest)
			return
		}
		err := meta.ApplyChanges(req)
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
