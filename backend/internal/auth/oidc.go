package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gorilla/mux"
	"golang.org/x/oauth2"
)

// OIDCConfig holds the settings for the OIDC login flow, loaded from the
// OIDC_* environment variables.
type OIDCConfig struct {
	Enabled      bool
	Name         string
	Issuer       string
	ClientID     string
	ClientSecret string
	Scopes       []string
	AdminGroup   string
	GroupsClaim  string
	AutoCreate   bool
}

// LoadOIDCConfig reads OIDC settings from the environment.
func LoadOIDCConfig() OIDCConfig {
	scopes := []string{"openid", "profile", "email"}
	if v := os.Getenv("OIDC_SCOPES"); v != "" {
		parsed := make([]string, 0, 4)
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				parsed = append(parsed, s)
			}
		}
		if len(parsed) > 0 {
			scopes = parsed
		}
	}
	return OIDCConfig{
		Enabled:      envBool("OIDC_ENABLED", false),
		Name:         envStr("OIDC_NAME", "SSO"),
		Issuer:       os.Getenv("OIDC_ISSUER"),
		ClientID:     os.Getenv("OIDC_CLIENT_ID"),
		ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
		Scopes:       scopes,
		AdminGroup:   os.Getenv("OIDC_ADMIN_GROUP"),
		GroupsClaim:  envStr("OIDC_GROUPS_CLAIM", "groups"),
		AutoCreate:   envBool("OIDC_AUTO_CREATE", true),
	}
}

const (
	// oidcStateCookieName binds the OIDC flow to the browser that started it.
	oidcStateCookieName = "jellyide_oidc_state"

	// oidcStateCookieMaxAge is the lifetime of the state cookie in seconds.
	oidcStateCookieMaxAge = 600

	// oidcPendingTTL is how long an in-flight login may take before its
	// pending entry expires.
	oidcPendingTTL = 10 * time.Minute
)

// pendingLogin is one in-flight OIDC login, keyed by its state parameter.
type pendingLogin struct {
	nonce    string
	verifier string // PKCE code verifier
	expires  time.Time
}

// oidcState holds the runtime OIDC state. It is set once by EnableOIDC at
// startup, before route registration, and read-only afterwards.
type oidcState struct {
	verifier *oidc.IDTokenVerifier
	oauth    *oauth2.Config
	cfg      OIDCConfig

	mu       sync.Mutex
	pendings map[string]pendingLogin
}

// EnableOIDC discovers the provider, builds the oauth2 config and the id
// token verifier, and registers the provider for /auth/providers. It must be
// called once at startup, before route registration.
func (s *Service) EnableOIDC(ctx context.Context, c OIDCConfig) error {
	if c.Issuer == "" || c.ClientID == "" || c.ClientSecret == "" {
		return errors.New("oidc: OIDC_ISSUER, OIDC_CLIENT_ID and OIDC_CLIENT_SECRET are required when OIDC is enabled")
	}
	provider, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return fmt.Errorf("oidc: discover provider: %w", err)
	}
	oauthCfg := &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  s.cfg.PublicURL + "/api/auth/oidc/callback",
		Scopes:       c.Scopes,
		Endpoint:     provider.Endpoint(),
	}
	s.oidc = &oidcState{
		verifier: provider.Verifier(&oidc.Config{ClientID: c.ClientID}),
		oauth:    oauthCfg,
		cfg:      c,
		pendings: make(map[string]pendingLogin),
	}
	s.SetOIDCProvider(c.Name)
	return nil
}

// oidcStateCookie builds the short-lived cookie that binds the OIDC flow to
// the browser that started it.
func (s *Service) oidcStateCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // G124 - the Secure flag is deliberately config-driven (COOKIE_SECURE) so local http dev keeps working
		Name:     oidcStateCookieName,
		Value:    value,
		Path:     "/api/auth/oidc",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
		Secure:   s.cfg.CookieSecure,
	}
}

// randomOIDCToken returns 32 random bytes, base64url-encoded without
// padding.
func randomOIDCToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// newPendingLogin creates the state/nonce/PKCE-verifier triple for a new
// login, stores it with a 10-minute TTL and prunes expired entries.
func (s *Service) newPendingLogin() (state, nonce, verifier string, err error) {
	if state, err = randomOIDCToken(); err != nil {
		return "", "", "", err
	}
	if nonce, err = randomOIDCToken(); err != nil {
		return "", "", "", err
	}
	if verifier, err = randomOIDCToken(); err != nil {
		return "", "", "", err
	}
	o := s.oidc
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pruneLocked(time.Now())
	o.pendings[state] = pendingLogin{nonce: nonce, verifier: verifier, expires: time.Now().Add(oidcPendingTTL)}
	return state, nonce, verifier, nil
}

// takePendingLogin removes and returns the pending entry for state
// (single-use). Expired or unknown states report ok=false.
func (s *Service) takePendingLogin(state string) (pendingLogin, bool) {
	o := s.oidc
	o.mu.Lock()
	defer o.mu.Unlock()
	p, ok := o.pendings[state]
	delete(o.pendings, state)
	if !ok || time.Now().After(p.expires) {
		return pendingLogin{}, false
	}
	return p, true
}

// pruneLocked drops expired pending entries. The caller must hold o.mu.
func (o *oidcState) pruneLocked(now time.Time) {
	for k, p := range o.pendings {
		if now.After(p.expires) {
			delete(o.pendings, k)
		}
	}
}

// handleOIDCLogin: GET /api/auth/oidc/login
// Starts the flow: stores the pending state/nonce/PKCE triple, binds the
// state to the browser via a short cookie, and redirects to the provider.
func (s *Service) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	o := s.oidc
	if o == nil {
		http.Error(w, "sso disabled", http.StatusServiceUnavailable)
		return
	}
	state, nonce, verifier, err := s.newPendingLogin()
	if err != nil {
		log.Printf("[auth] oidc login: generate state: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, s.oidcStateCookie(state, oidcStateCookieMaxAge))
	target := o.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, target, http.StatusFound)
}

