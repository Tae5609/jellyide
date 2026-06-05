package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"

	"yt-scraper/internal/download"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
	"yt-scraper/internal/metadata"
	"yt-scraper/internal/scanner"

	"github.com/gorilla/mux"
)

func NewRouter(d *download.Downloader, lib *library.Service, meta *metadata.Service, jm *jobs.Manager, sc *scanner.Scanner) *mux.Router {
	r := mux.NewRouter()

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
	r.HandleFunc("/library/albums/{id}/cover", getAlbumCover(lib)).Methods("GET")
	r.HandleFunc("/library/albums/{id}", deleteAlbum(lib)).Methods("DELETE")
	r.HandleFunc("/library/albums/{id}/rename", renameAlbum(lib)).Methods("POST")
	r.HandleFunc("/library/tracks/{id}", deleteTrack(lib)).Methods("DELETE")
	r.HandleFunc("/library/scan", triggerScan(sc)).Methods("POST")

	// Metadata APIs
	r.HandleFunc("/metadata/analyze/{album_id}", analyzeAlbum(meta)).Methods("GET")
	r.HandleFunc("/metadata/preview", previewMetadata(meta)).Methods("POST")
	r.HandleFunc("/metadata/apply", applyMetadata(meta)).Methods("POST")

	return r
}

func getInfo(d *download.Downloader) http.HandlerFunc {
	// Re-using the same yt-dlp logic from main.go
	return func(w http.ResponseWriter, r *http.Request) {
		// This uses the yt-dlp to get playlist. For brevity, it is similar to previous.
		// Since we didn't extract yt-dlp info logic to download package, we can put it here or there.
		// For now we will mock or move it.
		// We'll reimplement quickly.
		url := r.URL.Query().Get("url")
		if url == "" {
			url = r.URL.Query().Get("id")
		}
		if url == "" {
			http.Error(w, `{"error":"missing url or id"}`, http.StatusBadRequest)
			return
		}

		if !strings.Contains(url, "youtube.com") && !strings.Contains(url, "youtu.be") {
			if len(url) == 11 {
				url = "https://www.youtube.com/watch?v=" + url
			} else {
				url = "https://www.youtube.com/playlist?list=" + url
			}
		}

		// (The actual exec.Command to yt-dlp should go here. We'll simplify for the example.)
		args := []string{"--dump-single-json", "--flat-playlist", "--playlist-end", "200", url}
		cmd := exec.Command("yt-dlp", args...)
		out, err := cmd.Output()
		if err != nil {
			http.Error(w, `{"error":"failed to fetch info"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(out) // Pass raw JSON from yt-dlp.
	}
}

func queueDownloads(d *download.Downloader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req []download.DownloadDetails
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
			return
		}

		jobIDs, err := d.BulkQueueDownloads(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"queued":  len(jobIDs),
			"job_ids": jobIDs,
		})
	}
}

func getJobs(jm *jobs.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobsList, err := jm.GetAllJobs()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jobsList)
	}
}

func clearJobs(jm *jobs.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jm.ClearCompleted()
		w.WriteHeader(http.StatusOK)
	}
}

func getConfig(d *download.Downloader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
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
	return func(w http.ResponseWriter, r *http.Request) {
		res, _ := lib.GetArtists()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	}
}

func getAlbums(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(mux.Vars(r)["id"])
		res, err := lib.GetAlbumsByArtist(id)
		if err != nil {
			log.Printf("Error fetching albums for artist %d: %v", id, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if res == nil {
			res = []library.Album{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	}
}

func getTracks(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(mux.Vars(r)["id"])
		res, _ := lib.GetTracksByAlbum(id)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	}
}

func getAlbum(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(mux.Vars(r)["id"])
		res, _ := lib.GetAlbum(id)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	}
}

func deleteTrack(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(mux.Vars(r)["id"])
		err := lib.DeleteTrack(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func deleteAlbum(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(mux.Vars(r)["id"])
		err := lib.DeleteAlbum(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func renameAlbum(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(mux.Vars(r)["id"])
		var req struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		err := lib.UpdateAlbumTitle(id, req.Title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func getAlbumCover(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(mux.Vars(r)["id"])
		path, err := lib.GetFirstTrackPath(id)
		if err != nil || path == "" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		cmd := exec.Command("ffmpeg", "-i", path, "-an", "-c:v", "copy", "-f", "image2pipe", "-")
		out, err := cmd.Output()
		if err != nil || len(out) == 0 {
			http.Error(w, "No cover", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", http.DetectContentType(out))
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(out)
	}
}

// Metadata Handlers
func analyzeAlbum(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(mux.Vars(r)["album_id"])
		res, err := meta.AnalyzeAlbum(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	}
}

func previewMetadata(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req metadata.PreviewRequest
		json.NewDecoder(r.Body).Decode(&req)
		res, _ := meta.PreviewChanges(req)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	}
}

func applyMetadata(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req []metadata.PreviewResponse
		json.NewDecoder(r.Body).Decode(&req)
		err := meta.ApplyChanges(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
