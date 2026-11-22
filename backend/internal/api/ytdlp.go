package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"yt-scraper/internal/ytdlp"
)

// ytdlpAdminTimeout bounds the release check in the check/update endpoints.
const ytdlpAdminTimeout = 2 * time.Minute

// The yt-dlp admin endpoints. All of them are mounted behind requireAdmin
// (T15): the updater changes the binary every download uses, so it is an
// admin-only surface.

// ytdlpStatus: GET /api/admin/ytdlp
func ytdlpStatus(m *ytdlp.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		st, err := m.Status()
		if err != nil {
			sendJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, st)
	}
}

// ytdlpCheck: POST /api/admin/ytdlp/check. Refreshes the latest release
// and returns the status shape; even a failed check returns the shape,
// with the failure in last_result.
func ytdlpCheck(m *ytdlp.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), ytdlpAdminTimeout)
		defer cancel()
		_, _ = m.Check(ctx)
		st, err := m.Status()
		if err != nil {
			sendJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, st)
	}
}

// ytdlpUpdate: POST /api/admin/ytdlp/update. Always enqueues the update as
// a ytdlp_update job and answers 202 {"job_id"}; the job itself reports an
// "up to date" result when the binary in use already matches the known
// latest release and force is false (send {"force": true} to reinstall
// anyway). 409 strictly while an update is already running.
func ytdlpUpdate(m *ytdlp.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Force bool `json:"force"`
		}
		// The body is optional; an empty body means no force.
		_ = json.NewDecoder(r.Body).Decode(&req)
		job, err := m.StartManualUpdate(r.Context(), req.Force)
		switch {
		case errors.Is(err, ytdlp.ErrUpdateRunning):
			sendJSONError(w, "an update is already running", http.StatusConflict)
		case err != nil:
			sendJSONError(w, "internal error", http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusAccepted)
			writeJSON(w, map[string]string{"job_id": job.ID})
		}
	}
}

// ytdlpRollback: POST /api/admin/ytdlp/rollback. 409 when there is no
// previous binary to restore, or when an update is already running
// (rollback shares the update slot with updates and is refused, not
// blocked, while one is in flight).
func ytdlpRollback(m *ytdlp.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if err := m.Rollback(); err != nil {
			switch {
			case errors.Is(err, ytdlp.ErrNoRollback):
				sendJSONError(w, "no previous binary to roll back to", http.StatusConflict)
			case errors.Is(err, ytdlp.ErrUpdateRunning):
				sendJSONError(w, "an update is already running", http.StatusConflict)
			default:
				sendJSONError(w, "rollback failed", http.StatusInternalServerError)
			}
			return
		}
		st, err := m.Status()
		if err != nil {
			sendJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, st)
	}
}

// ytdlpPutSettings: PUT /api/admin/ytdlp/settings. Any of auto_update,
// interval_hours and channel may be sent; absent fields keep their value.
// 400 on invalid values.
func ytdlpPutSettings(m *ytdlp.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AutoUpdate    *bool   `json:"auto_update"`
			IntervalHours *int    `json:"interval_hours"`
			Channel       *string `json:"channel"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSONError(w, "invalid json", http.StatusBadRequest)
			return
		}
		cur, err := m.GetSettings()
		if err != nil {
			sendJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		if req.AutoUpdate != nil {
			cur.AutoUpdate = *req.AutoUpdate
		}
		if req.IntervalHours != nil {
			cur.IntervalHours = *req.IntervalHours
		}
		if req.Channel != nil {
			cur.Channel = *req.Channel
		}
		if err := m.SetSettings(cur); err != nil {
			sendJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
		st, err := m.Status()
		if err != nil {
			sendJSONError(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, st)
	}
}
