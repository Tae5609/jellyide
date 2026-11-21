# Engineering conventions (binding for every agent)

## Commits: Conventional Commits
Format: `<type>(<scope>): <summary>`. Use the imperative mood, lower-case, no trailing period, and keep the subject at 72 characters or fewer.

- **types:** `feat` `fix` `refactor` `perf` `test` `docs` `style` `build` `ci` `chore`
- **scopes:** `auth` `oidc` `saml` `users` `ui` `mobile` `library` `metadata` `download` `scanner` `jobs` `api` `db` `deps` `docker` `plan`
- **body** (optional, wrapped at 72): explains *why*, not what.
- **breaking change:** `feat(auth)!: ...` plus a `BREAKING CHANGE:` footer.
- Examples:
  - `feat(auth): add local login with bcrypt and session cookies`
  - `fix(download): prevent yt-dlp option injection via url param`
  - `test(metadata): cover regex preview edge cases`
- Make one logical change per commit. Never commit "wip", "fix stuff" or "update".
- Commits on a branch that is not pushed yet may be reworded with `git commit --amend` or `git rebase`. Pushed history is never rewritten, except after a rebase on `origin/main` using `--force-with-lease` on the feature branch.

## Branches
`feat/<topic>`, `fix/<topic>`, `chore/<topic>`, `docs/<topic>`, `test/<topic>`, `ci/<topic>`.

## Pull requests
- **Title:** same format as a commit, because it becomes the squash commit on `main`. Example: `feat(auth): local username/password login and sessions`.
- **Body:** use `.github/pull_request_template.md` exactly, with every section filled. Write "N/A" when a section does not apply, and never delete a section.
- One task brief = one PR. Keep PRs focused, and don't bundle unrelated changes.
- Merge by squash only, after reviewer APPROVE and green CI.

## Code quality gates (each PR must pass all of them)
- **Go:** `gofmt`/`goimports` clean, `go vet`, `golangci-lint run` (config `backend/.golangci.yml`, added in T7), and `go test -race ./...`.
- **Frontend:** `npm run lint` (zero warnings), `npm run format:check` (Prettier), `CI=true npm test -- --watchAll=false`, and `CI=true npm run build` (added in T9; before T9 lands, build only).
- **Python** (`backend/update_metadata.py`): `ruff check` and `ruff format --check` (added in T7).
- **Tests:** new behaviour needs tests. Bug fixes need a test that fails before the fix.
- Until a gate's tooling task has merged, run whatever gates already exist.

## Bugs found out of scope
Do not fix bugs outside your brief. Append them to `docs/plan/BUGS.md` as `- [ ] <file:line> — <bug> — <impact> (found in <task>)`. The product owner schedules them.

## Security baseline
- Never log secrets, tokens, passwords or session IDs.
- Parameterized SQL only.
- Validate every path that touches the filesystem (no `..` or absolute-path escapes from the music dir).
- Never pass user input to a shell or as a CLI option; use `--` before positional args.
- Outbound HTTP to user-supplied URLs only through an allowlist.
