package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gorilla/mux"

	"yt-scraper/internal/auth"
	"yt-scraper/internal/library"
	"yt-scraper/internal/metadata"
	"yt-scraper/internal/syncconf"
)

// syncAdminAllowed reports whether the request may manage the sync policy
// setting: admins when auth is enabled, and the anonymous superuser when
// it is not (the middleware then passes the request through with no user
// in the context).
func syncAdminAllowed(r *http.Request) bool {
	return syncPolicyAdminAllowed(auth.UserFromContext(r.Context()))
}

// syncPolicyAdminAllowed is the gate logic, separated so it can be tested
// without an authenticated context (the wiring itself is the auth
// middleware's job, mirrored from RequireAdmin).
func syncPolicyAdminAllowed(u *auth.User) bool {
	return u == nil || u.IsAdmin
}

// getSyncConflicts lists conflict rows (default status "open"),
// optionally filtered by album, grouped by track.
func getSyncConflicts(sync *syncconf.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		status := query.Get("status")
		if status == "" {
			status = library.ConflictStatusOpen
		}
		switch status {
		case library.ConflictStatusOpen, library.ConflictStatusResolved, library.ConflictStatusDismissed:
		default:
			sendJSONError(w, "status must be open, resolved or dismissed", http.StatusBadRequest)
			return
		}
		res, err := sync.Conflicts(status, query.Get("album_id"))
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	}
}

// getSyncSummary is the sidebar badge payload.
func getSyncSummary(sync *syncconf.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		res, err := sync.Summary()
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	}
}

// resolveConflicts applies the requested per-field resolutions through a
// metadata job.
func resolveConflicts(sync *syncconf.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Resolutions []syncconf.Resolution `json:"resolutions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSONError(w, "invalid json", http.StatusBadRequest)
			return
		}
		jobID, err := sync.Resolve(req.Resolutions, actorFromRequest(r))
		if err != nil {
			writeSyncError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]string{"job_id": jobID})
	}
}

// resolveAllConflicts resolves every open conflict (optionally one album)
// with one choice.
func resolveAllConflicts(sync *syncconf.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			AlbumID string `json:"album_id"`
			Choice  string `json:"choice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSONError(w, "invalid json", http.StatusBadRequest)
			return
		}
		jobID, err := sync.ResolveAll(req.AlbumID, req.Choice, actorFromRequest(r))
		if err != nil {
			writeSyncError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		writeJSON(w, map[string]string{"job_id": jobID})
	}
}

// dismissConflict marks one conflict dismissed.
func dismissConflict(sync *syncconf.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		c, err := sync.Dismiss(vars["id"], actorFromRequest(r))
		if err != nil {
			writeSyncError(w, err)
			return
		}
		writeJSON(w, c)
	}
}

// getSyncMissing lists missing tracks with their relink candidates.
func getSyncMissing(sync *syncconf.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		res, err := sync.Missing()
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	}
}

// relinkMissing re-points a missing track at a candidate file.
func relinkMissing(sync *syncconf.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSONError(w, "invalid json", http.StatusBadRequest)
			return
		}
		track, err := sync.Relink(vars["track_id"], req.Path)
		if err != nil {
			writeSyncError(w, err)
			return
		}
		writeJSON(w, track)
	}
}

// deleteMissing removes a missing row.
func deleteMissing(sync *syncconf.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if err := sync.DeleteMissing(vars["track_id"]); err != nil {
			writeSyncError(w, err)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// getSyncTrash lists the trashed (displaced) files.
func getSyncTrash(sync *syncconf.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		res, err := sync.Trash()
		if err != nil {
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, res)
	}
}

// restoreTrash moves a trashed file back.
func restoreTrash(sync *syncconf.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if err := sync.Restore(vars["id"]); err != nil {
			if errors.Is(err, library.ErrTrashRestoreConflict) {
				sendJSONError(w, err.Error(), http.StatusConflict)
				return
			}
			if errors.Is(err, sql.ErrNoRows) {
				sendJSONError(w, "trash entry not found", http.StatusNotFound)
				return
			}
			sendJSONError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// getSyncPolicy returns the sync conflict policy (admin).
func getSyncPolicy(sync *syncconf.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !syncAdminAllowed(r) {
			sendJSONError(w, "admin access required", http.StatusForbidden)
			return
		}
		writeJSON(w, map[string]string{"policy": sync.Policy()})
	}
}

// putSyncPolicy validates and stores the sync conflict policy (admin).
func putSyncPolicy(sync *syncconf.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !syncAdminAllowed(r) {
			sendJSONError(w, "admin access required", http.StatusForbidden)
			return
		}
		var req struct {
			Policy string `json:"policy"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSONError(w, "invalid json", http.StatusBadRequest)
			return
		}
		if err := sync.SetPolicy(req.Policy); err != nil {
			sendJSONError(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, map[string]string{"policy": req.Policy})
	}
}

// checkRenameCollisions reports, for the pending edits, which title
// changes would collide with an existing file name (the review dialog
// then offers suffix / skip / replace per track).
func checkRenameCollisions(meta *metadata.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Edits []metadata.TrackEdit `json:"edits"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			sendJSONError(w, "invalid json", http.StatusBadRequest)
			return
		}
		res, err := meta.CheckRenameCollisions(req.Edits)
		if err != nil {
			writeMetadataError(w, err)
			return
		}
		writeJSON(w, res)
	}
}

// actorFromRequest names the user the action is attributed to (auth
// disabled: the empty string, i.e. anonymous).
func actorFromRequest(r *http.Request) string {
	u := auth.UserFromContext(r.Context())
	if u == nil {
		return ""
	}
	return u.Username
}

// writeSyncError maps the syncconf service errors to the API contract:
// 404 for unknown ids, 409 for state conflicts, 400 for bad input, 500
// otherwise.
func writeSyncError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		sendJSONError(w, "not found", http.StatusNotFound)
	case errors.Is(err, syncconf.ErrInvalidResolution):
		sendJSONError(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, syncconf.ErrConflictClosed):
		sendJSONError(w, err.Error(), http.StatusConflict)
	case errors.Is(err, syncconf.ErrNotMissing):
		sendJSONError(w, err.Error(), http.StatusConflict)
	case errors.Is(err, syncconf.ErrNoCandidate):
		sendJSONError(w, err.Error(), http.StatusConflict)
	default:
		sendJSONError(w, err.Error(), http.StatusInternalServerError)
	}
}
