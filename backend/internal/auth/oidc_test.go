package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	josejwt "github.com/go-jose/go-jose/v4/jwt"
)

// fakeOIDCProvider is an in-process OIDC provider: it serves the discovery
// document, an RSA JWK set and a /token endpoint that answers with a
// RS256-signed id_token carrying whatever nonce the test asks for.
type fakeOIDCProvider struct {
	t            *testing.T
	server       *httptest.Server
	key          *rsa.PrivateKey
	kid          string
	clientID     string
	clientSecret string

	sub, email, username, name string
	claims                     map[string]interface{}

	mu            sync.Mutex
	nonce         string // nonce the next id_token must carry
	codeChallenge string // S256 challenge captured from the login redirect
}

func newFakeOIDCProvider(t *testing.T) *fakeOIDCProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	f := &fakeOIDCProvider{
		t:            t,
		key:          key,
		kid:          "test-key-1",
		clientID:     "jellyide-client",
		clientSecret: "jellyide-client-secret",
		sub:          "user-1",
		email:        "alice@example.com",
		username:     "alice",
		name:         "Alice Example",
		claims:       map[string]interface{}{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", f.handleDiscovery)
	mux.HandleFunc("/keys", f.handleKeys)
	mux.HandleFunc("/token", f.handleToken)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeOIDCProvider) issuer() string { return f.server.URL }

func (f *fakeOIDCProvider) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"issuer":                                f.server.URL,
		"authorization_endpoint":                f.server.URL + "/auth",
		"token_endpoint":                        f.server.URL + "/token",
		"jwks_uri":                              f.server.URL + "/keys",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (f *fakeOIDCProvider) handleKeys(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: f.key.Public(), KeyID: f.kid, Algorithm: "RS256", Use: "sig"},
	}})
}

func (f *fakeOIDCProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("code") == "" {
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}
	if r.Form.Get("client_id") != f.clientID || r.Form.Get("client_secret") != f.clientSecret {
		http.Error(w, "bad client credentials", http.StatusUnauthorized)
		return
	}
	// Enforce PKCE: the verifier must hash to the challenge from the login
	// redirect (when the test armed one).
	f.mu.Lock()
	challenge := f.codeChallenge
	nonce := f.nonce
	f.mu.Unlock()
	if challenge != "" {
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			http.Error(w, "code verifier does not match challenge", http.StatusBadRequest)
			return
		}
	}
	idToken, err := f.signIDToken(nonce)
	if err != nil {
		http.Error(w, "signing failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"access_token": "fake-access-token",
		"token_type":   "Bearer",
		"expires_in":   "3600",
		"id_token":     idToken,
	})
}

// signIDToken builds a RS256-signed id_token for the fake provider's key.
func (f *fakeOIDCProvider) signIDToken(nonce string) (string, error) {
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: f.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), f.kid),
	)
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := map[string]interface{}{
		"iss":                f.server.URL,
		"sub":                f.sub,
		"aud":                f.clientID,
		"iat":                now.Unix(),
		"exp":                now.Add(time.Hour).Unix(),
		"nonce":              nonce,
		"email":              f.email,
		"preferred_username": f.username,
		"name":               f.name,
	}
	for k, v := range f.claims {
		claims[k] = v
	}
	return josejwt.Signed(signer).Claims(claims).Serialize()
}

// newOIDCService builds an auth service with the fake provider enabled.
func newOIDCService(t *testing.T, f *fakeOIDCProvider, oc OIDCConfig) *Service {
	t.Helper()
	if oc.Issuer == "" {
		oc.Issuer = f.issuer()
	}
	if oc.ClientID == "" {
		oc.ClientID = f.clientID
	}
	if oc.ClientSecret == "" {
		oc.ClientSecret = f.clientSecret
	}
	if len(oc.Scopes) == 0 {
		oc.Scopes = []string{"openid", "profile", "email"}
	}
	if oc.GroupsClaim == "" {
		oc.GroupsClaim = "groups"
	}
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true, PublicURL: "http://app.example"})
	if err := svc.EnableOIDC(context.Background(), oc); err != nil {
		t.Fatalf("EnableOIDC: %v", err)
	}
	return svc
}

