package auth

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
)

// ProviderInfo describes one external identity provider in the
// /auth/providers response.
type ProviderInfo struct {
	Enabled bool   `json:"enabled"`
	Name    string `json:"name"`
}

// Providers is the shape of GET /auth/providers. T4 (OIDC) and T5 (SAML)
// fill their fields via SetOIDCProvider / SetSAMLProvider.
type Providers struct {
	Enabled bool         `json:"enabled"`
	Local   bool         `json:"local"`
	OIDC    ProviderInfo `json:"oidc"`
	SAML    ProviderInfo `json:"saml"`
	// Registration is the current registration mode (admin_only, open or
	// approval); the login page uses it to decide whether to offer a
	// "Create account" link.
	Registration string `json:"registration"`
	// SetupRequired is true while the first-run setup wizard is pending
	// (no users exist yet); the frontend sends every route to /setup.
	SetupRequired bool `json:"setup_required"`
}

// providerState holds the mutable provider registrations.
type providerState struct {
	mu   sync.RWMutex
	oidc ProviderInfo
	saml ProviderInfo
}

func newProviderState() *providerState { return &providerState{} }

func (p *providerState) snapshot(cfg Config) Providers {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return Providers{
		Enabled: cfg.Enabled,
		Local:   cfg.LocalEnabled,
		OIDC:    p.oidc,
		SAML:    p.saml,
	}
}

// SetOIDCProvider registers the OIDC provider name and enables it in the
// /auth/providers response. Called by T4.
func (s *Service) SetOIDCProvider(name string) {
	s.providers.mu.Lock()
	defer s.providers.mu.Unlock()
	s.providers.oidc = ProviderInfo{Enabled: true, Name: name}
}

// SetSAMLProvider registers the SAML provider name and enables it in the
// /auth/providers response. Called by T5.
func (s *Service) SetSAMLProvider(name string) {
	s.providers.mu.Lock()
	defer s.providers.mu.Unlock()
	s.providers.saml = ProviderInfo{Enabled: true, Name: name}
}

// Login rate limiting: max failures per IP per window, then requests get
// 429. The login and register endpoints share loginFailureLimit; the
// first-run setup endpoint gets the stricter setupFailureLimit.
const (
	loginFailureLimit  = 10
	loginFailureWindow = 15 * time.Minute
	setupFailureLimit  = 5
)

type loginLimiter struct {
	limit int
	mu    sync.Mutex
	fails map[string][]time.Time
}

func newLoginLimiter(limit int) *loginLimiter {
	return &loginLimiter{limit: limit, fails: make(map[string][]time.Time)}
}

// blocked reports whether ip has reached the failure limit inside the window.
func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pruneLocked(ip, time.Now())) >= l.limit
}

// record adds a failed attempt for ip.
func (l *loginLimiter) record(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.fails[ip] = append(l.pruneLocked(ip, now), now)
}

// reset clears the failure history for ip (after a successful login).
func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, ip)
}

// clearAll drops the failure history for every IP.
func (l *loginLimiter) clearAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails = make(map[string][]time.Time)
}

// ClearLoginRateLimit clears the login rate limiter. The limiter is in
// memory per process: this call affects only this process, and restarting
// the server clears it as well. The admin CLI exposes it as
// `server admin unlock`.
func (s *Service) ClearLoginRateLimit() {
	s.limiter.clearAll()
}

// pruneLocked drops failures older than the window. Caller holds l.mu.
func (l *loginLimiter) pruneLocked(ip string, now time.Time) []time.Time {
	cutoff := now.Add(-loginFailureWindow)
	recent := l.fails[ip][:0]
	for _, t := range l.fails[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(l.fails, ip)
		return nil
	}
	l.fails[ip] = recent
	return recent
}

// dummyHash equalizes bcrypt timing for unknown users.
var dummyHash string

func init() {
	if h, err := HashPassword("timing-equalizer-dummy-password"); err == nil {
		dummyHash = h
	}
}

// RegisterRoutes mounts the auth API on the /api subrouter. The OIDC routes
// are mounted only when OIDC has been enabled at startup.
func (s *Service) RegisterRoutes(r *mux.Router) {
	r.HandleFunc("/auth/providers", s.handleProviders).Methods(http.MethodGet)
	r.HandleFunc("/auth/login", s.handleLogin).Methods(http.MethodPost)
	r.HandleFunc("/auth/logout", s.handleLogout).Methods(http.MethodPost)
	r.HandleFunc("/auth/me", s.handleMe).Methods(http.MethodGet)
	r.HandleFunc("/auth/password", s.handleChangePassword).Methods(http.MethodPost)
	r.HandleFunc("/auth/register", s.handleRegister).Methods(http.MethodPost)
	s.registerSetupRoutes(r)
	s.registerAdminRoutes(r)
	if s.oidc != nil {
		s.registerOIDCRoutes(r)
	}
}

