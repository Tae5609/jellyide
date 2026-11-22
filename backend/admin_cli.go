// Package main — admin recovery CLI (T19).
//
// `server admin <command>` operates directly on the JellyIDE database and
// works while the server is running (SQLite WAL with busy_timeout). The
// same binary runs the server when invoked without arguments.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"golang.org/x/term"

	"yt-scraper/internal/auth"
	"yt-scraper/internal/db"
)

// adminActor is the audit_log actor for every CLI action.
const adminActorCLI = "cli"

// adminUsage is the top-level help for the recovery CLI.
const adminUsage = `Usage: server admin <command> [flags]

Operates directly on the JellyIDE database (DOWNLOAD_DIR/jellyide.db) and
works while the server is running (SQLite WAL).

Commands:
  list                     List all users
  reset-password <user>    Reset a local user's password and delete their sessions
  create <user>            Create a local user
  promote <user>           Grant admin rights
  demote <user>            Revoke admin rights (refuses the last active admin)
  enable <user>            Set the user active (also approves pending users)
  disable <user>           Set the user disabled and delete their sessions
  unlock                   Clear the in-memory login rate limiter

Run "server admin <command> --help" for each command's flags.

Docker example:
  docker exec -it jelly-ide ./server admin reset-password admin
`

// runAdmin implements the admin recovery CLI. It returns a process exit
// code: 0 success (or --help), 1 runtime error, 2 usage error.
func runAdmin(args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stdout, adminUsage)
		return 2
	}
	switch args[0] {
	case "help", "--help", "-h":
		_, _ = fmt.Fprint(stdout, adminUsage)
		return 0
	case "list":
		return runAdminList(args[1:], stdin, stdout)
	case "reset-password":
		return runAdminResetPassword(args[1:], stdin, stdout)
	case "create":
		return runAdminCreate(args[1:], stdin, stdout)
	case "promote":
		return runAdminPromote(args[1:], stdin, stdout)
	case "demote":
		return runAdminDemote(args[1:], stdin, stdout)
	case "enable":
		return runAdminEnable(args[1:], stdin, stdout)
	case "disable":
		return runAdminDisable(args[1:], stdin, stdout)
	case "unlock":
		return runAdminUnlock(args[1:], stdin, stdout)
	}
	_, _ = fmt.Fprintf(stdout, "error: unknown admin command %q\n\n%s", args[0], adminUsage)
	return 2
}

// newAdminService opens the database using the same path resolution as
// main and builds the auth service the CLI operates through.
func newAdminService() (*auth.Service, func(), error) {
	downloadDir := getEnv("DOWNLOAD_DIR", "/mnt/media/music")
	if err := os.MkdirAll(downloadDir, 0o755); err != nil { //nolint:gosec // G301 - shared media library dir, same as the server
		return nil, nil, fmt.Errorf("create download dir: %w", err)
	}
	database, err := db.NewDB(filepath.Join(downloadDir, "jellyide.db"))
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	svc, err := auth.NewService(auth.LoadConfig(), database)
	if err != nil {
		_ = database.Close()
		return nil, nil, fmt.Errorf("initialize auth: %w", err)
	}
	return svc, func() { _ = database.Close() }, nil
}

// fail prints an error to stdout (the CLI's testable output channel) and
// returns exit code 1.
func fail(stdout io.Writer, err error) int {
	_, _ = fmt.Fprintf(stdout, "error: %v\n", err)
	return 1
}

// flagExitCode maps a flag.Parse error to an exit code: --help is 0, any
// other parse error is 2.
func flagExitCode(err error) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	return 2
}

