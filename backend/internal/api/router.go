// Package api wires the HTTP routes of the JellyIDE backend onto a
// gorilla/mux router.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os/exec"
	"strconv"
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
	r.HandleFunc("/jobs/{id}/retry", retryJob(d)).Methods("POST")
	r.HandleFunc("/jobs/retry-failed", retryFailedJobs(d)).Methods("POST")

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
	r.HandleFunc("/library/albums/{id}/cover", getAlbumCover(lib)).Methods("GET")
	r.HandleFunc("/library/albums/{id}/cover", uploadCover(meta)).Methods("POST")
	r.HandleFunc("/library/albums/{id}/cover/extract", extractCover(meta)).Methods("POST")
	r.HandleFunc("/library/albums/{id}/cover/from-url", coverFromURL(meta)).Methods("POST")
	r.HandleFunc("/library/tracks/{id}", getTrack(lib)).Methods("GET")
	r.HandleFunc("/library/tracks/{id}", deleteTrack(lib)).Methods("DELETE")
	r.HandleFunc("/library/scan", triggerScan(s)).Methods("POST")

	// Metadata APIs
	r.HandleFunc("/metadata/analyze/{album_id}", analyzeAlbum(meta)).Methods("GET")
	r.HandleFunc("/metadata/preview", previewMetadata(meta)).Methods("POST")
	r.HandleFunc("/metadata/apply", applyMetadata(meta)).Methods("POST")
	r.HandleFunc("/metadata/tracks", patchTracks(meta)).Methods("PATCH")
	r.HandleFunc("/metadata/history", getHistory(meta)).Methods("GET")
	r.HandleFunc("/metadata/history/{change_set_id}/revert", revertChangeSet(meta)).Methods("POST")
	r.HandleFunc("/metadata/lookup/search", lookupSearch(meta)).Methods("GET")
	r.HandleFunc("/metadata/lookup/release/{mbid}", lookupRelease(meta)).Methods("GET")
}

// maxJSONBody caps the JSON request bodies of the API endpoints (1 MiB).
// Cover uploads carry their own larger cap (metadata.MaxCoverBytes).
const maxJSONBody = 1 << 20

// decodeJSONBody caps r.Body at maxJSONBody and decodes it into v.
// Oversized bodies get 413, malformed JSON 400.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			sendJSONError(w, "request body too large", http.StatusRequestEntityTooLarge)
		} else {
			sendJSONError(w, "invalid json", http.StatusBadRequest)
		}
		return err
	}
	return nil
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
		// Re-encode the yt-dlp output instead of echoing it raw, so
		// untrusted strings from the fetched playlist (titles, etc.) cannot
		// carry raw markup into the response (gosec G705).
		var payload interface{}
		if err := json.Unmarshal(out, &payload); err != nil {
			sendJSONError(w, "failed to parse yt-dlp output", http.StatusInternalServerError)
			return
		}
		writeJSON(w, payload)
	}
}

func queueDownloads(d *download.Downloader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req []download.Details
		if decodeJSONBody(w, r, &req) != nil {
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

func retryJob(d *download.Downloader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := mux.Vars(r)["id"]
		job, err := d.Retry(id)
		switch {
		case errors.Is(err, jobs.ErrJobNotFound):
			sendJSONError(w, "job not found", http.StatusNotFound)
		case errors.Is(err, jobs.ErrNotRetryable):
			sendJSONError(w, "job is not a failed download", http.StatusConflict)
		case err != nil:
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
		default:
			writeJSON(w, job)
		}
	}
}

func retryFailedJobs(d *download.Downloader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		n, err := d.RetryFailedDownloads()
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]int{"retried": n})
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
		if decodeJSONBody(w, r, &req) != nil {
			return
		}
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

func getTrack(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		res, err := lib.GetTrack(id)
		if err != nil {
			sendJSONError(w, "track not found", http.StatusNotFound)
			return
		}
		writeJSON(w, res)
	}
}

func getAlbumCover(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		path, ok, err := lib.CoverFilePath(id)
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !ok {
			sendJSONError(w, "album has no cover", http.StatusNotFound)
			return
		}
		//nolint:gosec // G703 - path comes from the album row via library.CoverFilePath, which confines it to the music dir
		http.ServeFile(w, r, path)
	}
}

func renameAlbum(lib *library.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		var req struct {
			Title string `json:"title"`
		}
		if decodeJSONBody(w, r, &req) != nil {
			return
		}
		err := lib.RenameAlbum(id, req.Title)
		if err != nil {
			if errors.Is(err, library.ErrAlbumExists) {
				sendJSONError(w, err.Error(), http.StatusConflict)
				return
			}
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
		if decodeJSONBody(w, r, &req) != nil {
			return
		}
		res, err := meta.PreviewChanges(req)
		if err != nil {
			writeMetadataError(w, err)
			return
		}
		writeJSON(w, res)
	}
}

