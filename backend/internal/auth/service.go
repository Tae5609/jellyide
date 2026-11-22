package auth

import (
	"context"
	"errors"
	"fmt"

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
	oidc      *oidcState // set by EnableOIDC at startup; non-nil when OIDC is on
}

// NewService builds the auth service and bootstraps the admin account when
// auth is enabled and no users exist yet. If ADMIN_PASSWORD is empty in that
// situation it returns an error instead of creating an unusable admin.
func NewService(cfg Config, database *db.DB) (*Service, error) {
	s := &Service{
		cfg:       cfg,
		db:        database,
		providers: newProviderState(),
		limiter:   newLoginLimiter(),
	}

	if cfg.Enabled {
		count, err := s.CountUsers()
		if err != nil {
			return nil, fmt.Errorf("count users: %w", err)
		}
		if count == 0 {
			if cfg.AdminPassword == "" {
				return nil, errors.New("no users exist: set ADMIN_PASSWORD to bootstrap the admin account")
			}
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

	s.startSessionCleanup(context.Background())
	return s, nil
}

// Config returns a copy of the service configuration.
func (s *Service) Config() Config { return s.cfg }

// DB returns the underlying database handle.
func (s *Service) DB() *db.DB { return s.db }
