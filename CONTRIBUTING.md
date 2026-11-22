# Contributing to JellyIDE

Thanks for taking the time. Two rules cover everything: follow the conventions, and open a
focused PR.

## Read first

- **[docs/plan/CONVENTIONS.md](docs/plan/CONVENTIONS.md)** — the binding rules:
  - **Commits:** Conventional Commits, `<type>(<scope>): <summary>` — imperative, lower-case,
    no trailing period, subject ≤ 72 chars, one logical change per commit.
  - **Branches:** `feat/<topic>`, `fix/<topic>`, `chore/<topic>`, `docs/<topic>`,
    `test/<topic>`, `ci/<topic>`.
  - **Pull requests:** title in commit format (it becomes the squash commit), body follows
    `.github/pull_request_template.md` with every section filled (write `N/A` — never delete
    a section). Merge by squash only, after reviewer APPROVE and green CI.
- **[docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)** — prerequisites, running the stack locally,
  the `make` targets (`fmt`, `lint`, `test`, `build`, `check`), the quality gates every PR
  must pass, and how the task-brief workflow in `docs/plan/` works.

## The short list

1. Branch from `main` using the naming scheme above.
2. Keep the PR to one logical change; cover new behaviour with tests, bug fixes with a
   regression test that fails without the fix.
3. Run `make check` (plus the frontend gates) before pushing.
4. Found a bug you do not fix in the PR? Append it to `docs/plan/BUGS.md` instead of
   silently ignoring or bundling a fix.
5. Never commit secrets, tokens or passwords; keep SQL parameterized; keep user input out
   of shells and CLI option position (use `--`) — the security baseline in CONVENTIONS.

Questions? Open a discussion on the issue tracker first. See also the
[README](README.md) for what the project is and [docs/API.md](docs/API.md) for the HTTP
surface.
