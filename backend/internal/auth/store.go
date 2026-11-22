package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrNoUser is returned when a user lookup matches no row.
var ErrNoUser = errors.New("user not found")

// User is a row in the users table.
//
// JSON tags define the shape returned by the auth API. Sensitive fields
// (password_hash, subject) are never serialized.
type User struct {
	ID           string  `db:"id" json:"id"`
	Username     string  `db:"username" json:"username"`
	PasswordHash *string `db:"password_hash" json:"-"`
	Provider     string  `db:"provider" json:"provider"`
	Subject      *string `db:"subject" json:"-"`
	Email        *string `db:"email" json:"email"`
	DisplayName  *string `db:"display_name" json:"display_name"`
	IsAdmin      bool    `db:"is_admin" json:"is_admin"`
}

// newID generates a random UUID string for a new user row.
func newID() string {
	return uuid.New().String()
}

// GetUserByUsername looks up a user by exact username.
func (s *Service) GetUserByUsername(username string) (*User, error) {
	var u User
	err := s.db.Get(&u, `SELECT id, username, password_hash, provider, subject, email, display_name, is_admin FROM users WHERE username = ?`, username)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoUser
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUserByID looks up a user by primary key.
func (s *Service) GetUserByID(id string) (*User, error) {
	var u User
	err := s.db.Get(&u, `SELECT id, username, password_hash, provider, subject, email, display_name, is_admin FROM users WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoUser
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser inserts a new user.
func (s *Service) CreateUser(u *User) error {
	_, err := s.db.Exec(
		`INSERT INTO users (id, username, password_hash, provider, subject, email, display_name, is_admin) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, u.Provider, u.Subject, u.Email, u.DisplayName, u.IsAdmin,
	)
	return err
}

// UpdatePassword replaces the password hash for a user.
func (s *Service) UpdatePassword(id, passwordHash string) error {
	_, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, id)
	return err
}

// UpdateExternalIdentity syncs the mutable identity fields (email, display
// name, admin flag) for an existing external-provider user on each login.
func (s *Service) UpdateExternalIdentity(id, email, displayName string, isAdmin bool) error {
	_, err := s.db.Exec(`UPDATE users SET email = ?, display_name = ?, is_admin = ? WHERE id = ?`,
		strPtrOrNil(email), strPtrOrNil(displayName), isAdmin, id)
	return err
}

// TouchLastLogin records the current time as the user's last login.
func (s *Service) TouchLastLogin(id string) error {
	_, err := s.db.Exec(`UPDATE users SET last_login = ? WHERE id = ?`, formatTime(time.Now().UTC()), id)
	return err
}

// CountUsers returns the total number of users.
func (s *Service) CountUsers() (int, error) {
	var n int
	err := s.db.Get(&n, `SELECT COUNT(*) FROM users`)
	return n, err
}

// GetUserByProviderSubject looks up a user by its (provider, subject) pair.
func (s *Service) GetUserByProviderSubject(provider, subject string) (*User, error) {
	var u User
	err := s.db.Get(&u, `SELECT id, username, password_hash, provider, subject, email, display_name, is_admin FROM users WHERE provider = ? AND subject = ?`, provider, subject)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoUser
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// FindOrCreateExternalUser returns the user for (provider, subject), creating
// it on first use. Intended for external identity providers (T4 OIDC, T5
// SAML). If the resolved username is already taken, "-<provider>" is
// appended, then "-2", "-3", ... until a free unique name is found.
func (s *Service) FindOrCreateExternalUser(provider, subject, username, email, displayName string, isAdmin bool) (*User, error) {
	// Check by unique (provider, subject) first.
	existing, err := s.GetUserByProviderSubject(provider, subject)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, ErrNoUser) {
		return nil, err
	}

	name := username
	if name == "" {
		name = subject
	}
	if name == "" {
		return nil, errors.New("external user needs a username or a subject")
	}
	if _, err := s.GetUserByUsername(name); !errors.Is(err, ErrNoUser) {
		if err != nil {
			return nil, err
		}
		// Username taken: append the provider (then a counter) to the
		// resolved name until a free unique name is found.
		var uerr error
		if name, uerr = s.uniqueExternalName(name, provider); uerr != nil {
			return nil, uerr
		}
	}

	u := &User{
		ID:          newID(),
		Username:    name,
		Provider:    provider,
		Subject:     &subject,
		Email:       strPtrOrNil(email),
		DisplayName: strPtrOrNil(displayName),
		IsAdmin:     isAdmin,
	}
	if err := s.CreateUser(u); err != nil {
		return nil, err
	}
	return u, nil
}

// uniqueExternalName returns a username that is not in use:
// base-<provider>, base-<provider>-2, base-<provider>-3, ...
func (s *Service) uniqueExternalName(base, provider string) (string, error) {
	suffix := base + "-" + provider
	for n := 2; ; n++ {
		if _, err := s.GetUserByUsername(suffix); errors.Is(err, ErrNoUser) {
			return suffix, nil
		} else if err != nil {
			return "", err
		}
		suffix = fmt.Sprintf("%s-%d", base+"-"+provider, n)
	}
}

// randomTokenHash returns the sha256 hex digest of a fresh 32 random bytes.
// The raw token is returned alongside so it can be set as the session cookie.
func randomTokenHash() (token, tokenHash string, err error) {
	raw := make([]byte, 32)
	if _, err = rand.Read(raw); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	return token, hex.EncodeToString(sum[:]), nil
}

func strPtrOrNil(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

// formatTime renders time in a layout SQLite DATETIME accepts and sorts
// correctly. All auth times are stored in UTC.
func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

// parseSQLiteTime parses the DATETIME layouts used by this codebase:
// our own "2006-01-02 15:04:05" (UTC) and SQLite's CURRENT_TIMESTAMP form.
func parseSQLiteTime(v string) (time.Time, error) {
	layouts := []string{
		"2006-01-02 15:04:05",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, v, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("unrecognized time format: " + v)
}
