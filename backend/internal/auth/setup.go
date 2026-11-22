// Package auth — first-run setup wizard (T19).
package auth

import (
	"crypto/subtle"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// registerSetupRoutes mounts the public /setup/* endpoints.
func (s *Service) registerSetupRoutes(r *mux.Router) {
	r.HandleFunc("/setup/status", s.handleSetupStatus).Methods(http.MethodGet)
	r.HandleFunc("/setup/complete", s.handleSetupComplete).Methods(http.MethodPost)
}

// SetupGate wraps the handler chain while the instance is in setup mode:
// every /api/* route except /api/setup/* and /api/auth/providers, plus all
// of /media/*, answers 503 {"error":"setup_required"}. Everything else (the
// SPA, so the /setup page itself can load) passes through. When setup
// completes the gate opens immediately, with no restart.
func (s *Service) SetupGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.SetupRequired() {
			next.ServeHTTP(w, r)
			return
		}
		p := r.URL.Path
		if p == "/api/auth/providers" || strings.HasPrefix(p, "/api/setup/") {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/media/") {
			writeJSONError(w, http.StatusServiceUnavailable, "setup_required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handleSetupStatus: GET /api/setup/status
func (s *Service) handleSetupStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"setup_required": s.SetupRequired()})
}

// handleSetupComplete: POST /api/setup/complete. Creates the first admin
// with the given credentials, logs them in, and ends setup mode. The whole
// thing is guarded inside a transaction so it completes exactly once; a
// repeated or concurrent call gets 409. Wrong token: 403.
func (s *Service) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !s.SetupRequired() {
		writeJSONError(w, http.StatusConflict, "setup already complete")
		return
	}
	ip := clientIP(r)
	if s.setupLimiter.blocked(ip) {
		writeJSONError(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
		return
	}
	// Constant-time token compare: no timing oracle for the token.
	if subtle.ConstantTimeCompare([]byte(req.Token), []byte(s.setupToken)) != 1 {
		s.setupLimiter.record(ip)
		writeJSONError(w, http.StatusForbidden, "invalid setup token")
		return
	}
	// Username and password follow the same rules as registration.
	if !usernameRegexp.MatchString(req.Username) {
		writeJSONError(w, http.StatusBadRequest, "invalid username")
		return
	}
	if len(req.Password) < minPasswordLength {
		writeJSONError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		log.Printf("[auth] setup complete hash: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	u := &User{
		ID:           newID(),
		Username:     req.Username,
		PasswordHash: &hash,
		Provider:     "local",
		Email:        strPtrOrNil(req.Email),
		IsAdmin:      true,
		Status:       UserStatusActive,
	}

	// Complete exactly once: the insert is guarded inside a transaction by
	// a re-check that no users exist yet (the same invariant the startup
	// bootstrap relies on). The write pool serializes the transaction, so
	// concurrent callers cannot both pass the re-check.
	tx, err := s.db.Beginx()
	if err != nil {
		log.Printf("[auth] setup complete begin: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	var count int
	if err := tx.Get(&count, `SELECT COUNT(*) FROM users`); err != nil {
		_ = tx.Rollback()
		log.Printf("[auth] setup complete count: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if count != 0 {
		_ = tx.Rollback()
		writeJSONError(w, http.StatusConflict, "setup already complete")
		return
	}
	if _, err := tx.Exec(
		`INSERT INTO users (id, username, password_hash, provider, subject, email, display_name, is_admin, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, u.Provider, u.Subject, u.Email, u.DisplayName, u.IsAdmin, u.Status,
	); err != nil {
		_ = tx.Rollback()
		log.Printf("[auth] setup complete insert: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := tx.Exec(
		`INSERT INTO audit_log (at, actor, action, target, detail) VALUES (?, ?, ?, ?, ?)`,
		formatTime(time.Now().UTC()), "setup", AuditActionSetupComplete, req.Username, "",
	); err != nil {
		_ = tx.Rollback()
		log.Printf("[auth] setup complete audit: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("[auth] setup complete commit: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Setup ends immediately, with no restart.
	s.setupRequired.Store(false)
	log.Printf("[auth] first-run setup complete: admin %q created", req.Username)
	if err := s.StartSession(w, u.ID); err != nil {
		log.Printf("[auth] setup complete session: %v", err)
	}
	writeJSON(w, http.StatusCreated, u)
}
