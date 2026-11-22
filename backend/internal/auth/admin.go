// Package auth — admin user management API (T8).
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
)

// registerAdminRoutes mounts the /admin/* endpoints behind RequireAdmin.
func (s *Service) registerAdminRoutes(r *mux.Router) {
	admin := r.PathPrefix("/admin").Subrouter()
	admin.Use(s.RequireAdmin)
	admin.HandleFunc("/users", s.handleListUsers).Methods(http.MethodGet)
	admin.HandleFunc("/users", s.handleCreateUser).Methods(http.MethodPost)
	admin.HandleFunc("/users/{id}", s.handlePatchUser).Methods(http.MethodPatch)
	admin.HandleFunc("/users/{id}", s.handleDeleteUser).Methods(http.MethodDelete)
	admin.HandleFunc("/users/{id}/password", s.handleResetUserPassword).Methods(http.MethodPost)
	admin.HandleFunc("/settings", s.handleGetSettings).Methods(http.MethodGet)
	admin.HandleFunc("/settings", s.handlePutSettings).Methods(http.MethodPut)
}

// IsLastActiveAdmin reports whether the user is the only active admin. The
// web admin API and the recovery CLI both refuse actions that would leave
// the instance without an active admin.
func (s *Service) IsLastActiveAdmin(u *User) (bool, error) {
	return s.isLastActiveAdmin(u)
}

// isLastActiveAdmin reports whether the user is the only active admin.
func (s *Service) isLastActiveAdmin(u *User) (bool, error) {
	if !u.IsAdmin || u.Status != UserStatusActive {
		return false, nil
	}
	n, err := s.CountActiveAdmins()
	if err != nil {
		return false, err
	}
	return n <= 1, nil
}

