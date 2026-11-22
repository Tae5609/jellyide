package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrNoUser is returned when a user lookup matches no row.
var ErrNoUser = errors.New("user not found")

// User statuses. Only active users can log in or hold sessions.
const (
	UserStatusActive   = "active"
	UserStatusPending  = "pending"
	UserStatusDisabled = "disabled"
)

// Registration modes. admin_only is the default: no self sign-up, only
// admins create accounts. open: anyone can register and is active
// immediately. approval: anyone can register, but the account is pending
// until an admin approves it.
const (
	RegistrationModeAdminOnly = "admin_only"
	RegistrationModeOpen      = "open"
	RegistrationModeApproval  = "approval"
)

// isValidRegistrationMode reports whether mode is one of the three modes.
func isValidRegistrationMode(mode string) bool {
	switch mode {
	case RegistrationModeAdminOnly, RegistrationModeOpen, RegistrationModeApproval:
		return true
	}
	return false
}

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
	Status       string  `db:"status" json:"status"`
	CreatedAt    string  `db:"created_at" json:"created_at"`
	LastLogin    *string `db:"last_login" json:"last_login"`
}

// userSelectColumns is the column list every user lookup shares.
const userSelectColumns = `id, username, password_hash, provider, subject, email, display_name, is_admin, status, created_at, last_login`

// newID generates a random UUID string for a new user row.
func newID() string {
	return uuid.New().String()
}

