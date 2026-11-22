package auth

// SAML 2.0 service provider (T5).
//
// The SP is built on github.com/crewjam/saml/samlsp and rooted at
// <PublicURL>/api/auth, so samlsp serves the metadata at
// /api/auth/saml/metadata, the assertion consumer service (ACS) at
// /api/auth/saml/acs and the SP-initiated login flow at
// /api/auth/saml/login. The auth middleware already treats /api/auth/* as
// public, so the cross-site ACS POST from the IdP needs no session and no
// CSRF header; no other path is affected.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	saml "github.com/crewjam/saml"
	samlsp "github.com/crewjam/saml/samlsp"
	"github.com/gorilla/mux"
)

// SAMLConfig holds the SAML 2.0 service provider settings.
type SAMLConfig struct {
	Enabled         bool
	Name            string
	IDPMetadataURL  string
	IDPMetadataFile string
	CertFile        string
	KeyFile         string
	UsernameAttr    string
	EmailAttr       string
	NameAttr        string
	GroupsAttr      string
	AdminGroup      string
	AutoCreate      bool
}

// LoadSAMLConfig reads the SAML settings from the environment. dataDir is
// the default location of the SP certificate and key. When SAML is enabled
// and the cert/key files are missing, a self-signed pair is generated (see
// ensureSPKeyPair); a failure is logged here and reported again as a hard
// error by EnableSAML.
func LoadSAMLConfig(dataDir string) SAMLConfig {
	cfg := SAMLConfig{
		Enabled:         envBool("SAML_ENABLED", false),
		Name:            envStr("SAML_NAME", "SAML"),
		IDPMetadataURL:  envStr("SAML_IDP_METADATA_URL", ""),
		IDPMetadataFile: envStr("SAML_IDP_METADATA_FILE", ""),
		CertFile:        envStr("SAML_SP_CERT", filepath.Join(dataDir, "saml", "sp.crt")),
		KeyFile:         envStr("SAML_SP_KEY", filepath.Join(dataDir, "saml", "sp.key")),
		UsernameAttr:    envStr("SAML_USERNAME_ATTR", ""),
		EmailAttr:       envStr("SAML_EMAIL_ATTR", "email"),
		NameAttr:        envStr("SAML_NAME_ATTR", "displayName"),
		GroupsAttr:      envStr("SAML_GROUPS_ATTR", "groups"),
		AdminGroup:      envStr("SAML_ADMIN_GROUP", ""),
		AutoCreate:      envBool("SAML_AUTO_CREATE", true),
	}
	if cfg.Enabled {
		if err := ensureSPKeyPair(cfg.CertFile, cfg.KeyFile); err != nil {
			log.Printf("saml: %v", err)
		}
	}
	return cfg
}