// handleOIDCCallback: GET /api/auth/oidc/callback
func (s *Service) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	o := s.oidc
	if o == nil {
		http.Error(w, "sso disabled", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()

	// 1. The state must match the query, the binding cookie and a live
	// pending entry; the entry is consumed on use.
	state := q.Get("state")
	cookie, cookieErr := r.Cookie(oidcStateCookieName)
	if state == "" || cookieErr != nil || state != cookie.Value {
		s.failOIDC(w, r, "invalid sso state")
		return
	}
	pending, ok := s.takePendingLogin(state)
	if !ok {
		s.failOIDC(w, r, "unknown or expired sso state")
		return
	}

	// 2. The provider redirected with an error instead of a code. The raw
	// value is not logged because it is untrusted input (log injection).
	if q.Get("error") != "" {
		log.Printf("[auth] oidc callback: provider returned an error")
		s.failOIDC(w, r, "sso login failed")
		return
	}

	// 3. Exchange the code (PKCE), verify the id_token, check the nonce.
	code := q.Get("code")
	if code == "" {
		s.failOIDC(w, r, "missing sso code")
		return
	}
	token, err := o.oauth.Exchange(r.Context(), code, oauth2.VerifierOption(pending.verifier))
	if err != nil {
		log.Printf("[auth] oidc callback: token exchange: %v", err)
		s.failOIDC(w, r, "sso login failed")
		return
	}
	rawIDToken, isStr := token.Extra("id_token").(string)
	if !isStr || rawIDToken == "" {
		log.Printf("[auth] oidc callback: token response has no id_token")
		s.failOIDC(w, r, "sso login failed")
		return
	}
	idToken, err := o.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		log.Printf("[auth] oidc callback: verify id_token: %v", err)
		s.failOIDC(w, r, "sso login failed")
		return
	}
	if idToken.Nonce != pending.nonce {
		log.Printf("[auth] oidc callback: nonce mismatch")
		s.failOIDC(w, r, "sso login failed")
		return
	}

	// 4. Read the claims.
	var claims map[string]interface{}
	if err := idToken.Claims(&claims); err != nil {
		log.Printf("[auth] oidc callback: decode claims: %v", err)
		s.failOIDC(w, r, "sso login failed")
		return
	}
	sub, _ := claims["sub"].(string)
	email, _ := claims["email"].(string)
	username, _ := claims["preferred_username"].(string)
	displayName, _ := claims["name"].(string)
	var groups []string
	if rawGroups, ok := claims[o.cfg.GroupsClaim].([]interface{}); ok {
		for _, g := range rawGroups {
			if gs, ok := g.(string); ok {
				groups = append(groups, gs)
			}
		}
	}
	if sub == "" {
		log.Printf("[auth] oidc callback: id_token has no sub claim")
		s.failOIDC(w, r, "sso login failed")
		return
	}

	// 5. Username: preferred_username, else the email local-part, else sub.
	if username == "" && email != "" {
		if at := strings.IndexByte(email, '@'); at > 0 {
			username = email[:at]
		}
	}
	if username == "" {
		username = sub
	}

	// 6. isAdmin from the configured admin group.
	isAdmin := false
	if o.cfg.AdminGroup != "" {
		for _, g := range groups {
			if g == o.cfg.AdminGroup {
				isAdmin = true
				break
			}
		}
	}

	// 7. Resolve (or provision) the local user, and 8. sync the identity on
	// every login, start a session, and go home.
	var user *User
	if o.cfg.AutoCreate {
		user, err = s.FindOrCreateExternalUser("oidc", sub, username, email, displayName, isAdmin)
	} else {
		user, err = s.GetUserByProviderSubject("oidc", sub)
		if errors.Is(err, ErrNoUser) {
			log.Printf("[auth] oidc callback: subject %s not provisioned and auto-create is off", sub)
			s.failOIDC(w, r, "account not provisioned")
			return
		}
	}
	if err != nil {
		log.Printf("[auth] oidc callback: resolve user: %v", err)
		s.failOIDC(w, r, "internal error")
		return
	}
	if err := s.UpdateExternalIdentity(user.ID, email, displayName, isAdmin); err != nil {
		log.Printf("[auth] oidc callback: update identity: %v", err)
	}
	if err := s.TouchLastLogin(user.ID); err != nil {
		log.Printf("[auth] oidc callback: touch last login: %v", err)
	}
	if err := s.StartSession(w, user.ID); err != nil {
		log.Printf("[auth] oidc callback: start session: %v", err)
		s.failOIDC(w, r, "internal error")
		return
	}
	http.SetCookie(w, s.oidcStateCookie("", -1))
	http.Redirect(w, r, "/", http.StatusFound)
}

// failOIDC logs the detail (never tokens or codes) and redirects to
// /login with a short, generic, url-escaped error message.
func (s *Service) failOIDC(w http.ResponseWriter, r *http.Request, msg string) {
	log.Printf("[auth] oidc callback: %s", msg)
	http.Redirect(w, r, "/login?error="+url.QueryEscape(msg), http.StatusFound)
}

// registerOIDCRoutes mounts the OIDC routes. Callers must only invoke this
// when OIDC is enabled.
func (s *Service) registerOIDCRoutes(r *mux.Router) {
	r.HandleFunc("/auth/oidc/login", s.handleOIDCLogin).Methods(http.MethodGet)
	r.HandleFunc("/auth/oidc/callback", s.handleOIDCCallback).Methods(http.MethodGet)
}
