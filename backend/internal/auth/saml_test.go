// Package auth — SAML 2.0 service provider tests (T5).
package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	saml "github.com/crewjam/saml"
	"github.com/gorilla/mux"
)

// testSPKeyPair generates the SP certificate and key under dir and returns
// the file paths.
func testSPKeyPair(t *testing.T, dir string) (string, string) {
	t.Helper()
	certFile := filepath.Join(dir, "sp.crt")
	keyFile := filepath.Join(dir, "sp.key")
	if err := ensureSPKeyPair(certFile, keyFile); err != nil {
		t.Fatalf("ensureSPKeyPair: %v", err)
	}
	return certFile, keyFile
}

// testIDPMetadata writes a minimal IdP metadata document, carrying a freshly
// generated signing certificate, and returns the file path.
func testIDPMetadata(t *testing.T, dir string) string {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate IdP key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate IdP serial: %v", err)
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "Test IdP"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create IdP certificate: %v", err)
	}
	meta := fmt.Sprintf(`<?xml version="1.0"?>
<EntityDescriptor xmlns="urn:oasis:names:tc:SAML:2.0:metadata" entityID="https://idp.example.com/saml">
  <IDPSSODescriptor WantAuthenticationRequestsSigned="false" protocolSupportEnumeration="urn:oasis:names:tc:SAML:2.0:protocol">
    <KeyDescriptor use="signing">
      <KeyInfo xmlns="http://www.w3.org/2000/09/xmldsig#">
        <X509Data>
          <X509Certificate>%s</X509Certificate>
        </X509Data>
      </KeyInfo>
    </KeyDescriptor>
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-Redirect" Location="https://idp.example.com/sso/redirect"/>
    <SingleSignOnService Binding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST" Location="https://idp.example.com/sso/post"/>
  </IDPSSODescriptor>
</EntityDescriptor>
`, base64.StdEncoding.EncodeToString(der))
	path := filepath.Join(dir, "idp-metadata.xml")
	if err := os.WriteFile(path, []byte(meta), 0o600); err != nil {
		t.Fatalf("write IdP metadata: %v", err)
	}
	return path
}

// testSAMLAssertion builds a hand-rolled assertion: NameID alice@example.com
// plus email/displayName/groups attributes. The attribute Names differ from
// the FriendlyNames on purpose, to prove matching works on either.
func testSAMLAssertion() *saml.Assertion {
	return &saml.Assertion{
		Subject: &saml.Subject{
			NameID: &saml.NameID{
				Format: "urn:oasis:names:tc:SAML:1.1:nameid-format:unspecified",
				Value:  "alice@example.com",
			},
		},
		AttributeStatements: []saml.AttributeStatement{
			{Attributes: []saml.Attribute{
				{Name: "urn:oid:0.9.999.1", FriendlyName: "email",
					Values: []saml.AttributeValue{{Value: "alice@example.com"}}},
				{Name: "urn:oid:0.9.999.2", FriendlyName: "displayName",
					Values: []saml.AttributeValue{{Value: "Alice Example"}}},
				{Name: "urn:oid:0.9.999.3", FriendlyName: "groups",
					Values: []saml.AttributeValue{{Value: "devs"}, {Value: "jellyide-admins"}}},
			}},
		},
	}
}

func TestLoadSAMLConfigDefaults(t *testing.T) {
	dataDir := t.TempDir()
	cfg := LoadSAMLConfig(dataDir)

	want := SAMLConfig{
		Name:       "SAML",
		CertFile:   filepath.Join(dataDir, "saml", "sp.crt"),
		KeyFile:    filepath.Join(dataDir, "saml", "sp.key"),
		EmailAttr:  "email",
		NameAttr:   "displayName",
		GroupsAttr: "groups",
		AutoCreate: true,
	}
	if cfg != want {
		t.Fatalf("LoadSAMLConfig() = %+v, want %+v", cfg, want)
	}
	// SAML disabled: no key pair files are written.
	if _, err := os.Stat(filepath.Join(dataDir, "saml")); !os.IsNotExist(err) {
		t.Fatalf("expected no saml dir when disabled, stat err = %v", err)
	}
}