// ensureSPKeyPair creates the SP key pair when either file is missing: an
// RSA 2048 key and a self-signed certificate (CN "JellyIDE SP") valid for
// 10 years. The key is written 0600, the parent directories 0700.
func ensureSPKeyPair(certFile, keyFile string) error {
	if fileExists(certFile) && fileExists(keyFile) {
		return nil
	}
	for _, dir := range []string{filepath.Dir(certFile), filepath.Dir(keyFile)} {
		// MkdirAll applies the 0700 mode to every directory it creates.
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("saml: create directory %s: %w", dir, err)
		}
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("saml: generate RSA key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("saml: generate certificate serial: %w", err)
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "JellyIDE SP"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("saml: create certificate: %w", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		return fmt.Errorf("saml: write key %s: %w", keyFile, err)
	}
	if err := os.Chmod(keyFile, 0o600); err != nil {
		return fmt.Errorf("saml: chmod key %s: %w", keyFile, err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil { //nolint:gosec // G306 - the certificate is public, world-readable on purpose
		return fmt.Errorf("saml: write certificate %s: %w", certFile, err)
	}
	log.Printf("saml: generated SP certificate %s and key %s", certFile, keyFile)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// EnableSAML builds the SAML service provider from c and registers it with
// the service. It must be called before RegisterSAMLRoutes. Signature
// validation is left entirely to the library, and unsolicited (IdP
// initiated) responses are refused.
func (s *Service) EnableSAML(ctx context.Context, c SAMLConfig) error {
	keyPair, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return fmt.Errorf("saml: load SP key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(keyPair.Certificate[0])
	if err != nil {
		return fmt.Errorf("saml: parse SP certificate: %w", err)
	}
	keyPair.Leaf = leaf
	rsaKey, ok := keyPair.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return fmt.Errorf("saml: SP key must be an RSA private key, got %T", keyPair.PrivateKey)
	}

	var idpMeta *saml.EntityDescriptor
	switch {
	case c.IDPMetadataURL != "":
		u, err := url.Parse(c.IDPMetadataURL)
		if err != nil {
			return fmt.Errorf("saml: parse IDP metadata URL: %w", err)
		}
		idpMeta, err = samlsp.FetchMetadata(ctx, http.DefaultClient, *u)
		if err != nil {
			return fmt.Errorf("saml: fetch IDP metadata from %s: %w", c.IDPMetadataURL, err)
		}
	case c.IDPMetadataFile != "":
		data, err := os.ReadFile(c.IDPMetadataFile)
		if err != nil {
			return fmt.Errorf("saml: read IDP metadata file: %w", err)
		}
		idpMeta, err = samlsp.ParseMetadata(data)
		if err != nil {
			return fmt.Errorf("saml: parse IDP metadata: %w", err)
		}
	default:
		return errors.New("saml: set SAML_IDP_METADATA_URL or SAML_IDP_METADATA_FILE")
	}

	// The trailing slash matters: samlsp resolves its "saml/metadata" and
	// "saml/acs" paths against this URL, so the endpoints must land under
	// /api/auth, not /api.
	root, err := url.Parse(strings.TrimSuffix(s.Config().PublicURL, "/") + "/api/auth/")
	if err != nil {
		return fmt.Errorf("saml: parse public URL %q: %w", s.Config().PublicURL, err)
	}

	opts := samlsp.Options{
		URL:               *root,
		Key:               rsaKey,
		Certificate:       leaf,
		IDPMetadata:       idpMeta,
		AllowIDPInitiated: false,
	}

	sp, err := samlsp.New(opts)
	if err != nil {
		return fmt.Errorf("saml: create service provider: %w", err)
	}

	// Sessions are ours, not samlsp's JWT cookie.
	sp.Session = &samlSessionBridge{svc: s, cfg: c}
	// Log the failure detail (never assertion content or tokens) and send
	// the user back to the login page with a generic message.
	sp.OnError = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("saml: %v", err)
		http.Redirect(w, r, "/login?error="+url.QueryEscape("SAML login failed"), http.StatusFound)
	}

	// The IdP POSTs the ACS response cross-site, so the request-tracker
	// cookie must ride that hop: SameSite=None and Secure when the site is
	// served over HTTPS.
	if s.Config().CookieSecure {
		opts.CookieSameSite = http.SameSiteNoneMode
	} else {
		// SP-initiated login over plain HTTP keeps the Lax tracker cookie;
		// browsers may drop it on the cross-site hop from the IdP, so in
		// this mode the IdP needs to be reachable on the same site.
		opts.CookieSameSite = http.SameSiteLaxMode
	}
	sp.RequestTracker = samlsp.DefaultRequestTracker(opts, &sp.ServiceProvider)

	s.SetSAMLProvider(c.Name)
	s.samlSP = sp
	return nil
}

// RegisterSAMLRoutes mounts the SAML endpoints on the /api subrouter. It is
// only safe to call after EnableSAML succeeded.
func (s *Service) RegisterSAMLRoutes(r *mux.Router) {
	sp := s.samlSP
	if sp == nil {
		log.Printf("saml: RegisterSAMLRoutes called before EnableSAML, skipping")
		return
	}
	r.HandleFunc("/auth/saml/metadata", sp.ServeMetadata).Methods(http.MethodGet)
	r.HandleFunc("/auth/saml/acs", sp.ServeACS).Methods(http.MethodPost)
	r.HandleFunc("/auth/saml/login", sp.HandleStartAuthFlow).Methods(http.MethodGet)
}

// samlSessionBridge implements samlsp.SessionProvider with JellyIDE's own
// sessions: after a validated ACS exchange it resolves the user (creating it
// when allowed) and starts a regular session.
type samlSessionBridge struct {
	svc *Service
	cfg SAMLConfig
}

var _ samlsp.SessionProvider = (*samlSessionBridge)(nil)

// CreateSession runs after samlsp has validated the ACS response
// (signature and conditions included). It resolves the user, records a
// JellyIDE session and redirects to the app.
func (b *samlSessionBridge) CreateSession(w http.ResponseWriter, _ *http.Request, assertion *saml.Assertion) error {
	nameID := ""
	if assertion.Subject != nil && assertion.Subject.NameID != nil {
		nameID = assertion.Subject.NameID.Value
	}
	if nameID == "" {
		return errors.New("saml: assertion has no NameID")
	}

	attrs := samlAssertionAttributes(assertion)

	username := ""
	if vals := attrs[b.cfg.UsernameAttr]; len(vals) > 0 {
		username = vals[0]
	}
	if username == "" {
		username = nameID
	}
	email := firstAttr(attrs, b.cfg.EmailAttr)
	displayName := firstAttr(attrs, b.cfg.NameAttr)

	isAdmin := false
	if b.cfg.AdminGroup != "" {
		for _, g := range attrs[b.cfg.GroupsAttr] {
			if g == b.cfg.AdminGroup {
				isAdmin = true
				break
			}
		}
	}

	var (
		user *User
		err  error
	)
	if b.cfg.AutoCreate {
		user, err = b.svc.FindOrCreateExternalUser("saml", nameID, username, email, displayName, isAdmin)
	} else {
		user, err = samlUserBySubject(b.svc, nameID)
		if errors.Is(err, ErrNoUser) {
			return fmt.Errorf("saml: no user for %q and SAML_AUTO_CREATE is off", nameID)
		}
	}
	if err != nil {
		return fmt.Errorf("saml: resolve user: %w", err)
	}

	if err := b.updateSAMLProfile(user.ID, email, displayName, isAdmin); err != nil {
		return fmt.Errorf("saml: update user profile: %w", err)
	}

	// In approval mode a newly auto-created user is pending (see
	// FindOrCreateExternalUser); in admin_only mode auto-creation only
	// happens when SAML_AUTO_CREATE is on. Pending and disabled users may
	// not hold sessions, so the login is refused here.
	if user.Status != UserStatusActive {
		if user.Status == UserStatusPending {
			return errors.New("saml: account pending approval")
		}
		return errors.New("saml: account disabled")
	}

	if err := b.svc.StartSession(w, user.ID); err != nil {
		return fmt.Errorf("saml: start session: %w", err)
	}
	// samlsp writes the redirect after CreateSession returns; writing one
	// here too would trigger a superfluous WriteHeader.
	return nil
}

// DeleteSession destroys the JellyIDE session for the request.
func (b *samlSessionBridge) DeleteSession(w http.ResponseWriter, r *http.Request) error {
	return b.svc.DestroySession(w, r)
}

// GetSession always reports no session. It is never consulted because this
// SP does not use samlsp.RequireAccount: the bridge only creates sessions.
func (b *samlSessionBridge) GetSession(_ *http.Request) (samlsp.Session, error) {
	return nil, samlsp.ErrNoSession
}

// updateSAMLProfile refreshes the profile fields of a SAML user on each
// login. Empty values are stored as NULL, mirroring
// FindOrCreateExternalUser.
func (b *samlSessionBridge) updateSAMLProfile(id, email, displayName string, isAdmin bool) error {
	_, err := b.svc.DB().Exec(
		`UPDATE users SET email = ?, display_name = ?, is_admin = ? WHERE id = ?`,
		strPtrOrNil(email), strPtrOrNil(displayName), isAdmin, id,
	)
	return err
}

// samlUserBySubject looks up a user by (provider, subject) without creating
// one.
func samlUserBySubject(s *Service, subject string) (*User, error) {
	var u User
	err := s.DB().Get(&u,
		`SELECT `+userSelectColumns+` FROM users WHERE provider = ? AND subject = ?`,
		"saml", subject)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoUser
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// samlAssertionAttributes collects the assertion's attribute values keyed by
// both Name and FriendlyName, so callers can match on either.
func samlAssertionAttributes(assertion *saml.Assertion) map[string][]string {
	attrs := map[string][]string{}
	for _, stmt := range assertion.AttributeStatements {
		for _, a := range stmt.Attributes {
			for _, name := range []string{a.Name, a.FriendlyName} {
				if name == "" {
					continue
				}
				for _, v := range a.Values {
					attrs[name] = append(attrs[name], v.Value)
				}
			}
		}
	}
	return attrs
}

func firstAttr(attrs map[string][]string, name string) string {
	if name == "" {
		return ""
	}
	if vals := attrs[name]; len(vals) > 0 {
		return vals[0]
	}
	return ""
}
