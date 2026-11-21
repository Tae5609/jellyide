# T5: SAML 2.0 login (backend)

Repo: JellyIDE, Go backend `backend/` (module `yt-scraper`). The auth core is merged in `backend/internal/auth/`. Read every file there first, plus `backend/main.go`. Use the exported helpers: `StartSession`, `FindOrCreateExternalUser`, `SetSAMLProvider`, `Config()`.

## Do
1. `cd backend && go get github.com/crewjam/saml@latest`.
2. Add `backend/internal/auth/saml.go`:
   - `type SAMLConfig struct{Enabled bool; Name, IDPMetadataURL, IDPMetadataFile, CertFile, KeyFile, UsernameAttr, EmailAttr, NameAttr, GroupsAttr, AdminGroup string; AutoCreate bool}` and `LoadSAMLConfig(dataDir string)`, reading env:

     | Env | Default |
     |---|---|
     | `SAML_ENABLED` | false |
     | `SAML_NAME` | "SAML" |
     | `SAML_IDP_METADATA_URL` | – |
     | `SAML_IDP_METADATA_FILE` | – |
     | `SAML_SP_CERT` | `<dataDir>/saml/sp.crt` |
     | `SAML_SP_KEY` | `<dataDir>/saml/sp.key` |
     | `SAML_USERNAME_ATTR` | "" (NameID is used) |
     | `SAML_EMAIL_ATTR` | "email" |
     | `SAML_NAME_ATTR` | "displayName" |
     | `SAML_GROUPS_ATTR` | "groups" |
     | `SAML_ADMIN_GROUP` | – |
     | `SAML_AUTO_CREATE` | true |
   - If the cert/key files do not exist, generate an RSA 2048 self-signed cert (CN=JellyIDE SP, 10 years). Write the key with 0600 perms (create the dir with 0700) and log the paths.
   - `func (s *Service) EnableSAML(ctx context.Context, c SAMLConfig) error`:
     ```go
     keyPair, _ := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
     keyPair.Leaf, _ = x509.ParseCertificate(keyPair.Certificate[0])
     idpMeta, _ := samlsp.FetchMetadata(ctx, http.DefaultClient, *mustParse(c.IDPMetadataURL)) // or samlsp.ParseMetadata(fileBytes)
     root, _ := url.Parse(s.Config().PublicURL + "/api/auth")
     sp, _ := samlsp.New(samlsp.Options{URL: *root, Key: keyPair.PrivateKey.(*rsa.PrivateKey), Certificate: keyPair.Leaf, IDPMetadata: idpMeta, AllowIDPInitiated: false})
     sp.Session = &samlSessionBridge{svc: s, cfg: c}   // our sessions, not samlsp's JWT cookie
     sp.OnError = func(w http.ResponseWriter, r *http.Request, err error) { log.Printf("saml: %v", err); http.Redirect(w, r, "/login?error="+url.QueryEscape("SAML login failed"), http.StatusFound) }
     ```
     Handle every error properly; the snippet above ignores them only for brevity. With this root URL, samlsp serves the metadata at `/api/auth/saml/metadata` and the ACS at `/api/auth/saml/acs`.
   - Request-tracker cookies: the IdP POSTs cross-site to the ACS. When `s.Config().CookieSecure` is true, set the tracker to `SameSite=None; Secure`: replace `sp.RequestTracker` with `samlsp.DefaultRequestTracker(opts, &sp.ServiceProvider)` after setting `opts.CookieSameSite = http.SameSiteNoneMode`. Otherwise use Lax, and add a code comment that SP-initiated login over plain HTTP may need the IdP on the same site.
   - `samlSessionBridge` implements `samlsp.SessionProvider`:
     - `CreateSession(w, r, assertion)`: read the NameID (`assertion.Subject.NameID.Value`) and attributes (loop `assertion.AttributeStatements[].Attributes[]`, matching `Name` or `FriendlyName`). Username = UsernameAttr value, else NameID. isAdmin = AdminGroup in groups. Honour AutoCreate. Call `FindOrCreateExternalUser("saml", nameID, ...)`, update is_admin/email/display_name, call `StartSession`, then `http.Redirect(w, r, "/", 302)` and return nil.
     - `DeleteSession` deletes our session.
     - `GetSession` returns `samlsp.ErrNoSession`. It is unused because we do not use `RequireAccount`.
   - Routes, registered only when SAML is enabled:
     - `GET /api/auth/saml/metadata` → `sp.ServeMetadata`
     - `POST /api/auth/saml/acs` → `sp.ServeACS`
     - `GET /api/auth/saml/login` → `sp.HandleStartAuthFlow`
   - The middleware treats `/api/auth/*` as public, so the ACS POST needs no CSRF header. Confirm this in `middleware.go`; do not weaken CSRF for other paths.
   - Never hand-parse or skip signature validation; the library validates.
3. `backend/main.go`: `if sc := auth.LoadSAMLConfig(downloadDir); sc.Enabled { if err := authSvc.EnableSAML(ctx, sc); err != nil { log.Fatal(...) } }`. Do this before route registration.
4. `backend/internal/auth/saml_test.go`: test the cert auto-generation (files created, key perms 0600, reload works). Test that the `/api/auth/saml/metadata` route returns XML containing `AssertionConsumerService` when enabled with a static IdP metadata file. Put a minimal IdP metadata XML with a generated cert in the test. Test that `CreateSession` on the bridge with a hand-built `*saml.Assertion` creates a user and sets the `jellyide_session` cookie.

## Do NOT
- Touch the frontend, local login, or the OIDC code.
- Disable signature checks or set `AllowIDPInitiated: true`.

## Verify
```
cd backend && go mod tidy && gofmt -l . && go vet ./... && go build ./... && go test ./...
```
`gofmt -l` prints nothing. Commit: `add SAML 2.0 SP login`.