// parseFlagsAnyOrder accepts the standard-library flag syntax before or
// after positional arguments. All flags in the admin CLI are booleans, so
// separating options from positionals is unambiguous.
func parseFlagsAnyOrder(fs *flag.FlagSet, args []string) error {
	var options, positionals []string
	positionalOnly := false
	for _, arg := range args {
		if positionalOnly {
			positionals = append(positionals, arg)
			continue
		}
		if arg == "--" {
			positionalOnly = true
			continue
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			options = append(options, arg)
		} else {
			positionals = append(positionals, arg)
		}
	}
	return fs.Parse(append(options, positionals...))
}

// boolPtr returns a pointer to b for UserPatch optional fields.
func boolPtr(b bool) *bool { return &b }

// lookupUser is the shared "find by username" front for the CLI: it turns
// the auth sentinel into a clear error.
func lookupUser(svc *auth.Service, username string) (*auth.User, error) {
	u, err := svc.GetUserByUsername(username)
	if errors.Is(err, auth.ErrNoUser) {
		return nil, fmt.Errorf("user %q not found", username)
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// passwordSource names how a CLI password was provided (audit detail).
type passwordSource string

const (
	sourcePrompt    passwordSource = "prompt"
	sourceStdin     passwordSource = "password-stdin"
	sourceGenerated passwordSource = "generate"
)

// resolvePassword determines the new password for reset-password and
// create: --generate creates a strong random password, --password-stdin
// reads one line from stdin (for scripts), otherwise the user is prompted
// twice without echo (two plain lines when stdin is not a terminal).
func resolvePassword(fs *flag.FlagSet, stdin io.Reader, stdout io.Writer) (string, passwordSource, error) {
	generate := fs.Lookup("generate").Value.String() == "true"
	fromStdin := fs.Lookup("password-stdin").Value.String() == "true"
	if generate && fromStdin {
		return "", "", errors.New("--generate and --password-stdin are mutually exclusive")
	}
	if generate {
		pwd, err := generateStrongPassword()
		if err != nil {
			return "", "", err
		}
		return pwd, sourceGenerated, nil
	}
	if fromStdin {
		br := bufio.NewReader(stdin)
		line, err := br.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", "", fmt.Errorf("read password from stdin: %w", err)
		}
		pwd := strings.TrimRight(line, "\r\n")
		if len(pwd) < auth.MinPasswordLength {
			return "", "", errors.New("password must be at least 8 characters")
		}
		return pwd, sourceStdin, nil
	}
	lines, err := readSecretLines(stdin, stdout, "Enter new password: ", "Repeat new password: ")
	if err != nil {
		return "", "", err
	}
	if lines[0] != lines[1] {
		return "", "", errors.New("passwords do not match")
	}
	if len(lines[0]) < auth.MinPasswordLength {
		return "", "", errors.New("password must be at least 8 characters")
	}
	return lines[0], sourcePrompt, nil
}

// readSecretLines reads one line per prompt without echo when stdin is a
// terminal (golang.org/x/term), otherwise plain lines — scripts and tests
// pipe the passwords in. Prompts go to stdout.
func readSecretLines(stdin io.Reader, stdout io.Writer, prompts ...string) ([]string, error) {
	out := make([]string, len(prompts))
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		for i, p := range prompts {
			_, _ = fmt.Fprint(stdout, p)
			secret, err := term.ReadPassword(int(f.Fd()))
			_, _ = fmt.Fprintln(stdout)
			if err != nil {
				return nil, fmt.Errorf("read password: %w", err)
			}
			out[i] = string(secret)
		}
		return out, nil
	}
	br := bufio.NewReader(stdin)
	for i, p := range prompts {
		_, _ = fmt.Fprint(stdout, p)
		line, err := br.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read password: %w", err)
		}
		if line == "" && errors.Is(err, io.EOF) {
			return nil, errors.New("missing password input on stdin")
		}
		out[i] = strings.TrimRight(line, "\r\n")
	}
	return out, nil
}