func TestLoadSAMLConfigEnv(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SAML_ENABLED", "true")
	t.Setenv("SAML_NAME", "Keycloak")
	t.Setenv("SAML_IDP_METADATA_URL", "https://idp.example.com/metadata")
	t.Setenv("SAML_SP_CERT", filepath.Join(dataDir, "custom", "cert.pem"))
	t.Setenv("SAML_SP_KEY", filepath.Join(dataDir, "custom", "key.pem"))
	t.Setenv("SAML_USERNAME_ATTR", "preferred_username")
	t.Setenv("SAML_EMAIL_ATTR", "mail")
	t.Setenv("SAML_NAME_ATTR", "display_name")
	t.Setenv("SAML_GROUPS_ATTR", "memberOf")
	t.Setenv("SAML_ADMIN_GROUP", "jellyide-admins")
	t.Setenv("SAML_AUTO_CREATE", "false")

	cfg := LoadSAMLConfig(dataDir)

	if !cfg.Enabled ||
		cfg.Name != "Keycloak" ||
		cfg.IDPMetadataURL != "https://idp.example.com/metadata" ||
		cfg.CertFile != filepath.Join(dataDir, "custom", "cert.pem") ||
		cfg.KeyFile != filepath.Join(dataDir, "custom", "key.pem") ||
		cfg.UsernameAttr != "preferred_username" ||
		cfg.EmailAttr != "mail" ||
		cfg.NameAttr != "display_name" ||
		cfg.GroupsAttr != "memberOf" ||
		cfg.AdminGroup != "jellyide-admins" ||
		cfg.AutoCreate {
		t.Fatalf("LoadSAMLConfig() = %+v", cfg)
	}
	// An enabled config generates the (custom) key pair.
	if _, err := os.Stat(cfg.KeyFile); err != nil {
		t.Fatalf("key file not created: %v", err)
	}
}

func TestSAMLKeyAutoGeneration(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("SAML_ENABLED", "true")

	cfg := LoadSAMLConfig(dataDir)
	if !cfg.Enabled {
		t.Fatal("SAML_ENABLED=true not honoured")
	}

	certInfo, err := os.Stat(cfg.CertFile)
	if err != nil {
		t.Fatalf("cert file: %v", err)
	}
	keyInfo, err := os.Stat(cfg.KeyFile)
	if err != nil {
		t.Fatalf("key file: %v", err)
	}
	if perm := keyInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key perms = %o, want 600", perm)
	}
	if perm := certInfo.Mode().Perm(); perm != 0o644 {
		t.Fatalf("cert perms = %o, want 644", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(cfg.CertFile))
	if err != nil {
		t.Fatalf("saml dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("saml dir perms = %o, want 700", perm)
	}

	// The pair loads and is a 10-year self-signed certificate.
	keyPair, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		t.Fatalf("LoadX509KeyPair: %v", err)
	}
	leaf, err := x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	if leaf.Subject.CommonName != "JellyIDE SP" {
		t.Fatalf("certificate CN = %q, want %q", leaf.Subject.CommonName, "JellyIDE SP")
	}
	lifetime := leaf.NotAfter.Sub(leaf.NotBefore)
	if lifetime < 9*365*24*time.Hour || lifetime > 11*365*24*time.Hour {
		t.Fatalf("certificate lifetime = %s, want ~10 years", lifetime)
	}

	// Reload: a second LoadSAMLConfig must not regenerate the pair.
	keyBefore, err := os.ReadFile(cfg.KeyFile)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	cfg2 := LoadSAMLConfig(dataDir)
	keyAfter, err := os.ReadFile(cfg2.KeyFile)
	if err != nil {
		t.Fatalf("read key after reload: %v", err)
	}
	if !bytes.Equal(keyBefore, keyAfter) {
		t.Fatal("second LoadSAMLConfig regenerated the key pair")
	}
}