// doOIDCLogin runs GET /api/auth/oidc/login and returns the state, nonce and
// PKCE challenge from the redirect, plus the state cookie.
func doOIDCLogin(t *testing.T, h http.Handler, f *fakeOIDCProvider) (state, nonce, challenge string) {
	t.Helper()
	rec := doReq(t, h, http.MethodGet, "/api/auth/oidc/login", nil, nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("login: got %d, want 302: %s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse login redirect %q: %v", rec.Header().Get("Location"), err)
	}
	if loc.String() != f.issuer()+"/auth" && !strings.HasPrefix(loc.String(), f.issuer()+"/auth?") {
		t.Fatalf("login redirect target = %s, want the provider authorization endpoint", loc)
	}
	q := loc.Query()
	if q.Get("client_id") != f.clientID {
		t.Fatalf("login redirect client_id = %q, want %q", q.Get("client_id"), f.clientID)
	}
	if q.Get("redirect_uri") != "http://app.example/api/auth/oidc/callback" {
		t.Fatalf("login redirect redirect_uri = %q", q.Get("redirect_uri"))
	}
	state, nonce, challenge = q.Get("state"), q.Get("nonce"), q.Get("code_challenge")
	if state == "" || nonce == "" || challenge == "" {
		t.Fatalf("login redirect missing state/nonce/code_challenge: %s", loc)
	}
	if cookie := cookieValue(t, rec, oidcStateCookieName); cookie != state {
		t.Fatalf("state cookie = %q, want %q", cookie, state)
	}
	return state, nonce, challenge
}

// doOIDCCallback arms the fake provider with the nonce and PKCE challenge
// from the login redirect and runs the callback with a valid code.
func doOIDCCallback(t *testing.T, h http.Handler, f *fakeOIDCProvider, state, nonce, challenge string) *httptest.ResponseRecorder {
	t.Helper()
	f.mu.Lock()
	f.nonce = nonce
	f.codeChallenge = challenge
	f.mu.Unlock()
	return doReq(t, h, http.MethodGet,
		"/api/auth/oidc/callback?code=fake-code-123&state="+url.QueryEscape(state),
		nil, http.Header{"Cookie": {oidcStateCookieName + "=" + state}})
}

// assertErrorRedirect checks that the callback redirected to
// /login?error=<non-empty> and set no session cookie.
func assertErrorRedirect(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("callback: got %d, want 302", rec.Code)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse callback redirect %q: %v", rec.Header().Get("Location"), err)
	}
	if loc.Path != "/login" || loc.Query().Get("error") == "" {
		t.Fatalf("callback redirect = %q, want /login?error=<message>", loc)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			t.Fatalf("failing callback set a session cookie: %q", rec.Header().Get("Location"))
		}
	}
}