// handleListUsers: GET /api/admin/users
func (s *Service) handleListUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := s.GetUsers()
	if err != nil {
		log.Printf("[auth] list users: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, users)
}

// handleCreateUser: POST /api/admin/users. Creates an active local user;
// admins may grant the new account admin rights at creation time.
func (s *Service) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !usernameRegexp.MatchString(req.Username) {
		writeJSONError(w, http.StatusBadRequest, "invalid username")
		return
	}
	if len(req.Password) < MinPasswordLength {
		writeJSONError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	if _, err := s.GetUserByUsername(req.Username); err == nil {
		writeJSONError(w, http.StatusConflict, "username already taken")
		return
	} else if !errors.Is(err, ErrNoUser) {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	u, err := s.CreateLocalUser(req.Username, req.Password, req.Email, req.IsAdmin)
	if err != nil {
		log.Printf("[auth] create user: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.RecordAudit(adminActor(r), AuditActionCreateUser, req.Username, "")
	writeJSON(w, http.StatusCreated, u)
}

// handlePatchUser: PATCH /api/admin/users/{id}. Any of is_admin, status,
// email and display_name may be sent; absent fields are untouched.
func (s *Service) handlePatchUser(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	target, err := s.GetUserByID(id)
	if errors.Is(err, ErrNoUser) {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	var patch struct {
		IsAdmin     *bool   `json:"is_admin"`
		Status      *string `json:"status"`
		Email       *string `json:"email"`
		DisplayName *string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if patch.Status != nil && !validUserStatus(*patch.Status) {
		writeJSONError(w, http.StatusBadRequest, "invalid status")
		return
	}

	// Last active admin may not lose admin rights or activity: that would
	// leave the instance with zero active admins.
	last, err := s.isLastActiveAdmin(target)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if last && patch.IsAdmin != nil && !*patch.IsAdmin {
		writeJSONError(w, http.StatusConflict, "cannot demote the last active admin")
		return
	}
	if last && patch.Status != nil && *patch.Status != UserStatusActive {
		writeJSONError(w, http.StatusConflict, "cannot disable the last active admin")
		return
	}

	p := UserPatch{
		IsAdmin:     patch.IsAdmin,
		Status:      patch.Status,
		Email:       patch.Email,
		DisplayName: patch.DisplayName,
	}
	if err := s.UpdateUser(id, p); err != nil {
		log.Printf("[auth] patch user: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if summary := patchSummary(p); summary != "" {
		s.RecordAudit(adminActor(r), AuditActionPatchUser, target.Username, summary)
	}
	// Disabling (or suspending as pending) ends every session the user
	// holds.
	if patch.Status != nil && *patch.Status != UserStatusActive {
		if err := s.DeleteUserSessions(id); err != nil {
			// The id is not logged: it is request input (gosec G706).
			log.Printf("[auth] delete sessions after status change: %v", err)
		}
	}
	updated, err := s.GetUserByID(id)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleResetUserPassword: POST /api/admin/users/{id}/password. Admin
// password reset for local users only; it deletes the user's sessions.
func (s *Service) handleResetUserPassword(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	target, err := s.GetUserByID(id)
	if errors.Is(err, ErrNoUser) {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if target.Provider != "local" {
		writeJSONError(w, http.StatusForbidden, "password reset is only available for local users")
		return
	}
	var req struct {
		NewPassword string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.NewPassword) < MinPasswordLength {
		writeJSONError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}
	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := s.UpdatePassword(id, hash); err != nil {
		log.Printf("[auth] reset password: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := s.DeleteUserSessions(id); err != nil {
		// The id is not logged: it is request input (gosec G706).
		log.Printf("[auth] delete sessions after password reset: %v", err)
	}
	s.RecordAudit(adminActor(r), AuditActionResetPassword, target.Username, "")
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteUser: DELETE /api/admin/users/{id}. The last active admin may
// not be deleted (checked first, so deleting oneself as the last admin
// reports the invariant, not the self-rule), and nobody may delete their
// own account at all.
func (s *Service) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	caller := UserFromContext(r.Context())
	target, err := s.GetUserByID(id)
	if errors.Is(err, ErrNoUser) {
		writeJSONError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	last, err := s.isLastActiveAdmin(target)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if last {
		writeJSONError(w, http.StatusConflict, "cannot delete the last active admin")
		return
	}
	if caller != nil && caller.ID == id {
		writeJSONError(w, http.StatusBadRequest, "cannot delete your own account")
		return
	}
	if err := s.DeleteUser(id); err != nil {
		log.Printf("[auth] delete user: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.RecordAudit(adminActor(r), AuditActionDeleteUser, target.Username, "")
	w.WriteHeader(http.StatusNoContent)
}

// handleGetSettings: GET /api/admin/settings
func (s *Service) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	mode, err := s.GetRegistrationMode()
	if err != nil {
		log.Printf("[auth] get settings: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"registration_mode": mode})
}

// handlePutSettings: PUT /api/admin/settings
func (s *Service) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RegistrationMode string `json:"registration_mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !isValidRegistrationMode(req.RegistrationMode) {
		writeJSONError(w, http.StatusBadRequest, "invalid registration mode")
		return
	}
	if err := s.SetRegistrationMode(req.RegistrationMode); err != nil {
		log.Printf("[auth] set registration mode: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.RecordAudit(adminActor(r), AuditActionRegistrationMode, "", "mode="+req.RegistrationMode)
	w.WriteHeader(http.StatusNoContent)
}

// patchSummary renders the fields a UserPatch changes as a compact audit
// detail string ("is_admin=true status=disabled"). It reports only the
// fields actually carried, so a no-op patch renders as the empty string.
func patchSummary(p UserPatch) string {
	var parts []string
	if p.IsAdmin != nil {
		parts = append(parts, fmt.Sprintf("is_admin=%t", *p.IsAdmin))
	}
	if p.Status != nil {
		parts = append(parts, "status="+*p.Status)
	}
	if p.Email != nil {
		parts = append(parts, "email set")
	}
	if p.DisplayName != nil {
		parts = append(parts, "display_name set")
	}
	return strings.Join(parts, " ")
}

// validUserStatus reports whether status is one of the three user statuses.
func validUserStatus(status string) bool {
	switch status {
	case UserStatusActive, UserStatusPending, UserStatusDisabled:
		return true
	}
	return false
}
