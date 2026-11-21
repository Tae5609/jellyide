package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"time"
)

const (
	// SessionCookieName is the cookie carrying the session token.
	SessionCookieName = "jellyide_session"

	// sessionTTL is how long a session lives from (re)creation.
	sessionTTL = 7 * 24 * time.Hour

	// sessionSlideThreshold slides the expiry forward when less than this
	// time remains on the session.
	sessionSlideThreshold = 6 * 24 * time.Hour

	// sessionCleanupInterval is how often expired sessions are purged.
	sessionCleanupInterval = time.Hour
)

// ErrNoSession is returned when the request carries no valid session.
var ErrNoSession = errors.New("no valid session")

// StartSession creates a new session for userID: 32 random bytes from
// crypto/rand, base64url-encoded for the cookie, sha256-hex stored in the
// database with a 7-day expiry.
func (s *Service) StartSession(w http.ResponseWriter, userID string) error {
	token, tokenHash, err := randomTokenHash()
	if err != nil {
		return err
	}
	expiresAt := time.Now().Add(sessionTTL)
	if _, err := s.db.Exec(
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		tokenHash, userID, formatTime(expiresAt),
	); err != nil {
		return err
	}
	http.SetCookie(w, s.sessionCookie(token))
	return nil
}

// CreateSession is an alias for StartSession.
func (s *Service) CreateSession(w http.ResponseWriter, userID string) error {
	return s.StartSession(w, userID)
}

// sessionCookie builds the session cookie with the right flags.
func (s *Service) sessionCookie(token string) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // G124 - the Secure flag is deliberately config-driven (COOKIE_SECURE) so local http dev keeps working
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure,
		MaxAge:   int(sessionTTL.Seconds()),
	}
}

// sessionRow is a row of the sessions table.
type sessionRow struct {
	UserID    string `db:"user_id"`
	ExpiresAt string `db:"expires_at"`
}

// UserFromRequest resolves the session cookie to a user. Expired sessions
// are deleted; sessions with less than 6 days remaining are slid forward.
func (s *Service) UserFromRequest(r *http.Request) (*User, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil {
		return nil, ErrNoSession
	}
	sum := sha256.Sum256([]byte(cookie.Value))
	tokenHash := hex.EncodeToString(sum[:])

	var row sessionRow
	err = s.db.Get(&row, `SELECT user_id, expires_at FROM sessions WHERE token_hash = ?`, tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, err
	}

	expires, err := parseSQLiteTime(row.ExpiresAt)
	if err != nil {
		log.Printf("[auth] dropping session with bad expiry %q: %v", row.ExpiresAt, err)
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
		return nil, ErrNoSession
	}
	remaining := time.Until(expires)
	if remaining <= 0 {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
		return nil, ErrNoSession
	}
	if remaining < sessionSlideThreshold {
		extended := time.Now().Add(sessionTTL)
		if _, err := s.db.Exec(`UPDATE sessions SET expires_at = ? WHERE token_hash = ?`,
			formatTime(extended), tokenHash); err != nil {
			log.Printf("[auth] sliding session: %v", err)
		}
	}

	return s.GetUserByID(row.UserID)
}

// DestroySession deletes the caller's session row (if any) and clears the
// session cookie.
func (s *Service) DestroySession(w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		sum := sha256.Sum256([]byte(cookie.Value))
		if _, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`,
			hex.EncodeToString(sum[:])); err != nil {
			return err
		}
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124 - the Secure flag is deliberately config-driven (COOKIE_SECURE) so local http dev keeps working
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cfg.CookieSecure,
		MaxAge:   -1,
	})
	return nil
}

// startSessionCleanup purges expired sessions once per hour until ctx is
// cancelled.
func (s *Service) startSessionCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(sessionCleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`,
					formatTime(time.Now().UTC())); err != nil {
					log.Printf("[auth] session cleanup: %v", err)
				}
			}
		}
	}()
}