func TestOIDCLoginFlow(t *testing.T) {
	f := newFakeOIDCProvider(t)
	f.claims["groups"] = []string{"admins"}
	svc := newOIDCService(t, f, OIDCConfig{
		Name: "TestIdP", AdminGroup: "admins", AutoCreate: true,
	})
	h := newTestHandler(svc)

	state, nonce, challenge := doOIDCLogin(t, h, f)
	cback := doOIDCCallback(t, h, f, state, nonce, challenge)

	if cback.Code != http.StatusFound {
		t.Fatalf("callback: got %d, want 302: %s", cback.Code, cback.Body.String())
	}
	if loc := cback.Header().Get("Location"); loc != "/" {
		t.Fatalf("callback redirect = %q, want /", loc)
	}
	session := cookieValue(t, cback, SessionCookieName)
	if session == "" {
		t.Fatal("callback did not set a session cookie")
	}

	// The state cookie is cleared on success.
	cleared := false
	for _, c := range cback.Result().Cookies() {
		if c.Name == oidcStateCookieName {
			cleared = true
			if c.MaxAge >= 0 {
				t.Fatalf("state cookie not cleared: MaxAge = %d", c.MaxAge)
			}
		}
	}
	if !cleared {
		t.Fatal("callback did not clear the oidc state cookie")
	}

	// The session authenticates and carries the provider's identity.
	rec := doReq(t, h, http.MethodGet, "/api/auth/me", nil, sessionHeader(t, session, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("me with sso session: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["username"] != "alice" || body["provider"] != "oidc" {
		t.Fatalf("me body = %v", body)
	}
	if body["email"] != "alice@example.com" || body["display_name"] != "Alice Example" {
		t.Fatalf("me identity = %v", body)
	}
	if body["is_admin"] != true {
		t.Fatalf("me is_admin = %v, want true (admin group matched)", body["is_admin"])
	}

	// The providers endpoint reports the OIDC provider.
	rec = doReq(t, h, http.MethodGet, "/api/auth/providers", nil, nil)
	body = decodeBody(t, rec)
	oidcInfo, _ := body["oidc"].(map[string]interface{})
	if oidcInfo["enabled"] != true || oidcInfo["name"] != "TestIdP" {
		t.Fatalf("providers oidc = %v", oidcInfo)
	}
}

func TestOIDCLoginFlowSingleUseState(t *testing.T) {
	f := newFakeOIDCProvider(t)
	svc := newOIDCService(t, f, OIDCConfig{AutoCreate: true})
	h := newTestHandler(svc)

	state, nonce, challenge := doOIDCLogin(t, h, f)
	rec := doOIDCCallback(t, h, f, state, nonce, challenge)
	if rec.Code != http.StatusFound {
		t.Fatalf("first callback: got %d, want 302", rec.Code)
	}

	// Replaying the same state must fail: pending entries are single-use.
	rec = doReq(t, h, http.MethodGet,
		"/api/auth/oidc/callback?code=fake-code-123&state="+url.QueryEscape(state),
		nil, http.Header{"Cookie": {oidcStateCookieName + "=" + state}})
	assertErrorRedirect(t, rec)
}

func TestOIDCBadState(t *testing.T) {
	f := newFakeOIDCProvider(t)
	svc := newOIDCService(t, f, OIDCConfig{AutoCreate: true})
	h := newTestHandler(svc)

	// Unknown state (never issued) → error redirect.
	rec := doReq(t, h, http.MethodGet,
		"/api/auth/oidc/callback?code=fake-code-123&state=not-a-real-state",
		nil, http.Header{"Cookie": {oidcStateCookieName + "=not-a-real-state"}})
	assertErrorRedirect(t, rec)

	// State query does not match the cookie → error redirect.
	rec = doReq(t, h, http.MethodGet,
		"/api/auth/oidc/callback?code=fake-code-123&state=query-state",
		nil, http.Header{"Cookie": {oidcStateCookieName + "=cookie-state"}})
	assertErrorRedirect(t, rec)

	// Missing state cookie entirely → error redirect.
	rec = doReq(t, h, http.MethodGet,
		"/api/auth/oidc/callback?code=fake-code-123&state=query-state", nil, nil)
	assertErrorRedirect(t, rec)
}

func TestOIDCNonceMismatch(t *testing.T) {
	f := newFakeOIDCProvider(t)
	svc := newOIDCService(t, f, OIDCConfig{AutoCreate: true})
	h := newTestHandler(svc)

	state, _, challenge := doOIDCLogin(t, h, f)

	// The provider signs the id_token with a stale nonce.
	f.mu.Lock()
	f.nonce = "stale-nonce"
	f.codeChallenge = challenge
	f.mu.Unlock()
	rec := doReq(t, h, http.MethodGet,
		"/api/auth/oidc/callback?code=fake-code-123&state="+url.QueryEscape(state),
		nil, http.Header{"Cookie": {oidcStateCookieName + "=" + state}})
	assertErrorRedirect(t, rec)
}

func TestOIDCProviderErrorParam(t *testing.T) {
	f := newFakeOIDCProvider(t)
	svc := newOIDCService(t, f, OIDCConfig{AutoCreate: true})
	h := newTestHandler(svc)

	state, _, _ := doOIDCLogin(t, h, f)

	// The provider redirected back with an error instead of a code.
	rec := doReq(t, h, http.MethodGet,
		"/api/auth/oidc/callback?error=access_denied&state="+url.QueryEscape(state),
		nil, http.Header{"Cookie": {oidcStateCookieName + "=" + state}})
	assertErrorRedirect(t, rec)
}

func TestOIDCAutoCreateDisabled(t *testing.T) {
	f := newFakeOIDCProvider(t)
	svc := newOIDCService(t, f, OIDCConfig{AutoCreate: false})
	h := newTestHandler(svc)

	// Unknown subject without auto-create: "account not provisioned".
	state, nonce, challenge := doOIDCLogin(t, h, f)
	rec := doOIDCCallback(t, h, f, state, nonce, challenge)
	assertErrorRedirect(t, rec)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if msg := loc.Query().Get("error"); msg != "account not provisioned" {
		t.Fatalf("provisioning error message = %q", msg)
	}

	// A provisioned user can still log in, and the identity is synced.
	if err := svc.CreateUser(&User{ID: newID(), Username: "alice", Provider: "oidc", Subject: strPtrOrNil(f.sub)}); err != nil {
		t.Fatalf("create provisioned user: %v", err)
	}
	state, nonce, challenge = doOIDCLogin(t, h, f)
	rec = doOIDCCallback(t, h, f, state, nonce, challenge)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("provisioned callback: got %d %q, want 302 /", rec.Code, rec.Header().Get("Location"))
	}
	session := cookieValue(t, rec, SessionCookieName)
	if session == "" {
		t.Fatal("provisioned callback did not set a session cookie")
	}
	u, err := svc.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("lookup alice: %v", err)
	}
	if u.Email == nil || *u.Email != f.email || u.DisplayName == nil || *u.DisplayName != f.name {
		t.Fatalf("identity not synced on login: email=%v display_name=%v", u.Email, u.DisplayName)
	}
}