// GetUserByUsername looks up a user by exact username.
func (s *Service) GetUserByUsername(username string) (*User, error) {
	var u User
	err := s.db.Get(&u, `SELECT `+userSelectColumns+` FROM users WHERE username = ?`, username)
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
	err := s.db.Get(&u, `SELECT `+userSelectColumns+` FROM users WHERE id = ?`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoUser
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateUser inserts a new user. A user without an explicit status starts
// active.
func (s *Service) CreateUser(u *User) error {
	if u.Status == "" {
		u.Status = UserStatusActive
	}
	_, err := s.db.Exec(
		`INSERT INTO users (id, username, password_hash, provider, subject, email, display_name, is_admin, status) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Username, u.PasswordHash, u.Provider, u.Subject, u.Email, u.DisplayName, u.IsAdmin, u.Status,
	)
	return err
}

// GetUsers lists all users, oldest first.
func (s *Service) GetUsers() ([]User, error) {
	var users []User
	err := s.db.Select(&users, `SELECT `+userSelectColumns+` FROM users ORDER BY created_at, username`)
	return users, err
}

// CreateLocalUser creates an active local user with a bcrypt-hashed
// password. Used by self-registration and the admin create-user endpoint.
func (s *Service) CreateLocalUser(username, password, email string, isAdmin bool) (*User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	u := &User{
		ID:           newID(),
		Username:     username,
		PasswordHash: &hash,
		Provider:     "local",
		Email:        strPtrOrNil(email),
		IsAdmin:      isAdmin,
		Status:       UserStatusActive,
	}
	if err := s.CreateUser(u); err != nil {
		return nil, err
	}
	return u, nil
}

// UserPatch holds the fields an admin may change on a user. Nil fields are
// left untouched; for Email and DisplayName a non-nil pointer to "" clears
// the field to NULL.
type UserPatch struct {
	IsAdmin     *bool
	Status      *string
	Email       *string
	DisplayName *string
}

// UpdateUser applies a patch to the user with id.
func (s *Service) UpdateUser(id string, patch UserPatch) error {
	var sets []string
	var args []interface{}
	if patch.IsAdmin != nil {
		sets = append(sets, "is_admin = ?")
		args = append(args, *patch.IsAdmin)
	}
	if patch.Status != nil {
		sets = append(sets, "status = ?")
		args = append(args, *patch.Status)
	}
	if patch.Email != nil {
		sets = append(sets, "email = ?")
		args = append(args, strPtrOrNil(*patch.Email))
	}
	if patch.DisplayName != nil {
		sets = append(sets, "display_name = ?")
		args = append(args, strPtrOrNil(*patch.DisplayName))
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	_, err := s.db.Exec(`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}

// SetStatus changes a user's status. Callers must handle the side effects
// (e.g. DeleteUserSessions when disabling) — the column change alone does
// not.
func (s *Service) SetStatus(id, status string) error {
	_, err := s.db.Exec(`UPDATE users SET status = ? WHERE id = ?`, status, id)
	return err
}

// CountUserSessions returns the number of session rows for userID.
func (s *Service) CountUserSessions(userID string) (int, error) {
	var n int
	err := s.db.Get(&n, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, userID)
	return n, err
}

// DeleteUserSessions removes every session row belonging to userID.
func (s *Service) DeleteUserSessions(userID string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// DeleteUser removes a user and their sessions.
func (s *Service) DeleteUser(id string) error {
	if err := s.DeleteUserSessions(id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// CountActiveAdmins returns how many users are both admins and active. The
// invariant "at least one active admin" is checked against this count.
func (s *Service) CountActiveAdmins() (int, error) {
	var n int
	err := s.db.Get(&n, `SELECT COUNT(*) FROM users WHERE is_admin = 1 AND status = 'active'`)
	return n, err
}

// settingKeyRegistrationMode is the settings-table key for the registration
// mode. The environment only provides the initial value; once the row
// exists, the database value wins.
const settingKeyRegistrationMode = "registration_mode"

// GetRegistrationMode returns the current registration mode. A missing row
// (possible before the seeding migration has run) reads as the default.
func (s *Service) GetRegistrationMode() (string, error) {
	var v string
	err := s.db.Get(&v, `SELECT value FROM settings WHERE key = ?`, settingKeyRegistrationMode)
	if errors.Is(err, sql.ErrNoRows) {
		return RegistrationModeAdminOnly, nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetRegistrationMode stores the registration mode, creating or replacing
// the settings row.
func (s *Service) SetRegistrationMode(mode string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		settingKeyRegistrationMode, mode,
	)
	return err
}

// seedRegistrationMode writes the env-provided initial registration mode
// without ever overwriting an existing row, so a later admin change in the
// UI keeps winning over the environment.
func (s *Service) seedRegistrationMode(envMode string) error {
	mode := envMode
	if !isValidRegistrationMode(mode) {
		mode = RegistrationModeAdminOnly
	}
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`,
		settingKeyRegistrationMode, mode,
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
	err := s.db.Get(&u, `SELECT `+userSelectColumns+` FROM users WHERE provider = ? AND subject = ?`, provider, subject)
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
//
// Newly created users follow the registration mode: in approval mode they
// start pending until an admin approves them. In admin_only mode this
// function is only reached when the provider's *_AUTO_CREATE environment
// flag is on, so the user is created active. Existing users keep their
// status: the per-login update path (UpdateExternalIdentity) only touches
// email, display name and the admin flag.
func (s *Service) FindOrCreateExternalUser(provider, subject, username, email, displayName string, isAdmin bool) (*User, error) {
	name := username
	if name == "" {
		name = subject
	}
	if name == "" {
		return nil, errors.New("external user needs a username or a subject")
	}

	status := UserStatusActive
	if mode, err := s.GetRegistrationMode(); err == nil && mode == RegistrationModeApproval {
		status = UserStatusPending
	}

	// Two concurrent first logins can both pass the lookups below. The
	// UNIQUE indexes then reject the loser, so retry: a (provider, subject)
	// collision means another request already created the user, and a
	// username collision means the next suffix must be chosen.
	const maxAttempts = 10
	for attempt := 0; attempt < maxAttempts; attempt++ {
		existing, err := s.GetUserByProviderSubject(provider, subject)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, ErrNoUser) {
			return nil, err
		}

		candidate := name
		if _, err := s.GetUserByUsername(candidate); !errors.Is(err, ErrNoUser) {
			if err != nil {
				return nil, err
			}
			// Username taken: append the provider (then a counter) to the
			// resolved name until a free unique name is found.
			var uerr error
			if candidate, uerr = s.uniqueExternalName(candidate, provider); uerr != nil {
				return nil, uerr
			}
		}

		u := &User{
			ID:          newID(),
			Username:    candidate,
			Provider:    provider,
			Subject:     &subject,
			Email:       strPtrOrNil(email),
			DisplayName: strPtrOrNil(displayName),
			IsAdmin:     isAdmin,
			Status:      status,
		}
		if err := s.CreateUser(u); err != nil {
			if isUniqueConstraintErr(err) {
				continue
			}
			return nil, err
		}
		return u, nil
	}
	return nil, errors.New("could not allocate a unique external username")
}

// isUniqueConstraintErr reports whether err is a SQLite UNIQUE violation.
func isUniqueConstraintErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
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
