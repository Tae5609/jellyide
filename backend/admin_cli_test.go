// Package main — admin recovery CLI tests (T19).
package main

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"yt-scraper/internal/auth"
	"yt-scraper/internal/db"
)

// seedCLIDB opens a fresh database in a temp dir, points DOWNLOAD_DIR at
// it, and returns the test-side service (for seeding and assertions) plus
// a runner that invokes runAdmin exactly like the process would.
func seedCLIDB(t *testing.T) (*auth.Service, func(args []string, stdin string) (int, string)) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DOWNLOAD_DIR", dir)
	// Keep the environment from bootstrapping an admin through the CLI's
	// own service.
	t.Setenv("ADMIN_PASSWORD", "")

	database, err := db.NewDB(filepath.Join(dir, "jellyide.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	svc, err := auth.NewService(auth.LoadConfig(), database)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	run := func(args []string, stdin string) (int, string) {
		var buf strings.Builder
		code := runAdmin(args, strings.NewReader(stdin), &buf)
		return code, buf.String()
	}
	return svc, run
}

// auditCount returns the number of audit_log rows for (actor, action).
func auditCount(t *testing.T, svc *auth.Service, actor, action string) int {
	t.Helper()
	n, err := svc.CountAudit(actor, action)
	if err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return n
}

func TestAdminCLINoArgsShowsUsage(t *testing.T) {
	seedCLIDB(t)
	code, out := runAndCapture(t, []string{}, "")
	if code != 2 {
		t.Fatalf("no args: exit = %d, want 2", code)
	}
	if !strings.Contains(out, "Usage: server admin <command>") {
		t.Fatalf("usage missing: %s", out)
	}
	code, out = runAndCapture(t, []string{"frobnicate"}, "")
	if code != 2 {
		t.Fatalf("unknown command: exit = %d, want 2", code)
	}
	if !strings.Contains(strings.ToLower(out), "unknown admin command") {
		t.Fatalf("unknown command message missing: %s", out)
	}
	code, _ = runAndCapture(t, []string{"help"}, "")
	if code != 0 {
		t.Fatalf("help: exit = %d, want 0", code)
	}
}

// runAndCapture runs the CLI through runAdmin (DOWNLOAD_DIR is set by the
// per-test seedCLIDB call) and returns the exit code and output.
func runAndCapture(t *testing.T, args []string, stdin string) (int, string) {
	t.Helper()
	var buf strings.Builder
	code := runAdmin(args, strings.NewReader(stdin), &buf)
	return code, buf.String()
}

func TestAdminCLIList(t *testing.T) {
	svc, run := seedCLIDB(t)
	if _, err := svc.CreateLocalUser("alice", "password-123", "", false); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if _, err := svc.CreateLocalUser("bob", "password-123", "", true); err != nil {
		t.Fatalf("create bob: %v", err)
	}

	code, out := run([]string{"list"}, "")
	if code != 0 {
		t.Fatalf("list: exit = %d: %s", code, out)
	}
	for _, want := range []string{"alice", "bob", "IS_ADMIN", "STATUS", "LAST LOGIN", "2 user(s)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list output missing %q:\n%s", want, out)
		}
	}
	if n := auditCount(t, svc, "cli", "list"); n != 1 {
		t.Fatalf("list audit rows = %d, want 1", n)
	}
}

func TestAdminCLIResetPasswordGenerateRevokesSessions(t *testing.T) {
	svc, run := seedCLIDB(t)
	u, err := svc.CreateLocalUser("carol", "old-password-1", "", false)
	if err != nil {
		t.Fatalf("create carol: %v", err)
	}
	// Carol has a live session; the reset must delete it.
	if err := svc.StartSession(httptest.NewRecorder(), u.ID); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	code, out := run([]string{"reset-password", "carol", "--generate"}, "")
	if code != 0 {
		t.Fatalf("reset-password --generate: exit = %d: %s", code, out)
	}

	// --generate prints the new password exactly once.
	generated := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "New password: ") {
			generated = strings.TrimPrefix(line, "New password: ")
		}
	}
	if generated == "" {
		t.Fatalf("generated password not printed:\n%s", out)
	}
	if strings.Count(out, generated) != 1 {
		t.Fatalf("generated password printed more than once:\n%s", out)
	}

	// The new password works, the old one does not.
	updated, err := svc.GetUserByUsername("carol")
	if err != nil {
		t.Fatalf("lookup carol: %v", err)
	}
	if updated.PasswordHash == nil {
		t.Fatal("carol lost her password hash")
	}
	if auth.CheckPassword(*updated.PasswordHash, "old-password-1") {
		t.Fatal("old password still valid after reset")
	}
	if !auth.CheckPassword(*updated.PasswordHash, generated) {
		t.Fatal("generated password does not authenticate")
	}

	// The session was deleted.
	sessions, err := svc.CountUserSessions(u.ID)
	if err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("sessions after reset = %d, want 0", sessions)
	}
	if n := auditCount(t, svc, "cli", auth.AuditActionResetPassword); n != 1 {
		t.Fatalf("reset audit rows = %d, want 1", n)
	}
	// The output never contains the hash or the old password in plaintext
	// beyond the confirmation line.
	if strings.Contains(out, "old-password-1") {
		t.Fatalf("output leaked the old password:\n%s", out)
	}
}