// TestSAMLMetadataAndLoginRoutes enables the SP with a static IdP metadata
// file and checks the mounted endpoints: metadata XML with the ACS under
// /api/auth/saml, and the SP-initiated login redirect to the IdP.
func TestSAMLMetadataAndLoginRoutes(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true, PublicURL: "https://ide.example.com"})
	dir := t.TempDir()
	certFile, keyFile := testSPKeyPair(t, dir)
	idpFile := testIDPMetadata(t, dir)

	cfg := SAMLConfig{
		Enabled:         true,
		Name:            "Test IdP",
		IDPMetadataFile: idpFile,
		CertFile:        certFile,
		KeyFile:         keyFile,
		EmailAttr:       "email",
		NameAttr:        "displayName",
		GroupsAttr:      "groups",
		AutoCreate:      true,
	}
	if err := svc.EnableSAML(context.Background(), cfg); err != nil {
		t.Fatalf("EnableSAML: %v", err)
	}

	r := mux.NewRouter()
	apiSub := r.PathPrefix("/api").Subrouter()
	svc.RegisterRoutes(apiSub)
	svc.RegisterSAMLRoutes(apiSub)
	h := svc.Middleware(r)

	rec := doReq(t, h, http.MethodGet, "/api/auth/saml/metadata", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("metadata: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/samlmetadata+xml" {
		t.Fatalf("metadata content type = %q", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "AssertionConsumerService") {
		t.Fatalf("metadata missing AssertionConsumerService: %s", body)
	}
	if !strings.Contains(body, "https://ide.example.com/api/auth/saml/acs") {
		t.Fatalf("metadata ACS URL not rooted at /api/auth/saml: %s", body)
	}

	rec = doReq(t, h, http.MethodGet, "/api/auth/saml/login", nil, nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("login: got %d, want 302: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "https://idp.example.com/sso/") {
		t.Fatalf("login redirect Location = %q, want the IdP SSO URL", loc)
	}

	// The providers endpoint reports the registered SAML provider.
	rec = doReq(t, h, http.MethodGet, "/api/auth/providers", nil, nil)
	providers := decodeBody(t, rec)
	samlProv, _ := providers["saml"].(map[string]interface{})
	if samlProv["enabled"] != true || samlProv["name"] != "Test IdP" {
		t.Fatalf("providers saml = %v, want enabled with name %q", samlProv, "Test IdP")
	}
}

func TestSAMLCreateSession(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true, PublicURL: "https://ide.example.com"})
	bridge := &samlSessionBridge{
		svc: svc,
		cfg: SAMLConfig{
			EmailAttr:  "email",
			NameAttr:   "displayName",
			GroupsAttr: "groups",
			AdminGroup: "jellyide-admins",
			AutoCreate: true,
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/saml/acs", nil)
	rec := httptest.NewRecorder()
	if err := bridge.CreateSession(rec, req, testSAMLAssertion()); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// samlsp writes the redirect itself; the bridge must not write a
	// second one.
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("CreateSession Location = %q, want no redirect from the bridge", loc)
	}
	token := cookieValue(t, rec, SessionCookieName)

	// The user was created from the assertion.
	user, err := svc.GetUserByUsername("alice@example.com")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if user.Provider != "saml" || user.Subject == nil || *user.Subject != "alice@example.com" {
		t.Fatalf("user provider/subject = %s/%v, want saml/alice@example.com", user.Provider, user.Subject)
	}
	if user.Email == nil || *user.Email != "alice@example.com" {
		t.Fatalf("user email = %v", user.Email)
	}
	if user.DisplayName == nil || *user.DisplayName != "Alice Example" {
		t.Fatalf("user display name = %v", user.DisplayName)
	}
	if !user.IsAdmin {
		t.Fatal("user in the admin group must be an admin")
	}
	if user.PasswordHash != nil {
		t.Fatal("SAML user must not have a local password")
	}

	// The session cookie authenticates against the auth API.
	h := newTestHandler(svc)
	rec = doReq(t, h, http.MethodGet, "/api/auth/me", nil, sessionHeader(t, token, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("me with SAML session: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	me := decodeBody(t, rec)
	if me["username"] != "alice@example.com" || me["provider"] != "saml" || me["is_admin"] != true {
		t.Fatalf("me body = %v", me)
	}

	// DeleteSession destroys the session.
	delReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	delReq.Header.Set("Cookie", SessionCookieName+"="+token)
	if err := bridge.DeleteSession(httptest.NewRecorder(), delReq); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	rec = doReq(t, h, http.MethodGet, "/api/auth/me", nil, sessionHeader(t, token, false))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me after DeleteSession: got %d, want 401", rec.Code)
	}
}

// In approval mode a SAML auto-created user starts pending and the bridge
// refuses to start a session until an admin approves.
func TestSAMPendingInApprovalMode(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true, PublicURL: "https://ide.example.com"})
	if err := svc.SetRegistrationMode(RegistrationModeApproval); err != nil {
		t.Fatalf("SetRegistrationMode: %v", err)
	}
	bridge := &samlSessionBridge{
		svc: svc,
		cfg: SAMLConfig{
			EmailAttr:  "email",
			NameAttr:   "displayName",
			GroupsAttr: "groups",
			AutoCreate: true,
		},
	}

	rec := httptest.NewRecorder()
	if err := bridge.CreateSession(rec, httptest.NewRequest(http.MethodPost, "/api/auth/saml/acs", nil), testSAMLAssertion()); err == nil {
		t.Fatal("expected an error for a pending SSO user")
	}
	u, err := svc.GetUserByUsername("alice@example.com")
	if err != nil {
		t.Fatalf("lookup alice: %v", err)
	}
	if u.Status != UserStatusPending {
		t.Fatalf("SAML user status = %q, want pending", u.Status)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			t.Fatal("pending SAML login must not set a session cookie")
		}
	}

	// After approval the same assertion logs in and creates a session.
	if err := svc.SetStatus(u.ID, UserStatusActive); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	rec = httptest.NewRecorder()
	if err := bridge.CreateSession(rec, httptest.NewRequest(http.MethodPost, "/api/auth/saml/acs", nil), testSAMLAssertion()); err != nil {
		t.Fatalf("CreateSession after approval: %v", err)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("CreateSession Location = %q, want no redirect from the bridge", loc)
	}
	if token := cookieValue(t, rec, SessionCookieName); token == "" {
		t.Fatal("approved SAML login did not set a session cookie")
	}
}

func TestSAMLCreateSessionAutoCreateDisabled(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, LocalEnabled: true, PublicURL: "https://ide.example.com"})
	bridge := &samlSessionBridge{
		svc: svc,
		cfg: SAMLConfig{
			EmailAttr:  "email",
			NameAttr:   "displayName",
			GroupsAttr: "groups",
			AdminGroup: "jellyide-admins",
			AutoCreate: false,
		},
	}

	// Unknown user: login fails and nothing is created.
	rec := httptest.NewRecorder()
	err := bridge.CreateSession(rec, httptest.NewRequest(http.MethodPost, "/api/auth/saml/acs", nil), testSAMLAssertion())
	if err == nil {
		t.Fatal("expected an error for an unknown user with SAML_AUTO_CREATE off")
	}
	if _, uerr := svc.GetUserByUsername("alice@example.com"); uerr == nil {
		t.Fatal("user must not be created with SAML_AUTO_CREATE off")
	}

	// Once the user exists, the same assertion logs in and updates the
	// profile fields.
	if _, err := svc.FindOrCreateExternalUser("saml", "alice@example.com", "alice@example.com", "", "", false); err != nil {
		t.Fatalf("FindOrCreateExternalUser: %v", err)
	}
	rec = httptest.NewRecorder()
	if err := bridge.CreateSession(rec, httptest.NewRequest(http.MethodPost, "/api/auth/saml/acs", nil), testSAMLAssertion()); err != nil {
		t.Fatalf("CreateSession for existing user: %v", err)
	}
	user, err := svc.GetUserByUsername("alice@example.com")
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if user.Email == nil || *user.Email != "alice@example.com" {
		t.Fatalf("existing user email not updated: %v", user.Email)
	}
	if !user.IsAdmin {
		t.Fatal("existing user should pick up the admin group on login")
	}
}