func applyMetadata(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req []metadata.PreviewResponse
		if decodeJSONBody(w, r, &req) != nil {
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

// writeMetadataError maps the metadata service's typed errors to the API
// contract: 400 {"error","field","track_id"}, 404, 409 and 500 otherwise.
func writeMetadataError(w http.ResponseWriter, err error) {
	var (
		valErr   *metadata.ValidationError
		notFound *metadata.NotFoundError
		conflict *metadata.ConflictError
	)
	switch {
	case errors.As(err, &valErr):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		body := map[string]string{"error": valErr.Message}
		if valErr.Field != "" {
			body["field"] = valErr.Field
		}
		if valErr.TrackID != "" {
			body["track_id"] = valErr.TrackID
		}
		if jsonErr := json.NewEncoder(w).Encode(body); jsonErr != nil {
			log.Printf("api: writing error response: %v", jsonErr)
		}
	case errors.As(err, &notFound):
		body := map[string]string{"error": notFound.Message}
		if notFound.TrackID != "" {
			body["track_id"] = notFound.TrackID
		}
		sendJSONBody(w, body, http.StatusNotFound)
	case errors.As(err, &conflict):
		body := map[string]string{"error": conflict.Message}
		if conflict.TrackID != "" {
			body["track_id"] = conflict.TrackID
		}
		sendJSONBody(w, body, http.StatusConflict)
	default:
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
	}
}

func sendJSONBody(w http.ResponseWriter, body map[string]string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("api: writing error response: %v", err)
	}
}

func patchTracks(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Edits []metadata.TrackEdit `json:"edits"`
		}
		if decodeJSONBody(w, r, &req) != nil {
			return
		}
		jobID, changeSetID, err := meta.ApplyTrackEdits(req.Edits)
		if err != nil {
			writeMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]string{
			"job_id":        jobID,
			"change_set_id": changeSetID,
		})
	}
}

func getHistory(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		limit := metadata.DefaultHistoryLimit
		if raw := query.Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 {
				sendJSONError(w, "limit must be a positive integer", http.StatusBadRequest)
				return
			}
			limit = n
		}
		res, err := meta.History(query.Get("album_id"), limit)
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	}
}

func revertChangeSet(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		jobID, changeSetID, err := meta.RevertChangeSet(vars["change_set_id"])
		if err != nil {
			writeMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]string{
			"job_id":        jobID,
			"change_set_id": changeSetID,
		})
	}
}

// Cover art handlers
func uploadCover(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		// The form limit is the in-memory cap; large files spill to disk.
		//nolint:gosec // G120 - ParseMultipartForm is bounded to the 10 MB cover limit by the size argument
		if err := r.ParseMultipartForm(metadata.MaxCoverBytes + 512*1024); err != nil {
			sendJSONError(w, "invalid multipart form or file too large", http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			sendJSONError(w, "missing file field", http.StatusBadRequest)
			return
		}
		defer func() {
			if cerr := file.Close(); cerr != nil {
				log.Printf("api: closing cover upload: %v", cerr)
			}
		}()

		jobID, coverURL, err := meta.UploadCover(id, file)
		if err != nil {
			writeMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]string{
			"job_id": jobID,
			"path":   coverURL,
		})
	}
}

func extractCover(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		coverURL, err := meta.ExtractCoverArt(id)
		if err != nil {
			writeMetadataError(w, err)
			return
		}
		writeJSON(w, map[string]string{"path": coverURL})
	}
}

func coverFromURL(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		id := vars["id"]
		var req struct {
			URL string `json:"url"`
		}
		if decodeJSONBody(w, r, &req) != nil {
			return
		}
		jobID, coverURL, err := meta.CoverFromURL(id, req.URL)
		if err != nil {
			writeMetadataError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]string{
			"job_id": jobID,
			"path":   coverURL,
		})
	}
}

// MusicBrainz lookup handlers
func lookupSearch(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		results, err := meta.SearchMusicBrainz(r.Context(), query.Get("album_id"), query.Get("q"))
		if err != nil {
			writeMetadataError(w, err)
			return
		}
		writeJSON(w, results)
	}
}

func lookupRelease(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		res, err := meta.LookupRelease(r.Context(), r.URL.Query().Get("album_id"), vars["mbid"])
		if err != nil {
			writeMetadataError(w, err)
			return
		}
		writeJSON(w, res)
	}
}