func TestAdminCLIResetPasswordStdin(t *testing.T) {
	svc, run := seedCLIDB(t)
	if _, err := svc.CreateLocalUser("dora", "old-password-1", "", false); err != nil {
		t.Fatalf("create dora: %v", err)
	}

	code, out := run([]string{"reset-password", "dora", "--password-stdin"}, "from-stdin-1\n")
	if code != 0 {
		t.Fatalf("reset-password --password-stdin: exit = %d: %s", code, out)
	}
	updated, err := svc.GetUserByUsername("dora")
	if err != nil {
		t.Fatalf("lookup dora: %v", err)
	}
	if !auth.CheckPassword(*updated.PasswordHash, "from-stdin-1") {
		t.Fatal("stdin password was not applied")
	}
	// A stdin password is never echoed back.
	if strings.Contains(out, "from-stdin-1") {
		t.Fatalf("output leaked the stdin password:\n%s", out)
	}
	// Short stdin password is rejected.
	code, _ = run([]string{"reset-password", "dora", "--password-stdin"}, "short\n")
	if code != 1 {
		t.Fatalf("short stdin password: exit = %d, want 1", code)
	}
}

func TestAdminCLIResetPasswordExternalUser(t *testing.T) {
	svc, run := seedCLIDB(t)
	subject := "sub-external-1"
	if err := svc.CreateUser(&auth.User{
		ID: "11111111-2222-3333-4444-555555555555", Username: "sso-only",
		Provider: "oidc", Subject: &subject,
	}); err != nil {
		t.Fatalf("create external user: %v", err)
	}
	code, out := run([]string{"reset-password", "sso-only", "--password-stdin"}, "whatever-1\n")
	if code != 1 {
		t.Fatalf("reset external user: exit = %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "only available for local users") {
		t.Fatalf("external user message missing: %s", out)
	}
	code, out = run([]string{"reset-password", "ghost", "--password-stdin"}, "whatever-1\n")
	if code != 1 {
		t.Fatalf("reset unknown user: exit = %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "not found") {
		t.Fatalf("unknown user message missing: %s", out)
	}
}

func TestAdminCLICreate(t *testing.T) {
	svc, run := seedCLIDB(t)

	code, out := run([]string{"create", "dave", "--admin", "--password-stdin"}, "dave-pass-1\n")
	if code != 0 {
		t.Fatalf("create: exit = %d: %s", code, out)
	}
	u, err := svc.GetUserByUsername("dave")
	if err != nil {
		t.Fatalf("lookup dave: %v", err)
	}
	if !u.IsAdmin || u.Status != auth.UserStatusActive || u.Provider != "local" {
		t.Fatalf("dave = %+v", u)
	}
	if !auth.CheckPassword(*u.PasswordHash, "dave-pass-1") {
		t.Fatal("dave password not applied")
	}
	if strings.Contains(out, "dave-pass-1") {
		t.Fatalf("output leaked the stdin password:\n%s", out)
	}
	if n := auditCount(t, svc, "cli", auth.AuditActionCreateUser); n != 1 {
		t.Fatalf("create audit rows = %d, want 1", n)
	}

	// --generate prints the password once.
	code, out = run([]string{"create", "erin", "--generate"}, "")
	if code != 0 {
		t.Fatalf("create --generate: exit = %d: %s", code, out)
	}
	if !strings.Contains(out, "New password: ") {
		t.Fatalf("generated password not printed:\n%s", out)
	}

	// Duplicate username.
	code, out = run([]string{"create", "dave", "--password-stdin"}, "other-pass-1\n")
	if code != 1 {
		t.Fatalf("duplicate create: exit = %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "already taken") {
		t.Fatalf("duplicate message missing: %s", out)
	}

	// Invalid username.
	code, out = run([]string{"create", "x"}, "password-123\n")
	if code != 1 {
		t.Fatalf("invalid username: exit = %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "invalid username") {
		t.Fatalf("invalid username message missing: %s", out)
	}

	// No password source and empty stdin: clear error, non-zero exit.
	code, out = run([]string{"create", "frank"}, "")
	if code != 1 {
		t.Fatalf("missing password: exit = %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "missing password input") {
		t.Fatalf("missing password message missing: %s", out)
	}
	if _, err := svc.GetUserByUsername("frank"); err == nil {
		t.Fatal("frank must not exist after a failed password read")
	}
}

func TestAdminCLIPromoteDemote(t *testing.T) {
	svc, run := seedCLIDB(t)
	// Single active admin only: both demote and disable are refused.
	if _, err := svc.CreateLocalUser("boss", "password-123", "", true); err != nil {
		t.Fatalf("create boss: %v", err)
	}
	code, out := run([]string{"demote", "boss"}, "")
	if code != 1 {
		t.Fatalf("demote last admin: exit = %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "last active admin") {
		t.Fatalf("last-admin message missing: %s", out)
	}
	if u, _ := svc.GetUserByUsername("boss"); !u.IsAdmin {
		t.Fatal("boss was demoted despite the guard")
	}

	// Promote a non-admin; a second promote is a no-op.
	if _, err := svc.CreateLocalUser("worker", "password-123", "", false); err != nil {
		t.Fatalf("create worker: %v", err)
	}
	code, out = run([]string{"promote", "worker"}, "")
	if code != 0 {
		t.Fatalf("promote: exit = %d: %s", code, out)
	}
	if u, _ := svc.GetUserByUsername("worker"); !u.IsAdmin {
		t.Fatal("worker was not promoted")
	}
	code, out = run([]string{"promote", "worker"}, "")
	if code != 0 || !strings.Contains(out, "already an admin") {
		t.Fatalf("second promote: exit = %d: %s", code, out)
	}
	if n := auditCount(t, svc, "cli", "promote"); n != 1 {
		t.Fatalf("promote audit rows = %d, want 1 (no-op must not be audited)", n)
	}

	// With a second active admin the first may be demoted.
	code, out = run([]string{"demote", "boss"}, "")
	if code != 0 {
		t.Fatalf("demote with backstop: exit = %d: %s", code, out)
	}
	if u, _ := svc.GetUserByUsername("boss"); u.IsAdmin {
		t.Fatal("boss should be demoted")
	}
	// Demoting a non-admin is a documented no-op.
	code, out = run([]string{"demote", "boss"}, "")
	if code != 0 || !strings.Contains(out, "not an admin") {
		t.Fatalf("demote non-admin: exit = %d: %s", code, out)
	}
	if n := auditCount(t, svc, "cli", "demote"); n != 1 {
		t.Fatalf("demote audit rows = %d, want 1", n)
	}
}

func TestAdminCLIEnableDisable(t *testing.T) {
	svc, run := seedCLIDB(t)
	// Two admins so the guards can be tested independently of each other.
	if _, err := svc.CreateLocalUser("admin-one", "password-123", "", true); err != nil {
		t.Fatalf("create admin-one: %v", err)
	}
	if _, err := svc.CreateLocalUser("admin-two", "password-123", "", true); err != nil {
		t.Fatalf("create admin-two: %v", err)
	}

	// enable approves a pending user.
	pending, err := svc.CreateLocalUser("waiter", "password-123", "", false)
	if err != nil {
		t.Fatalf("create waiter: %v", err)
	}
	if err := svc.SetStatus(pending.ID, auth.UserStatusPending); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	code, out := run([]string{"enable", "waiter"}, "")
	if code != 0 {
		t.Fatalf("enable: exit = %d: %s", code, out)
	}
	if u, _ := svc.GetUserByUsername("waiter"); u.Status != auth.UserStatusActive {
		t.Fatalf("waiter status = %q, want active", u.Status)
	}
	// Enabling an already-active user is a no-op.
	code, out = run([]string{"enable", "waiter"}, "")
	if code != 0 || !strings.Contains(out, "already active") {
		t.Fatalf("second enable: exit = %d: %s", code, out)
	}

	// disable deletes sessions and is audited.
	other, err := svc.CreateLocalUser("victim", "password-123", "", false)
	if err != nil {
		t.Fatalf("create victim: %v", err)
	}
	if err := svc.StartSession(httptest.NewRecorder(), other.ID); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	code, out = run([]string{"disable", "victim"}, "")
	if code != 0 {
		t.Fatalf("disable: exit = %d: %s", code, out)
	}
	if u, _ := svc.GetUserByUsername("victim"); u.Status != auth.UserStatusDisabled {
		t.Fatalf("victim status = %q, want disabled", u.Status)
	}
	sessions, err := svc.CountUserSessions(other.ID)
	if err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("sessions after disable = %d, want 0", sessions)
	}
	if n := auditCount(t, svc, "cli", "disable"); n != 1 {
		t.Fatalf("disable audit rows = %d, want 1", n)
	}

	// The last active admin may not be disabled (admin-one is the only
	// active admin left after demote-free setup: disable admin-two first
	// is refused while admin-two is the last... make admin-one inactive
	// via a non-admin to isolate).
	if _, err := svc.CreateLocalUser("guard", "password-123", "", true); err != nil {
		t.Fatalf("create guard: %v", err)
	}
	// guard is now the only *extra* admin; demote admin-one and guard to
	// leave admin-two the sole active admin.
	if err := svc.UpdateUser(mustUser(t, svc, "admin-one").ID, auth.UserPatch{IsAdmin: boolPtr(false)}); err != nil {
		t.Fatalf("demote admin-one: %v", err)
	}
	if err := svc.UpdateUser(mustUser(t, svc, "guard").ID, auth.UserPatch{IsAdmin: boolPtr(false)}); err != nil {
		t.Fatalf("demote guard: %v", err)
	}
	code, out = run([]string{"disable", "admin-two"}, "")
	if code != 1 {
		t.Fatalf("disable last admin: exit = %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "last active admin") {
		t.Fatalf("last-admin message missing: %s", out)
	}
}

func mustUser(t *testing.T, svc *auth.Service, username string) *auth.User {
	t.Helper()
	u, err := svc.GetUserByUsername(username)
	if err != nil {
		t.Fatalf("lookup %s: %v", username, err)
	}
	return u
}

func TestAdminCLIUnlock(t *testing.T) {
	svc, run := seedCLIDB(t)
	code, out := run([]string{"unlock"}, "")
	if code != 0 {
		t.Fatalf("unlock: exit = %d: %s", code, out)
	}
	for _, want := range []string{"Login rate limiter cleared", "restart"} {
		if !strings.Contains(out, want) {
			t.Fatalf("unlock output missing %q:\n%s", want, out)
		}
	}
	if n := auditCount(t, svc, "cli", "unlock"); n != 1 {
		t.Fatalf("unlock audit rows = %d, want 1", n)
	}
}

func TestAdminCLIHelp(t *testing.T) {
	seedCLIDB(t)
	for _, cmd := range []string{"list", "reset-password", "create", "promote", "demote", "enable", "disable", "unlock"} {
		code, out := runAndCapture(t, []string{cmd, "--help"}, "")
		if code != 0 {
			t.Fatalf("%s --help: exit = %d, want 0", cmd, code)
		}
		if !strings.Contains(out, "Usage: server admin "+cmd) {
			t.Fatalf("%s --help usage missing:\n%s", cmd, out)
		}
	}
}

// TestAdminCLIPasswordPrompt covers the interactive fallback: when stdin
// is not a terminal (a piped strings.Reader in tests), the prompt path
// reads two plain lines and requires them to match.
func TestAdminCLIPasswordPrompt(t *testing.T) {
	svc, run := seedCLIDB(t)

	// Mismatching confirmation.
	code, out := run([]string{"create", "grace"}, "pass-12345\npass-67890\n")
	if code != 1 {
		t.Fatalf("mismatch: exit = %d, want 1: %s", code, out)
	}
	if !strings.Contains(out, "passwords do not match") {
		t.Fatalf("mismatch message missing: %s", out)
	}
	if _, err := svc.GetUserByUsername("grace"); err == nil {
		t.Fatal("grace must not exist after a mismatch")
	}

	// Matching lines: the password is applied (and never echoed).
	code, out = run([]string{"create", "grace"}, "pass-12345\npass-12345\n")
	if code != 0 {
		t.Fatalf("prompt create: exit = %d: %s", code, out)
	}
	u, err := svc.GetUserByUsername("grace")
	if err != nil {
		t.Fatalf("lookup grace: %v", err)
	}
	if !auth.CheckPassword(*u.PasswordHash, "pass-12345") {
		t.Fatal("prompt password not applied")
	}
	if strings.Contains(out, "pass-12345") {
		t.Fatalf("output leaked the prompted password:\n%s", out)
	}
}
