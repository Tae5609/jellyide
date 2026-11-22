// Package auth — audit log for admin actions (T19).
package auth

import (
	"log"
	"net/http"
	"time"
)

// Audit actions recorded in the audit_log table.
const (
	// AuditActionSetupComplete is written when the first-run setup wizard
	// creates the initial admin.
	AuditActionSetupComplete = "setup_complete"
	// AuditActionCreateUser is written when an admin (or the CLI) creates
	// a user.
	AuditActionCreateUser = "create_user"
	// AuditActionPatchUser is written when an admin changes a user's
	// fields; the detail lists the changed fields.
	AuditActionPatchUser = "patch_user"
	// AuditActionResetPassword is written when an admin (or the CLI)
	// resets a local user's password.
	AuditActionResetPassword = "reset_password"
	// AuditActionDeleteUser is written when an admin deletes a user.
	AuditActionDeleteUser = "delete_user"
	// AuditActionRegistrationMode is written when the registration mode
	// changes.
	AuditActionRegistrationMode = "set_registration_mode"
)

// RecordAudit appends one row to the audit_log table. actor is the acting
// admin's username for web admin API actions, "cli" for the admin recovery
// CLI and "setup" for the first-run wizard. An audit failure is logged but
// never fails the action itself: the audit log must not take the admin API
// down.
func (s *Service) RecordAudit(actor, action, target, detail string) {
	if _, err := s.db.Exec(
		`INSERT INTO audit_log (at, actor, action, target, detail) VALUES (?, ?, ?, ?, ?)`,
		formatTime(time.Now().UTC()), actor, action, target, detail,
	); err != nil {
		log.Printf("[auth] record audit %s: %v", action, err)
	}
}

// adminActor returns the acting admin's username for an audit row.
func adminActor(r *http.Request) string {
	if u := UserFromContext(r.Context()); u != nil {
		return u.Username
	}
	return "unknown"
}
