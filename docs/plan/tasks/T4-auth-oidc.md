# T4: OIDC login (backend)

Repo: JellyIDE, Go backend `backend/` (module `yt-scraper`). The auth core is merged in `backend/internal/auth/`. Read every file there first, especially `service.go`, `session.go`, `store.go`, `handlers.go`, `middleware.go`, plus `backend/main.go`. Use the exported helpers: `StartSession`, `FindOrCreateExternalUser`, `SetOIDCProvider`, `Config()`.

Follow `docs/plan/CONVENTIONS.md` (commit, PR and quality-gate rules).

## Do
1. `cd backend && go get github.com/coreos/go-oidc/v3/oidc golang.org/x/oauth2`.
2. Add `backend/internal/auth/oidc.go`:
   - `type OIDCConfig struct{Enabled bool; Name, Issuer, ClientID, ClientSecret string; Scopes []string; AdminGroup, GroupsClaim string; AutoCreate bool}` and `LoadOIDCConfig()`, reading env `OIDC_ENABLED` (default false), `OIDC_NAME` (default "SSO"), `OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_SCOPES` (comma list, default `openid,profile,email`), `OIDC_ADMIN_GROUP`, `OIDC_GROUPS_CLAIM` (default `groups`), `OIDC_AUTO_CREATE` (default true).
   - `func (s *Service) EnableOIDC(ctx context.Context, c OIDCConfig) error`: `oidc.NewProvider(ctx, c.Issuer)`, build `oauth2.Config` with `RedirectURL = s.Config().PublicURL + "/api/auth/oidc/callback"`, a verifier with `ClientID`, then call `s.SetOIDCProvider(c.Name)`.
   - Pending logins live in an in-memory map `state -> {nonce, pkceVerifier, expires}` with a mutex and a 10-minute TTL (prune on each insert). The state, nonce and verifier are 32 random bytes each, base64url encoded. Also set a short cookie `jellyide_oidc_state` (HttpOnly, SameSite=Lax, Path=/api/auth/oidc, MaxAge 600) holding the state, to bind the flow to the browser.
   - `GET /api/auth/oidc/login`: create the pending entry and redirect 302 to `AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))`.
   - `GET /api/auth/oidc/callback`:
     1. Validate `state` against the query, the cookie and the map entry; delete it on use, single-use.
     2. Handle an `error` query param.
     3. `Exchange(ctx, code, oauth2.VerifierOption(verifier))`, extract `id_token`, verify it, and check `idToken.Nonce == nonce`.
     4. Read claims `sub, email, preferred_username, name` and the groups claim (string array).
     5. Username = preferred_username, else the email local-part, else `sub`.
     6. isAdmin = AdminGroup != "" and groups contains it.
     7. If AutoCreate is false and the user does not exist, fail with "account not provisioned".
     8. `FindOrCreateExternalUser("oidc", sub, ...)`. Update `is_admin`, email and display_name on every login, adding a store method if needed. Then `StartSession` and redirect 302 to `/`.
     - On any failure: `log.Printf` the detail (never tokens) and redirect to `/login?error=` with a short url-escaped generic message.
   - Register both routes in `RegisterRoutes` only when OIDC is enabled.
3. `backend/main.go`: after creating the auth service, `if oc := auth.LoadOIDCConfig(); oc.Enabled { if err := authSvc.EnableOIDC(ctx, oc); err != nil { log.Fatal(...) } }`. This must happen before route registration.
4. `backend/internal/auth/oidc_test.go`: spin up a fake OIDC provider with `httptest.Server` serving `/.well-known/openid-configuration`, `/keys` (an RSA JWK) and `/token` (returns an RS256-signed id_token with the given nonce, using `github.com/go-jose/go-jose/v4`, which is already an indirect dep of go-oidc). Test the full login → callback flow, which should set the session cookie. Test bad state → redirect with error. Test nonce mismatch → error.

## Do NOT
- Touch the frontend, local login behaviour, or the SAML code.
- Log tokens or codes.

## Verify
```
cd backend && go mod tidy && gofmt -l . && go vet ./... && go build ./... && go test ./...
```
`gofmt -l` prints nothing. Commit: `feat(oidc): add OIDC login with auth code and PKCE`.