func TestOIDCUsernameFallbacks(t *testing.T) {
	// No preferred_username: fall back to the email local-part.
	f := newFakeOIDCProvider(t)
	f.username = ""
	f.email = "bob.smith@example.com"
	svc := newOIDCService(t, f, OIDCConfig{AutoCreate: true})
	h := newTestHandler(svc)
	state, nonce, challenge := doOIDCLogin(t, h, f)
	rec := doOIDCCallback(t, h, f, state, nonce, challenge)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("email fallback callback: got %d %q, want 302 /", rec.Code, rec.Header().Get("Location"))
	}
	u, err := svc.GetUserByUsername("bob.smith")
	if err != nil {
		t.Fatalf("expected username from email local-part, lookup failed: %v", err)
	}
	if u.Provider != "oidc" {
		t.Fatalf("provider = %q, want oidc", u.Provider)
	}

	// No username and no email: fall back to the subject.
	f2 := newFakeOIDCProvider(t)
	f2.sub = "no-username-subject"
	f2.username = ""
	f2.email = ""
	svc2 := newOIDCService(t, f2, OIDCConfig{AutoCreate: true})
	h2 := newTestHandler(svc2)
	state2, nonce2, challenge2 := doOIDCLogin(t, h2, f2)
	rec = doOIDCCallback(t, h2, f2, state2, nonce2, challenge2)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("subject fallback callback: got %d %q, want 302 /", rec.Code, rec.Header().Get("Location"))
	}
	if _, err := svc2.GetUserByUsername("no-username-subject"); err != nil {
		t.Fatalf("expected username from subject, lookup failed: %v", err)
	}
}

func TestOIDCCustomGroupsClaim(t *testing.T) {
	f := newFakeOIDCProvider(t)
	f.claims["roles"] = []string{"org-admins"}
	svc := newOIDCService(t, f, OIDCConfig{
		GroupsClaim: "roles", AdminGroup: "org-admins", AutoCreate: true,
	})
	h := newTestHandler(svc)

	state, nonce, challenge := doOIDCLogin(t, h, f)
	rec := doOIDCCallback(t, h, f, state, nonce, challenge)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("callback: got %d %q, want 302 /", rec.Code, rec.Header().Get("Location"))
	}
	u, err := svc.GetUserByUsername("alice")
	if err != nil {
		t.Fatalf("lookup alice: %v", err)
	}
	if !u.IsAdmin {
		t.Fatal("is_admin should be true when the custom groups claim contains the admin group")
	}
}

func TestLoadOIDCConfig(t *testing.T) {
	t.Setenv("OIDC_ENABLED", "1")
	t.Setenv("OIDC_NAME", "Corp SSO")
	t.Setenv("OIDC_ISSUER", "https://id.example.com")
	t.Setenv("OIDC_CLIENT_ID", "client-1")
	t.Setenv("OIDC_CLIENT_SECRET", "secret-1")
	t.Setenv("OIDC_SCOPES", "openid, email ,profile")
	t.Setenv("OIDC_ADMIN_GROUP", "admins")
	t.Setenv("OIDC_GROUPS_CLAIM", "roles")
	t.Setenv("OIDC_AUTO_CREATE", "false")

	c := LoadOIDCConfig()
	if !c.Enabled || c.Name != "Corp SSO" || c.Issuer != "https://id.example.com" ||
		c.ClientID != "client-1" || c.ClientSecret != "secret-1" ||
		c.AdminGroup != "admins" || c.GroupsClaim != "roles" || c.AutoCreate {
		t.Fatalf("LoadOIDCConfig = %+v", c)
	}
	if len(c.Scopes) != 3 || c.Scopes[0] != "openid" || c.Scopes[1] != "email" || c.Scopes[2] != "profile" {
		t.Fatalf("scopes = %v, want [openid email profile]", c.Scopes)
	}
}

func TestLoadOIDCConfigDefaults(t *testing.T) {
	c := LoadOIDCConfig()
	if c.Enabled {
		t.Fatal("OIDC must be disabled by default")
	}
	if c.Name != "SSO" || c.GroupsClaim != "groups" || !c.AutoCreate {
		t.Fatalf("defaults = %+v", c)
	}
	if len(c.Scopes) != 3 || c.Scopes[0] != "openid" || c.Scopes[1] != "profile" || c.Scopes[2] != "email" {
		t.Fatalf("default scopes = %v", c.Scopes)
	}
}

func TestOIDCRoutesNotRegisteredWhenDisabled(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true})
	h := newTestHandler(svc)

	rec := doReq(t, h, http.MethodGet, "/api/auth/oidc/login", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("oidc login with OIDC disabled: got %d, want 404", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/api/auth/oidc/callback", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("oidc callback with OIDC disabled: got %d, want 404", rec.Code)
	}
}
