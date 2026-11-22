// Package auth — registration modes and admin user management tests (T8).
package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"yt-scraper/internal/db"
)

// newTestDB opens a temp sqlite database with the full migration.
func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.NewDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// registerUser POSTs /api/auth/register and returns the recorder.
func registerUser(t *testing.T, h http.Handler, username, password, email string) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]string{"username": username, "password": password}
	if email != "" {
		body["email"] = email
	}
	return doReq(t, h, http.MethodPost, "/api/auth/register", body, nil)
}

// adminSession logs in with username/password and returns the session cookie.
func adminSession(t *testing.T, h http.Handler, username, password string) string {
	t.Helper()
	token := cookieValue(t, login(t, h, username, password), SessionCookieName)
	if token == "" {
		t.Fatal("login did not set a session cookie")
	}
	return token
}

func TestProvidersIncludesRegistrationMode(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	rec := doReq(t, h, http.MethodGet, "/api/auth/providers", nil, nil)
	body := decodeBody(t, rec)
	if body["registration"] != "admin_only" {
		t.Fatalf("default registration mode = %v, want admin_only", body["registration"])
	}

	if err := svc.SetRegistrationMode(RegistrationModeOpen); err != nil {
		t.Fatalf("SetRegistrationMode: %v", err)
	}
	rec = doReq(t, h, http.MethodGet, "/api/auth/providers", nil, nil)
	body = decodeBody(t, rec)
	if body["registration"] != "open" {
		t.Fatalf("registration mode after change = %v, want open", body["registration"])
	}
}

