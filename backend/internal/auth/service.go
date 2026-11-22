package auth

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"

	samlsp "github.com/crewjam/saml/samlsp"

	"yt-scraper/internal/db"
)

// Service is the auth core: configuration, database handle, and the state
// shared by middleware and handlers. T4 (OIDC) and T5 (SAML) extend it via
// the exported accessors below.
type Service struct {
	cfg       Config
	db        *db.DB
	providers *providerState
	limiter   *loginLimiter
	// setupLimiter rate-limits failed setup-token attempts (separate from
	// the login limiter: a stricter limit on the first-run endpoint).
	setupLimiter *loginLimiter
	// setupRequired is armed when the server starts in setup mode (no
	// users, no ADMIN_PASSWORD). It is cleared the moment the first user
	// exists — by the wizard or by the admin CLI — so the 503 gate opens
	// without a restart.
	setupRequired atomic.Bool
	// setupToken is the one-time setup token. In memory only: it is
	// regenerated on every start until setup completes and is never
	// persisted or logged outside the startup banner.
	setupToken string
	oidc       *oidcState // set by EnableOIDC at startup; non-nil when OIDC is on
	samlSP     *samlsp.Middleware
}

// NewService builds the auth service. When auth is enabled and no users
// exist yet, it either bootstraps the admin account from ADMIN_PASSWORD
// (applied only on that first start, never re-applied or reset later) or,
// when ADMIN_PASSWORD is empty, starts in setup mode: a one-time setup
// token is generated and printed to the log for the first-run wizard.
func NewService(cfg Config, database *db.DB) (*Service, error) {
	s := &Service{
		cfg:          cfg,
		db:           database,
		providers:    newProviderState(),
		limiter:      newLoginLimiter(loginFailureLimit),
		setupLimiter: newLoginLimiter(setupFailureLimit),
	}

	if cfg.Enabled {
		count, err := s.CountUsers()
		if err != nil {
			return nil, fmt.Errorf("count users: %w", err)
		}
		if count == 0 {
			if cfg.AdminPassword == "" {
				if err := s.startSetupMode(cfg.PublicURL); err != nil {
					return nil, fmt.Errorf("start setup mode: %w", err)
				}
			} else {
				hash, err := HashPassword(cfg.AdminPassword)
				if err != nil {
					return nil, fmt.Errorf("hash admin password: %w", err)
				}
				admin := &User{
					ID:           newID(),
					Username:     cfg.AdminUsername,
					PasswordHash: &hash,
					Provider:     "local",
					IsAdmin:      true,
				}
				if err := s.CreateUser(admin); err != nil {
					return nil, fmt.Errorf("create admin user: %w", err)
				}
			}
		}

		// Seed the registration mode from the environment exactly once; a
		// later admin change in the UI keeps winning over the env value.
		if err := s.seedRegistrationMode(cfg.RegistrationMode); err != nil {
			return nil, fmt.Errorf("seed registration mode: %w", err)
		}
	}

	s.startSessionCleanup(context.Background())
	return s, nil
}

// Config returns a copy of the service configuration.
func (s *Service) Config() Config { return s.cfg }

// DB returns the underlying database handle.
func (s *Service) DB() *db.DB { return s.db }

// startSetupMode arms the first-run setup wizard: a one-time token (32
// random bytes, base64url) is generated in memory and printed to the log so
// `docker logs jelly-ide` reveals it. The token is regenerated on every
// start until setup completes.
func (s *Service) startSetupMode(publicURL string) error {
	token, _, err := randomTokenHash()
	if err != nil {
		return fmt.Errorf("generate setup token: %w", err)
	}
	s.setupToken = token
	s.setupRequired.Store(true)
	log.Printf("==== JellyIDE first-run setup ====\n"+
		"Open %s/setup and enter this setup token:\n"+
		"%s\n"+
		"==================================", publicURL, token)
	return nil
}

// SetupRequired reports whether the first-run setup is still pending: the
// server started in setup mode and no user exists yet. It turns false the
// moment the first user is created (wizard or CLI), without a restart.
func (s *Service) SetupRequired() bool {
	if !s.setupRequired.Load() {
		return false
	}
	count, err := s.CountUsers()
	if err != nil {
		// The count cannot be verified; keep the gate closed.
		return true
	}
	return count == 0
}