// generateStrongPassword returns a 24-character base64url password from
// 18 random bytes (128 bits of entropy).
func generateStrongPassword() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func runAdminList(args []string, _ io.Reader, stdout io.Writer) int {
	usage := "Usage: server admin list\n\nLists all users: id, username, provider, is_admin, status, last login."
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { _, _ = fmt.Fprintln(stdout, usage) }
	if err := fs.Parse(args); err != nil {
		return flagExitCode(err)
	}
	if fs.NArg() != 0 {
		return fail(stdout, errors.New("list takes no arguments"))
	}
	svc, done, err := newAdminService()
	if err != nil {
		return fail(stdout, err)
	}
	defer done()
	users, err := svc.GetUsers()
	if err != nil {
		return fail(stdout, err)
	}
	svc.RecordAudit(adminActorCLI, "list", "", fmt.Sprintf("count=%d", len(users)))
	tw := tabwriter.NewWriter(stdout, 0, 8, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tUSERNAME\tPROVIDER\tIS_ADMIN\tSTATUS\tLAST LOGIN")
	for _, u := range users {
		last := ""
		if u.LastLogin != nil {
			last = *u.LastLogin
		}
		admin := "no"
		if u.IsAdmin {
			admin = "yes"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", u.ID, u.Username, u.Provider, admin, u.Status, last)
	}
	if err := tw.Flush(); err != nil {
		return fail(stdout, err)
	}
	_, _ = fmt.Fprintf(stdout, "%d user(s)\n", len(users))
	return 0
}

func runAdminResetPassword(args []string, stdin io.Reader, stdout io.Writer) int {
	usage := `Usage: server admin reset-password <username> [--password-stdin | --generate]

Resets the password of a local user and deletes all of the user's
sessions. Without --password-stdin or --generate the new password is
entered twice without echo (two plain lines when stdin is not a
terminal). Only local users have a password; OIDC/SAML users are
rejected.`
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { _, _ = fmt.Fprintln(stdout, usage) }
	fs.Bool("password-stdin", false, "read the new password as one line from stdin (for scripts)")
	fs.Bool("generate", false, "generate a strong random password and print it once")
	if err := parseFlagsAnyOrder(fs, args); err != nil {
		return flagExitCode(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	username := fs.Arg(0)
	svc, done, err := newAdminService()
	if err != nil {
		return fail(stdout, err)
	}
	defer done()
	u, err := lookupUser(svc, username)
	if err != nil {
		return fail(stdout, err)
	}
	if u.Provider != "local" {
		return fail(stdout, fmt.Errorf("password reset is only available for local users (%s is %s)", username, u.Provider))
	}
	pwd, source, err := resolvePassword(fs, stdin, stdout)
	if err != nil {
		return fail(stdout, err)
	}
	hash, err := auth.HashPassword(pwd)
	if err != nil {
		return fail(stdout, err)
	}
	if err := svc.UpdatePassword(u.ID, hash); err != nil {
		return fail(stdout, err)
	}
	if err := svc.DeleteUserSessions(u.ID); err != nil {
		return fail(stdout, err)
	}
	svc.RecordAudit(adminActorCLI, auth.AuditActionResetPassword, username, "source="+string(source))
	_, _ = fmt.Fprintf(stdout, "Password for %q reset; existing sessions deleted.\n", username)
	if source == sourceGenerated {
		// The only case where a password is printed: --generate.
		_, _ = fmt.Fprintf(stdout, "New password: %s\n", pwd)
	}
	return 0
}

func runAdminCreate(args []string, stdin io.Reader, stdout io.Writer) int {
	usage := `Usage: server admin create <username> [--admin] [--password-stdin | --generate]

Creates an active local user. Use --admin to grant admin rights. The
username follows the local-account rule (3-32 letters, numbers, dot, dash
or underscore); the password the 8-character minimum.`
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { _, _ = fmt.Fprintln(stdout, usage) }
	adminFlag := fs.Bool("admin", false, "grant admin rights")
	fs.Bool("password-stdin", false, "read the password as one line from stdin (for scripts)")
	fs.Bool("generate", false, "generate a strong random password and print it once")
	if err := parseFlagsAnyOrder(fs, args); err != nil {
		return flagExitCode(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	username := fs.Arg(0)
	if !auth.ValidUsername(username) {
		return fail(stdout, errors.New("invalid username (3-32 letters, numbers, dot, dash or underscore)"))
	}
	svc, done, err := newAdminService()
	if err != nil {
		return fail(stdout, err)
	}
	defer done()
	if _, err := svc.GetUserByUsername(username); err == nil {
		return fail(stdout, fmt.Errorf("username %q already taken", username))
	} else if !errors.Is(err, auth.ErrNoUser) {
		return fail(stdout, err)
	}
	pwd, source, err := resolvePassword(fs, stdin, stdout)
	if err != nil {
		return fail(stdout, err)
	}
	u, err := svc.CreateLocalUser(username, pwd, "", *adminFlag)
	if err != nil {
		return fail(stdout, err)
	}
	svc.RecordAudit(adminActorCLI, auth.AuditActionCreateUser, username, "source="+string(source))
	role := "local user"
	if u.IsAdmin {
		role = "admin"
	}
	_, _ = fmt.Fprintf(stdout, "Created %s %q\n", role, username)
	if source == sourceGenerated {
		// The only case where a password is printed: --generate.
		_, _ = fmt.Fprintf(stdout, "New password: %s\n", pwd)
	}
	return 0
}

func runAdminPromote(args []string, _ io.Reader, stdout io.Writer) int {
	usage := "Usage: server admin promote <username>\n\nGrants admin rights. A no-op if the user is already an admin."
	fs := flag.NewFlagSet("promote", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { _, _ = fmt.Fprintln(stdout, usage) }
	if err := fs.Parse(args); err != nil {
		return flagExitCode(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	username := fs.Arg(0)
	svc, done, err := newAdminService()
	if err != nil {
		return fail(stdout, err)
	}
	defer done()
	u, err := lookupUser(svc, username)
	if err != nil {
		return fail(stdout, err)
	}
	if u.IsAdmin {
		_, _ = fmt.Fprintf(stdout, "%q is already an admin\n", username)
		return 0
	}
	if err := svc.UpdateUser(u.ID, auth.UserPatch{IsAdmin: boolPtr(true)}); err != nil {
		return fail(stdout, err)
	}
	svc.RecordAudit(adminActorCLI, "promote", username, "")
	_, _ = fmt.Fprintf(stdout, "Promoted %q to admin\n", username)
	return 0
}

func runAdminDemote(args []string, _ io.Reader, stdout io.Writer) int {
	usage := "Usage: server admin demote <username>\n\nRevokes admin rights. Refuses the last active admin."
	fs := flag.NewFlagSet("demote", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { _, _ = fmt.Fprintln(stdout, usage) }
	if err := fs.Parse(args); err != nil {
		return flagExitCode(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	username := fs.Arg(0)
	svc, done, err := newAdminService()
	if err != nil {
		return fail(stdout, err)
	}
	defer done()
	u, err := lookupUser(svc, username)
	if err != nil {
		return fail(stdout, err)
	}
	if !u.IsAdmin {
		_, _ = fmt.Fprintf(stdout, "%q is not an admin\n", username)
		return 0
	}
	// Same last-active-admin guard the web admin API applies.
	last, err := svc.IsLastActiveAdmin(u)
	if err != nil {
		return fail(stdout, err)
	}
	if last {
		return fail(stdout, fmt.Errorf("refusing to demote %q: last active admin", username))
	}
	if err := svc.UpdateUser(u.ID, auth.UserPatch{IsAdmin: boolPtr(false)}); err != nil {
		return fail(stdout, err)
	}
	svc.RecordAudit(adminActorCLI, "demote", username, "")
	_, _ = fmt.Fprintf(stdout, "Demoted %q\n", username)
	return 0
}

func runAdminEnable(args []string, _ io.Reader, stdout io.Writer) int {
	usage := "Usage: server admin enable <username>\n\nSets the user active; this also approves pending users."
	fs := flag.NewFlagSet("enable", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { _, _ = fmt.Fprintln(stdout, usage) }
	if err := fs.Parse(args); err != nil {
		return flagExitCode(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	username := fs.Arg(0)
	svc, done, err := newAdminService()
	if err != nil {
		return fail(stdout, err)
	}
	defer done()
	u, err := lookupUser(svc, username)
	if err != nil {
		return fail(stdout, err)
	}
	if u.Status == auth.UserStatusActive {
		_, _ = fmt.Fprintf(stdout, "%q is already active\n", username)
		return 0
	}
	if err := svc.SetStatus(u.ID, auth.UserStatusActive); err != nil {
		return fail(stdout, err)
	}
	svc.RecordAudit(adminActorCLI, "enable", username, "from="+u.Status)
	_, _ = fmt.Fprintf(stdout, "Enabled %q (status active)\n", username)
	return 0
}

func runAdminDisable(args []string, _ io.Reader, stdout io.Writer) int {
	usage := "Usage: server admin disable <username>\n\nSets the user disabled and deletes their sessions. Refuses the last active admin."
	fs := flag.NewFlagSet("disable", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { _, _ = fmt.Fprintln(stdout, usage) }
	if err := fs.Parse(args); err != nil {
		return flagExitCode(err)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	username := fs.Arg(0)
	svc, done, err := newAdminService()
	if err != nil {
		return fail(stdout, err)
	}
	defer done()
	u, err := lookupUser(svc, username)
	if err != nil {
		return fail(stdout, err)
	}
	if u.Status == auth.UserStatusDisabled {
		_, _ = fmt.Fprintf(stdout, "%q is already disabled\n", username)
		return 0
	}
	// Same last-active-admin guard the web admin API applies.
	last, err := svc.IsLastActiveAdmin(u)
	if err != nil {
		return fail(stdout, err)
	}
	if last {
		return fail(stdout, fmt.Errorf("refusing to disable %q: last active admin", username))
	}
	if err := svc.SetStatus(u.ID, auth.UserStatusDisabled); err != nil {
		return fail(stdout, err)
	}
	if err := svc.DeleteUserSessions(u.ID); err != nil {
		return fail(stdout, err)
	}
	svc.RecordAudit(adminActorCLI, "disable", username, "")
	_, _ = fmt.Fprintf(stdout, "Disabled %q; sessions deleted\n", username)
	return 0
}

func runAdminUnlock(args []string, _ io.Reader, stdout io.Writer) int {
	usage := `Usage: server admin unlock

Clears the in-memory login rate limiter.

The limiter lives in the memory of the server process that counted the
failed logins. This command clears the limiter in the process it runs in
and records the action in the audit log. To clear the limiter of a
running server, restart it (for example
"docker compose restart jelly-ide") — a restart clears the in-memory
limiter as well.`
	fs := flag.NewFlagSet("unlock", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { _, _ = fmt.Fprintln(stdout, usage) }
	if err := fs.Parse(args); err != nil {
		return flagExitCode(err)
	}
	if fs.NArg() != 0 {
		return fail(stdout, errors.New("unlock takes no arguments"))
	}
	svc, done, err := newAdminService()
	if err != nil {
		return fail(stdout, err)
	}
	defer done()
	svc.ClearLoginRateLimit()
	svc.RecordAudit(adminActorCLI, "unlock", "", "")
	_, _ = fmt.Fprintln(stdout, "Login rate limiter cleared (in-memory, this process).")
	_, _ = fmt.Fprintln(stdout, "Note: the running server keeps its own in-memory limiter;")
	_, _ = fmt.Fprintln(stdout, "restart the server to clear it.")
	return 0
}