func TestRegisterAdminOnlyForbidden(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	rec := registerUser(t, h, "newbie", "password-123", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("register in admin_only: got %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if _, err := svc.GetUserByUsername("newbie"); !errors.Is(err, ErrNoUser) {
		t.Fatal("register in admin_only must not create a user")
	}
}

func TestRegisterOpenStartsSession(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	if err := svc.SetRegistrationMode(RegistrationModeOpen); err != nil {
		t.Fatalf("SetRegistrationMode: %v", err)
	}

	rec := registerUser(t, h, "newbie", "password-123", "new@example.com")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register open: got %d, want 201: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["status"] != "active" {
		t.Fatalf("register open status = %v, want active", body["status"])
	}

	// Open registration starts a session immediately.
	token := cookieValue(t, rec, SessionCookieName)
	rec = doReq(t, h, http.MethodGet, "/api/auth/me", nil, sessionHeader(t, token, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("me after open register: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body = decodeBody(t, rec)
	if body["username"] != "newbie" || body["status"] != "active" {
		t.Fatalf("me body = %v", body)
	}

	// The account can also log in with its password.
	if rec := login(t, h, "newbie", "password-123"); rec.Code != http.StatusOK {
		t.Fatalf("login after open register: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	u, err := svc.GetUserByUsername("newbie")
	if err != nil {
		t.Fatalf("lookup newbie: %v", err)
	}
	if u.Provider != "local" || u.Email == nil || *u.Email != "new@example.com" {
		t.Fatalf("newbie user = %+v", u)
	}
}

func TestRegisterApprovalPendingUntilApproved(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	if err := svc.SetRegistrationMode(RegistrationModeApproval); err != nil {
		t.Fatalf("SetRegistrationMode: %v", err)
	}

	rec := registerUser(t, h, "newbie", "password-123", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register approval: got %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if body := decodeBody(t, rec); body["status"] != "pending" {
		t.Fatalf("register approval status = %v, want pending", body["status"])
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			t.Fatal("approval register must not start a session")
		}
	}

	// Correct password, pending account: 403 with the approval message.
	rec = login(t, h, "newbie", "password-123")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("login pending: got %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeBody(t, rec)["error"]; msg != "account pending approval" {
		t.Fatalf("pending login error = %v", msg)
	}

	// An admin approves the account; login then succeeds.
	adminToken := adminSession(t, h, testAdmin, testPassword)
	u, err := svc.GetUserByUsername("newbie")
	if err != nil {
		t.Fatalf("lookup newbie: %v", err)
	}
	rec = doReq(t, h, http.MethodPatch, "/api/admin/users/"+u.ID,
		map[string]string{"status": "active"}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("approve: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := login(t, h, "newbie", "password-123"); rec.Code != http.StatusOK {
		t.Fatalf("login after approval: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestRegisterValidation(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	if err := svc.SetRegistrationMode(RegistrationModeOpen); err != nil {
		t.Fatalf("SetRegistrationMode: %v", err)
	}

	badUsernames := []string{
		"ab",                    // too short
		strings.Repeat("a", 33), // too long
		"has space",             // space
		"semi;colon",            // punctuation not allowed
		"unicode-ö",             // non-ASCII
		"",                      // empty
	}
	for _, username := range badUsernames {
		rec := registerUser(t, h, username, "password-123", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("register username %q: got %d, want 400: %s", username, rec.Code, rec.Body.String())
		}
	}

	// Short password.
	rec := registerUser(t, h, "newbie", "short-1", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("register short password: got %d, want 400: %s", rec.Code, rec.Body.String())
	}

	// Username already taken (the bootstrapped admin).
	rec = registerUser(t, h, testAdmin, "password-123", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("register taken username: got %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeBody(t, rec)["error"]; msg != "username already taken" {
		t.Fatalf("taken username error = %v", msg)
	}

	// Bad JSON.
	rec = doReq(t, h, http.MethodPost, "/api/auth/register", "{not json", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("register bad json: got %d, want 400", rec.Code)
	}

	// The boundary values are accepted: 3 and 32 character usernames, 8-char
	// password.
	rec = registerUser(t, h, "abc", "password-1", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register 3-char username: got %d, want 201: %s", rec.Code, rec.Body.String())
	}
	rec = registerUser(t, h, strings.Repeat("a", 32), "password-1", "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("register 32-char username: got %d, want 201: %s", rec.Code, rec.Body.String())
	}
}

func TestRegisterRateLimit(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	if err := svc.SetRegistrationMode(RegistrationModeOpen); err != nil {
		t.Fatalf("SetRegistrationMode: %v", err)
	}

	// Claiming a taken username counts as a failed attempt, like a failed
	// login. After loginFailureLimit of them the endpoint returns 429.
	for i := 0; i < loginFailureLimit; i++ {
		rec := registerUser(t, h, testAdmin, "password-123", "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("attempt %d: got %d, want 409", i+1, rec.Code)
		}
	}
	rec := registerUser(t, h, "fresh-user", "password-123", "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after %d failures: got %d, want 429", loginFailureLimit, rec.Code)
	}
}

// Regression: validation rejections (invalid username, short password)
// feed the shared failure limiter like the 409s do, so a flood of short
// usernames/passwords cannot sail past the lockout.
func TestRegisterValidationFailuresRateLimit(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	if err := svc.SetRegistrationMode(RegistrationModeOpen); err != nil {
		t.Fatalf("SetRegistrationMode: %v", err)
	}

	// Five invalid usernames plus five short passwords: 400s, all counted.
	for i := 0; i < loginFailureLimit/2; i++ {
		rec := registerUser(t, h, "x", "password-123", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("bad username %d: got %d, want 400: %s", i+1, rec.Code, rec.Body.String())
		}
		rec = registerUser(t, h, "validname", "short-1", "")
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("short password %d: got %d, want 400: %s", i+1, rec.Code, rec.Body.String())
		}
	}
	// The next attempt is locked out even with a fully valid payload.
	rec := registerUser(t, h, "fresh-user", "password-123", "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after %d validation failures: got %d, want 429", loginFailureLimit, rec.Code)
	}
	if _, err := svc.GetUserByUsername("fresh-user"); !errors.Is(err, ErrNoUser) {
		t.Fatal("blocked attempt must not create a user")
	}
}

func TestLoginStatusGates(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	u, err := svc.CreateLocalUser("gated", "password-123", "", false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}

	// Disabled account: 403 after a correct password, no session.
	if err := svc.SetStatus(u.ID, UserStatusDisabled); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	rec := login(t, h, "gated", "password-123")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("login disabled: got %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeBody(t, rec)["error"]; msg != "account disabled" {
		t.Fatalf("disabled login error = %v", msg)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			t.Fatal("disabled login must not set a session cookie")
		}
	}

	// Pending account: same, with the approval message.
	if err := svc.SetStatus(u.ID, UserStatusPending); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	rec = login(t, h, "gated", "password-123")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("login pending: got %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeBody(t, rec)["error"]; msg != "account pending approval" {
		t.Fatalf("pending login error = %v", msg)
	}

	// A wrong password never reveals the account status.
	rec = login(t, h, "gated", "wrong-password-9")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password on pending: got %d, want 401", rec.Code)
	}
	if msg := decodeBody(t, rec)["error"]; msg != "invalid credentials" {
		t.Fatalf("wrong password error = %v", msg)
	}

	// Active account logs in normally.
	if err := svc.SetStatus(u.ID, UserStatusActive); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if rec := login(t, h, "gated", "password-123"); rec.Code != http.StatusOK {
		t.Fatalf("login active: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestNonActiveUserCannotHoldSession(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	u, err := svc.CreateLocalUser("holder", "password-123", "", false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	// Start a session directly, then suspend the account.
	rec := login(t, h, "holder", "password-123")
	token := cookieValue(t, rec, SessionCookieName)
	if err := svc.SetStatus(u.ID, UserStatusPending); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	// The existing session is rejected (and dropped) once the user is no
	// longer active.
	rec = doReq(t, h, http.MethodGet, "/api/auth/me", nil, sessionHeader(t, token, false))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me with pending user session: got %d, want 401: %s", rec.Code, rec.Body.String())
	}
	var sessions int
	if err := svc.db.Get(&sessions, `SELECT COUNT(*) FROM sessions`); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("sessions left for a non-active user = %d, want 0", sessions)
	}
}

func TestAdminAPIRequiresAdmin(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	// No session at all: the auth middleware rejects with 401.
	rec := doReq(t, h, http.MethodGet, "/api/admin/users", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin list without session: got %d, want 401", rec.Code)
	}

	// A signed-in non-admin gets 403 on every admin endpoint.
	if _, err := svc.CreateLocalUser("pleb", "password-123", "", false); err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	plebToken := adminSession(t, h, "pleb", "password-123")
	paths := []struct {
		method, path string
	}{
		{http.MethodGet, "/api/admin/users"},
		{http.MethodPost, "/api/admin/users"},
		{http.MethodPatch, "/api/admin/users/" + strings.Repeat("0", 36)},
		{http.MethodDelete, "/api/admin/users/" + strings.Repeat("0", 36)},
		{http.MethodGet, "/api/admin/settings"},
		{http.MethodPut, "/api/admin/settings"},
	}
	for _, p := range paths {
		rec := doReq(t, h, p.method, p.path,
			map[string]interface{}{"registration_mode": "open"}, sessionHeader(t, plebToken, true))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: got %d, want 403: %s", p.method, p.path, rec.Code, rec.Body.String())
		}
		if msg := decodeBody(t, rec)["error"]; msg != "admin required" {
			t.Fatalf("%s %s error = %v", p.method, p.path, msg)
		}
	}
}

func TestAdminListUsers(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	adminToken := adminSession(t, h, testAdmin, testPassword)

	u, err := svc.CreateLocalUser("listed", "password-123", "l@example.com", false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	if err := svc.SetStatus(u.ID, UserStatusPending); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	rec := doReq(t, h, http.MethodGet, "/api/admin/users", nil, sessionHeader(t, adminToken, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("list users: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var users []User
	if err := json.Unmarshal(rec.Body.Bytes(), &users); err != nil {
		t.Fatalf("decode user list: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("list length = %d, want 2", len(users))
	}
	var listed *User
	for i := range users {
		if users[i].Username == "listed" {
			listed = &users[i]
		}
	}
	if listed == nil {
		t.Fatal("listed user missing from the list")
	}
	if listed.ID != u.ID || listed.Provider != "local" || listed.IsAdmin ||
		listed.Status != UserStatusPending || listed.CreatedAt == "" {
		t.Fatalf("listed user = %+v", listed)
	}
	if listed.Email == nil || *listed.Email != "l@example.com" {
		t.Fatalf("listed email = %v", listed.Email)
	}
	// The admin row carries the fields the UI renders.
	for i := range users {
		if users[i].Username == testAdmin {
			if !users[i].IsAdmin || users[i].Status != UserStatusActive {
				t.Fatalf("admin row = %+v", users[i])
			}
		}
	}
}

func TestAdminCreateUser(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	adminToken := adminSession(t, h, testAdmin, testPassword)

	rec := doReq(t, h, http.MethodPost, "/api/admin/users",
		map[string]interface{}{
			"username": "created", "password": "password-123",
			"email": "c@example.com", "is_admin": false,
		},
		sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: got %d, want 201: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["status"] != "active" || body["provider"] != "local" || body["is_admin"] != false {
		t.Fatalf("created user body = %v", body)
	}
	if strings.Contains(rec.Body.String(), "password_hash") {
		t.Fatal("create user response leaked password_hash")
	}
	// The new user can log in.
	if rec := login(t, h, "created", "password-123"); rec.Code != http.StatusOK {
		t.Fatalf("login created user: got %d, want 200: %s", rec.Code, rec.Body.String())
	}

	// Validation and conflict cases.
	rec = doReq(t, h, http.MethodPost, "/api/admin/users",
		map[string]interface{}{"username": "x", "password": "password-123"},
		sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create bad username: got %d, want 400", rec.Code)
	}
	rec = doReq(t, h, http.MethodPost, "/api/admin/users",
		map[string]interface{}{"username": "another", "password": "short-1"},
		sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create short password: got %d, want 400", rec.Code)
	}
	rec = doReq(t, h, http.MethodPost, "/api/admin/users",
		map[string]interface{}{"username": testAdmin, "password": "password-123"},
		sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusConflict {
		t.Fatalf("create taken username: got %d, want 409", rec.Code)
	}
}

func TestAdminPatchUser(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	adminToken := adminSession(t, h, testAdmin, testPassword)

	u, err := svc.CreateLocalUser("patched", "password-123", "", false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}

	// Profile fields.
	rec := doReq(t, h, http.MethodPatch, "/api/admin/users/"+u.ID,
		map[string]interface{}{"email": "p@example.com", "display_name": "Patched"},
		sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("patch profile: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["email"] != "p@example.com" || body["display_name"] != "Patched" || body["status"] != "active" {
		t.Fatalf("patched body = %v", body)
	}

	// Grant admin rights.
	rec = doReq(t, h, http.MethodPatch, "/api/admin/users/"+u.ID,
		map[string]interface{}{"is_admin": true}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("grant admin: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	updated, err := svc.GetUserByID(u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if !updated.IsAdmin {
		t.Fatal("is_admin should be true after patch")
	}

	// Suspend as pending, then re-activate.
	for _, status := range []string{UserStatusPending, UserStatusActive} {
		rec = doReq(t, h, http.MethodPatch, "/api/admin/users/"+u.ID,
			map[string]string{"status": status}, sessionHeader(t, adminToken, true))
		if rec.Code != http.StatusOK {
			t.Fatalf("patch status %s: got %d, want 200: %s", status, rec.Code, rec.Body.String())
		}
		updated, err = svc.GetUserByID(u.ID)
		if err != nil {
			t.Fatalf("GetUserByID: %v", err)
		}
		if updated.Status != status {
			t.Fatalf("status = %q, want %q", updated.Status, status)
		}
	}

	// An invalid status is rejected.
	rec = doReq(t, h, http.MethodPatch, "/api/admin/users/"+u.ID,
		map[string]string{"status": "frozen"}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch invalid status: got %d, want 400", rec.Code)
	}

	// Unknown user.
	rec = doReq(t, h, http.MethodPatch, "/api/admin/users/"+strings.Repeat("0", 36),
		map[string]string{"status": "active"}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("patch unknown user: got %d, want 404", rec.Code)
	}
}

func TestAdminResetPassword(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	adminToken := adminSession(t, h, testAdmin, testPassword)

	u, err := svc.CreateLocalUser("reset", "old-password-1", "", false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	// The user is signed in; the reset must invalidate that session.
	token := cookieValue(t, login(t, h, "reset", "old-password-1"), SessionCookieName)

	rec := doReq(t, h, http.MethodPost, "/api/admin/users/"+u.ID+"/password",
		map[string]string{"new_password": "new-password-2"}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("reset password: got %d, want 204: %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/auth/me", nil, sessionHeader(t, token, false))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old session after reset: got %d, want 401", rec.Code)
	}
	if rec := login(t, h, "reset", "new-password-2"); rec.Code != http.StatusOK {
		t.Fatalf("login with new password: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := login(t, h, "reset", "old-password-1"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old password still works: got %d, want 401", rec.Code)
	}

	// External-provider users have no local password to reset.
	ext := &User{ID: newID(), Username: "sso-only", Provider: "oidc", Subject: strPtrOrNil("sub-1")}
	if err := svc.CreateUser(ext); err != nil {
		t.Fatalf("create external user: %v", err)
	}
	rec = doReq(t, h, http.MethodPost, "/api/admin/users/"+ext.ID+"/password",
		map[string]string{"new_password": "new-password-3"}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("reset external password: got %d, want 403: %s", rec.Code, rec.Body.String())
	}

	// Short new password.
	rec = doReq(t, h, http.MethodPost, "/api/admin/users/"+u.ID+"/password",
		map[string]string{"new_password": "short-1"}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("reset short password: got %d, want 400", rec.Code)
	}
}

func TestAdminDeleteUser(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	adminToken := adminSession(t, h, testAdmin, testPassword)

	u, err := svc.CreateLocalUser("doomed", "password-123", "", false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	token := cookieValue(t, login(t, h, "doomed", "password-123"), SessionCookieName)

	rec := doReq(t, h, http.MethodDelete, "/api/admin/users/"+u.ID, nil, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete user: got %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if _, err := svc.GetUserByID(u.ID); !errors.Is(err, ErrNoUser) {
		t.Fatal("user still exists after delete")
	}
	rec = doReq(t, h, http.MethodGet, "/api/auth/me", nil, sessionHeader(t, token, false))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("deleted user session: got %d, want 401", rec.Code)
	}

	// Deleting oneself is refused (with a second admin present, so the
	// last-admin rule does not mask the self rule).
	rec = doReq(t, h, http.MethodPost, "/api/admin/users",
		map[string]interface{}{"username": "backstop-admin", "password": "password-123", "is_admin": true},
		sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create backstop admin: got %d, want 201: %s", rec.Code, rec.Body.String())
	}
	adminUser, err := svc.GetUserByUsername(testAdmin)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}
	rec = doReq(t, h, http.MethodDelete, "/api/admin/users/"+adminUser.ID, nil, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete self: got %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeBody(t, rec)["error"]; msg != "cannot delete your own account" {
		t.Fatalf("delete self error = %v", msg)
	}

	// Unknown user.
	rec = doReq(t, h, http.MethodDelete, "/api/admin/users/"+strings.Repeat("0", 36), nil, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown user: got %d, want 404", rec.Code)
	}
}

func TestLastAdminProtections(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	adminToken := adminSession(t, h, testAdmin, testPassword)
	adminUser, err := svc.GetUserByUsername(testAdmin)
	if err != nil {
		t.Fatalf("lookup admin: %v", err)
	}

	// While the admin is the only active admin, every protection fires.
	patch := func(body interface{}) *httptest.ResponseRecorder {
		return doReq(t, h, http.MethodPatch, "/api/admin/users/"+adminUser.ID, body,
			sessionHeader(t, adminToken, true))
	}
	if rec := patch(map[string]interface{}{"is_admin": false}); rec.Code != http.StatusConflict {
		t.Fatalf("demote last admin: got %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeBody(t, patch(map[string]interface{}{"is_admin": false}))["error"]; msg != "cannot demote the last active admin" {
		t.Fatalf("demote last admin error = %v", msg)
	}
	if rec := patch(map[string]string{"status": UserStatusDisabled}); rec.Code != http.StatusConflict {
		t.Fatalf("disable last admin: got %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeBody(t, patch(map[string]string{"status": UserStatusPending}))["error"]; msg != "cannot disable the last active admin" {
		t.Fatalf("pending last admin error = %v", msg)
	}
	if rec := doReq(t, h, http.MethodDelete, "/api/admin/users/"+adminUser.ID, nil,
		sessionHeader(t, adminToken, true)); rec.Code != http.StatusConflict {
		t.Fatalf("delete last admin: got %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if msg := decodeBody(t, doReq(t, h, http.MethodDelete, "/api/admin/users/"+adminUser.ID, nil,
		sessionHeader(t, adminToken, true)))["error"]; msg != "cannot delete the last active admin" {
		t.Fatalf("delete last admin error = %v", msg)
	}

	// A second active admin lifts the protections: the first admin may be
	// demoted and deleted again.
	rec := doReq(t, h, http.MethodPost, "/api/admin/users",
		map[string]interface{}{"username": "second-admin", "password": "password-123", "is_admin": true},
		sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create second admin: got %d, want 201: %s", rec.Code, rec.Body.String())
	}
	secondToken := adminSession(t, h, "second-admin", "password-123")

	if rec := patch(map[string]interface{}{"is_admin": false}); rec.Code != http.StatusOK {
		t.Fatalf("demote with a second admin: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	// The demoted admin's session no longer counts as admin; the second
	// admin performs the delete.
	if rec := doReq(t, h, http.MethodDelete, "/api/admin/users/"+adminUser.ID, nil,
		sessionHeader(t, secondToken, true)); rec.Code != http.StatusNoContent {
		t.Fatalf("delete with a second admin: got %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if _, err := svc.GetUserByID(adminUser.ID); !errors.Is(err, ErrNoUser) {
		t.Fatal("first admin still exists after delete")
	}
	if n, err := svc.CountActiveAdmins(); err != nil || n != 1 {
		t.Fatalf("active admins after delete = %d (err %v), want 1", n, err)
	}
}

func TestDisableDeletesSessions(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	adminToken := adminSession(t, h, testAdmin, testPassword)

	u, err := svc.CreateLocalUser("suspended", "password-123", "", false)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	token := cookieValue(t, login(t, h, "suspended", "password-123"), SessionCookieName)

	rec := doReq(t, h, http.MethodPatch, "/api/admin/users/"+u.ID,
		map[string]string{"status": UserStatusDisabled}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("disable user: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/auth/me", nil, sessionHeader(t, token, false))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("session after disable: got %d, want 401", rec.Code)
	}
	// Re-enabling lets the user log in again.
	rec = doReq(t, h, http.MethodPatch, "/api/admin/users/"+u.ID,
		map[string]string{"status": UserStatusActive}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusOK {
		t.Fatalf("enable user: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec := login(t, h, "suspended", "password-123"); rec.Code != http.StatusOK {
		t.Fatalf("login after enable: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestAdminSettings(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	adminToken := adminSession(t, h, testAdmin, testPassword)

	rec := doReq(t, h, http.MethodGet, "/api/admin/settings", nil, sessionHeader(t, adminToken, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("get settings: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if body := decodeBody(t, rec); body["registration_mode"] != "admin_only" {
		t.Fatalf("settings body = %v", body)
	}

	rec = doReq(t, h, http.MethodPut, "/api/admin/settings",
		map[string]string{"registration_mode": "approval"}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("put settings: got %d, want 204: %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodGet, "/api/admin/settings", nil, sessionHeader(t, adminToken, false))
	if body := decodeBody(t, rec); body["registration_mode"] != "approval" {
		t.Fatalf("settings after put = %v", body)
	}

	// Invalid and missing values are rejected.
	for _, body := range []string{"invalid", "open_registration"} {
		rec = doReq(t, h, http.MethodPut, "/api/admin/settings",
			map[string]string{"registration_mode": body}, sessionHeader(t, adminToken, true))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("put settings %q: got %d, want 400", body, rec.Code)
		}
	}
	rec = doReq(t, h, http.MethodPut, "/api/admin/settings",
		map[string]string{}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("put settings missing mode: got %d, want 400", rec.Code)
	}
}

// auditRows returns the audit_log rows for action, oldest first.
func auditRows(t *testing.T, svc *Service, action string) []struct {
	Actor  string `db:"actor"`
	Target string `db:"target"`
	Detail string `db:"detail"`
} {
	t.Helper()
	var rows []struct {
		Actor  string `db:"actor"`
		Target string `db:"target"`
		Detail string `db:"detail"`
	}
	err := svc.db.Select(&rows, `SELECT actor, target, detail FROM audit_log WHERE action = ? ORDER BY id`, action)
	if err != nil {
		t.Fatalf("select audit rows: %v", err)
	}
	return rows
}

func TestAdminAPIWritesAuditRows(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)
	adminToken := adminSession(t, h, testAdmin, testPassword)

	// Create.
	rec := doReq(t, h, http.MethodPost, "/api/admin/users",
		map[string]interface{}{"username": "audited", "password": "password-123", "is_admin": false},
		sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create user: got %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if rows := auditRows(t, svc, AuditActionCreateUser); len(rows) != 1 ||
		rows[0].Actor != testAdmin || rows[0].Target != "audited" {
		t.Fatalf("create audit rows = %+v", rows)
	}

	// Patch: demote the new admin and disable it.
	u, err := svc.GetUserByUsername("audited")
	if err != nil {
		t.Fatalf("lookup audited: %v", err)
	}
	for _, body := range []interface{}{
		map[string]interface{}{"is_admin": true},
		map[string]interface{}{"is_admin": false, "status": UserStatusDisabled},
	} {
		rec = doReq(t, h, http.MethodPatch, "/api/admin/users/"+u.ID, body, sessionHeader(t, adminToken, true))
		if rec.Code != http.StatusOK {
			t.Fatalf("patch user: got %d, want 200: %s", rec.Code, rec.Body.String())
		}
	}
	patches := auditRows(t, svc, AuditActionPatchUser)
	if len(patches) != 2 || patches[0].Target != "audited" ||
		patches[0].Detail != "is_admin=true" ||
		patches[1].Detail != "is_admin=false status=disabled" {
		t.Fatalf("patch audit rows = %+v", patches)
	}

	// Password reset.
	rec = doReq(t, h, http.MethodPost, "/api/admin/users/"+u.ID+"/password",
		map[string]string{"new_password": "new-password-2"}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("reset password: got %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if rows := auditRows(t, svc, AuditActionResetPassword); len(rows) != 1 || rows[0].Target != "audited" {
		t.Fatalf("reset audit rows = %+v", rows)
	}

	// Registration mode change.
	rec = doReq(t, h, http.MethodPut, "/api/admin/settings",
		map[string]string{"registration_mode": "approval"}, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("put settings: got %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if rows := auditRows(t, svc, AuditActionRegistrationMode); len(rows) != 1 ||
		rows[0].Detail != "mode=approval" {
		t.Fatalf("settings audit rows = %+v", rows)
	}

	// Delete.
	rec = doReq(t, h, http.MethodDelete, "/api/admin/users/"+u.ID, nil, sessionHeader(t, adminToken, true))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete user: got %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if rows := auditRows(t, svc, AuditActionDeleteUser); len(rows) != 1 || rows[0].Target != "audited" {
		t.Fatalf("delete audit rows = %+v", rows)
	}
}

func TestRegistrationModeFromEnv(t *testing.T) {
	for mode, want := range map[string]string{
		"open":       RegistrationModeOpen,
		"approval":   RegistrationModeApproval,
		"admin_only": RegistrationModeAdminOnly,
		"bogus":      RegistrationModeAdminOnly, // invalid values fall back
		"":           RegistrationModeAdminOnly, // unset falls back
	} {
		t.Run("env="+mode, func(t *testing.T) {
			t.Setenv("REGISTRATION_MODE", mode)
			t.Setenv("ADMIN_PASSWORD", testPassword)
			cfg := LoadConfig()
			if mode != "" && cfg.RegistrationMode != mode {
				t.Fatalf("LoadConfig RegistrationMode = %q, want %q", cfg.RegistrationMode, mode)
			}
			if mode == "" && cfg.RegistrationMode != RegistrationModeAdminOnly {
				t.Fatalf("LoadConfig RegistrationMode = %q, want the default", cfg.RegistrationMode)
			}
			svc, err := NewService(cfg, newTestDB(t))
			if err != nil {
				t.Fatalf("NewService: %v", err)
			}
			got, err := svc.GetRegistrationMode()
			if err != nil {
				t.Fatalf("GetRegistrationMode: %v", err)
			}
			if got != want {
				t.Fatalf("seeded mode = %q, want %q", got, want)
			}
			// A second service on the same database must not overwrite
			// the stored value, even if the environment changed.
			t.Setenv("REGISTRATION_MODE", "bogus")
			svc2, err := NewService(LoadConfig(), svc.DB())
			if err != nil {
				t.Fatalf("second NewService: %v", err)
			}
			if got, _ := svc2.GetRegistrationMode(); got != want {
				t.Fatalf("mode after re-seed = %q, want %q (DB value must win)", got, want)
			}
		})
	}
}

// The last-active-admin invariant under a single request: demoting,
// disabling or deleting the only active admin is rejected with
// ErrLastActiveAdmin and the user is left untouched.
func TestUpdateUserRejectsLastActiveAdmin(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	last, err := svc.GetUserByUsername(testAdmin)
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}

	noAdmin := false
	if err := svc.UpdateUser(last.ID, UserPatch{IsAdmin: &noAdmin}); !errors.Is(err, ErrLastActiveAdmin) {
		t.Fatalf("demote last admin err = %v, want ErrLastActiveAdmin", err)
	}
	disabled := UserStatusDisabled
	if err := svc.UpdateUser(last.ID, UserPatch{Status: &disabled}); !errors.Is(err, ErrLastActiveAdmin) {
		t.Fatalf("disable last admin err = %v, want ErrLastActiveAdmin", err)
	}
	still, err := svc.GetUserByID(last.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if !still.IsAdmin || still.Status != UserStatusActive {
		t.Fatalf("last admin state = %+v, want admin+active", still)
	}

	// A second active admin makes the demotion possible again.
	if _, err := svc.CreateLocalUser("co-admin", "password-123", "", true); err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}
	if err := svc.UpdateUser(last.ID, UserPatch{IsAdmin: &noAdmin}); err != nil {
		t.Fatalf("demote with a co-admin present: %v", err)
	}
}

func TestDeleteUserRejectsLastActiveAdmin(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	last, err := svc.GetUserByUsername(testAdmin)
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if err := svc.DeleteUser(last.ID); !errors.Is(err, ErrLastActiveAdmin) {
		t.Fatalf("delete last admin err = %v, want ErrLastActiveAdmin", err)
	}
	if _, err := svc.GetUserByID(last.ID); err != nil {
		t.Fatalf("last admin must survive: %v", err)
	}
}

// Regression for the check-then-act race: two concurrent patches that
// each demote/disable the other of the two active admins used to both
// pass the out-of-transaction count check and leave zero active admins.
// The in-transaction re-check must reject exactly one of them.
func TestLastActiveAdminConcurrentDemotion(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	a, err := svc.GetUserByUsername(testAdmin)
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	b, err := svc.CreateLocalUser("second-admin", "password-123", "", true)
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		disabled := UserStatusDisabled
		errs <- svc.UpdateUser(b.ID, UserPatch{Status: &disabled})
	}()
	go func() {
		defer wg.Done()
		noAdmin := false
		errs <- svc.UpdateUser(a.ID, UserPatch{IsAdmin: &noAdmin})
	}()
	wg.Wait()
	close(errs)

	failed := 0
	for err := range errs {
		if err != nil {
			failed++
		}
	}
	n, err := svc.CountActiveAdmins()
	if err != nil {
		t.Fatalf("CountActiveAdmins: %v", err)
	}
	if n < 1 {
		t.Fatalf("active admins after concurrent demotion = %d, want >= 1", n)
	}
	if failed != 1 {
		t.Fatalf("rejected patches = %d, want exactly 1", failed)
	}
}