// handleProviders: GET /api/auth/providers
func (s *Service) handleProviders(w http.ResponseWriter, _ *http.Request) {
	p := s.providers.snapshot(s.cfg)
	mode, err := s.GetRegistrationMode()
	if err != nil {
		log.Printf("[auth] read registration mode: %v", err)
		mode = RegistrationModeAdminOnly
	}
	p.Registration = mode
	p.SetupRequired = s.SetupRequired()
	writeJSON(w, http.StatusOK, p)
}

// usernameRegexp is the shared username rule for local accounts
// (self-registration and admin-created users).
var usernameRegexp = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,32}$`)

// ValidUsername reports whether username matches the local-account rule
// shared by self-registration, admin-created users and the setup wizard.
func ValidUsername(username string) bool {
	return usernameRegexp.MatchString(username)
}

// MinPasswordLength is the shortest accepted password.
const MinPasswordLength = 8

// handleRegister: POST /api/auth/register (public).
// Creates a local user in open or approval mode; in admin_only mode the
// endpoint answers 403. Uses the same in-memory failure limiter as login.
func (s *Service) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !s.cfg.LocalEnabled {
		writeJSONError(w, http.StatusForbidden, "local login disabled")
		return
	}

	mode, err := s.GetRegistrationMode()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if mode == RegistrationModeAdminOnly {
		writeJSONError(w, http.StatusForbidden, "registration is disabled")
		return
	}

	ip := clientIP(r)
	if s.limiter.blocked(ip) {
		writeJSONError(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
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
		s.limiter.record(ip)
		writeJSONError(w, http.StatusConflict, "username already taken")
		return
	} else if !errors.Is(err, ErrNoUser) {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	status := UserStatusActive
	if mode == RegistrationModeApproval {
		status = UserStatusPending
	}
	u := &User{
		ID:       newID(),
		Username: req.Username,
		Provider: "local",
		Email:    strPtrOrNil(req.Email),
		Status:   status,
	}
	hash, err := HashPassword(req.Password)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	u.PasswordHash = &hash
	if err := s.CreateUser(u); err != nil {
		// Concurrent registration of the same username loses the race.
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			s.limiter.record(ip)
			writeJSONError(w, http.StatusConflict, "username already taken")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Open mode starts a session immediately; approval mode waits for an
	// admin, so the browser gets no session cookie.
	if mode == RegistrationModeOpen {
		if err := s.StartSession(w, u.ID); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}
	s.limiter.reset(ip)
	writeJSON(w, http.StatusCreated, map[string]string{"status": status})
}

// handleLogin: POST /api/auth/login
func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !s.cfg.LocalEnabled {
		writeJSONError(w, http.StatusForbidden, "local login disabled")
		return
	}

	ip := clientIP(r)
	if s.limiter.blocked(ip) {
		writeJSONError(w, http.StatusTooManyRequests, "too many failed attempts, try again later")
		return
	}

	user, err := s.GetUserByUsername(req.Username)
	var valid bool
	switch {
	case errors.Is(err, ErrNoUser):
		// Unknown user: still run bcrypt against a dummy hash so response
		// timing does not reveal whether the username exists.
		valid = CheckPassword(dummyHash, req.Password)
	case err != nil:
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	case user.PasswordHash == nil:
		// External-provider user with no local password.
		valid = CheckPassword(dummyHash, req.Password)
	default:
		valid = CheckPassword(*user.PasswordHash, req.Password)
	}

	if !valid {
		s.limiter.record(ip)
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// The password is correct; only now is the account status revealed,
	// so a wrong password never leaks whether the account is pending or
	// disabled. No session is created for non-active users.
	switch user.Status {
	case UserStatusPending:
		writeJSONError(w, http.StatusForbidden, "account pending approval")
		return
	case UserStatusDisabled:
		writeJSONError(w, http.StatusForbidden, "account disabled")
		return
	}

	s.limiter.reset(ip)
	if err := s.TouchLastLogin(user.ID); err != nil {
		log.Printf("[auth] touch last login: %v", err)
	}
	if err := s.StartSession(w, user.ID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// handleLogout: POST /api/auth/logout
func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.DestroySession(w, r); err != nil {
		log.Printf("[auth] logout: %v", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMe: GET /api/auth/me
func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Enabled {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"id":       "anonymous",
			"username": "anonymous",
			"provider": "none",
			"is_admin": true,
		})
		return
	}
	u := UserFromContext(r.Context())
	if u == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// handleChangePassword: POST /api/auth/password (local users only)
func (s *Service) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	u := UserFromContext(r.Context())
	if u == nil {
		writeJSONError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if u.Provider != "local" {
		writeJSONError(w, http.StatusForbidden, "password change is only available for local users")
		return
	}
	if len(req.NewPassword) < 8 {
		writeJSONError(w, http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	if u.PasswordHash == nil || !CheckPassword(*u.PasswordHash, req.CurrentPassword) {
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if err := s.UpdatePassword(u.ID, hash); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// clientIP extracts the peer IP from RemoteAddr.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[auth] writing response: %v", err)
	}
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
