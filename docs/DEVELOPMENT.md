# Development guide

How to build, run, test and contribute to JellyIDE locally, and how the plan-driven agent
workflow in `docs/plan/` organises work. The commit, PR and quality-gate rules themselves
are binding in [docs/plan/CONVENTIONS.md](plan/CONVENTIONS.md); this file is the practical
guide. Endpoints: [API.md](API.md). System design: [ARCHITECTURE.md](ARCHITECTURE.md).

## Table of contents

- [Prerequisites](#prerequisites)
- [First checkout](#first-checkout)
- [Running locally](#running-locally)
- [Makefile targets](#makefile-targets)
- [Quality gates](#quality-gates)
- [Tests](#tests)
- [Project layout](#project-layout)
- [Environment variables for local runs](#environment-variables-for-local-runs)
- [How the plan and agent workflow works](#how-the-plan-and-agent-workflow-works)

## Prerequisites

| Tool | Version / pin | Needed for |
|---|---|---|
| Go | 1.25 (`backend/go.mod`: `go 1.25.0`; CI uses setup-go `go-version: "1.25"`) | backend build, tests |
| Node.js | 20 (`Dockerfile` `node:20-alpine`; CI setup-node `node-version: 20`) | frontend build and tests |
| npm | ships with Node | `npm ci`, CRA scripts |
| Python 3 | 3.x | tag helper `backend/update_metadata.py` |
| `mutagen` (pip) | any recent | tag writing from the Python helper |
| ffmpeg / ffprobe | any recent | library scanner |
| yt-dlp (pip) | any recent | downloads and `/api/info` |
| `golangci-lint` | **2.15.0** (pinned in `Makefile` `GOLANGCI_LINT_VERSION`, CI action `v2.15.0`, config `backend/.golangci.yml`) | Go lint gate |
| `ruff` | **0.17.6** (pinned in `Makefile` `RUFF_VERSION`, CI `astral-sh/ruff-action@v4.1.0`, config `backend/pyproject.toml`) | Python lint/format gate |
| `gofmt` + `goimports` | with Go | formatting gate (`make fmt`) |
| Docker + Docker Compose | v2 (`docker compose`, plugin form) | container runs |

The tool pins in the `Makefile`, `backend/.golangci.yml` and `.github/workflows/ci.yml` are
kept in sync; `make lint` aborts when a different linter version is on the `PATH`.

## First checkout

```bash
git clone <your-fork> && cd jellyide
cd backend && go mod download && cd ..
cd frontend && npm ci && cd ..
```

## Running locally

The server serves the API, the media files and the built SPA from one process. It expects
the frontend build at `./frontend/build`, relative to its **working directory** — so run the
server from the repository root.

1. Build the SPA once:

   ```bash
   cd frontend && npm run build && cd ..
   ```

2. Start the backend from the repository root (after `cd backend && go build -o
   ../jellyide-server main.go`, or with `go run` inside `backend/`):

   ```bash
   cd backend
   ADMIN_PASSWORD='change-me-123' DOWNLOAD_DIR="$HOME/Music" PORT=8080 go run .
   ```

    - The first start with an empty database bootstraps the admin: with
      `ADMIN_PASSWORD` set, the admin account is created from `ADMIN_USERNAME` (default
      `admin`) + `ADMIN_PASSWORD`. With it unset, the server starts in first-run setup
      mode instead — open `http://localhost:8080/setup` and use the one-time setup token
      from the server log (see [DEPLOYMENT.md](DEPLOYMENT.md) and
      [AUTHENTICATION.md](AUTHENTICATION.md)).
    - `DOWNLOAD_DIR` (default `/mnt/media/music`) holds the music tree **and** `jellyide.db`.
    - Open `http://localhost:8080` and sign in.

3. Day-to-day frontend work with the CRA dev server:

   ```bash
   cd frontend && npm start    # react-scripts on :3000
   ```

   The dev server does not proxy `/api` (`frontend/package.json` has no `proxy` key), so
   either point it at the API via a `proxy` entry you add locally, or keep using the built
   SPA served by the Go process.

4. Or run everything in the development container:

   ```bash
   docker compose up -d --build
   ```

    See [DEPLOYMENT.md](DEPLOYMENT.md) (deployment, the full env-var table, reverse-proxy
    and the production compose file).

## Makefile targets

From the repository root (`Makefile`):

| Target | What it runs |
|---|---|
| `make fmt` | `gofmt -w .` (and `goimports -w -local yt-scraper .` when installed) in `backend/`, `ruff format backend`, `npm run format` (Prettier, `--write`) in `frontend/` |
| `make lint` | version pin check, `golangci-lint run ./...` in `backend/`, `ruff check backend`, `npm run lint && npm run format:check` (ESLint zero warnings + Prettier check) in `frontend/` |
| `make test` | `go test -race -cover ./...` in `backend/`, `npm run test:ci` (Jest, `--watchAll=false --coverage`) in `frontend/` |
| `make build` | `go build ./...`, `npm ci` if `node_modules` is missing, `npm run build` |
| `make check` | `lint` → `test` → `build`, then the auth smoke script (`bash -n scripts/smoke-auth.sh` + `./scripts/smoke-auth.sh`) |

## Quality gates

Every PR must pass all gates that exist today (CONVENTIONS: *"Until a gate's tooling task
has merged, run whatever gates already exist"*). The same set runs in
`.github/workflows/ci.yml`:

| Gate | Command | Notes |
|---|---|---|
| Go formatting | `gofmt`/`goimports` clean (CI: `test -z "$(gofmt -l .)"`) | `make fmt` |
| Go vet | `go vet ./...` in `backend/` | |
| Go lint | `golangci-lint run ./...` with `backend/.golangci.yml` | pinned 2.15.0 |
| Go tests | `go test -race ./...` | CI adds `-coverprofile` and posts the total with `go tool cover -func` |
| Python | `ruff check backend` and `ruff format --check` | pinned 0.17.6 |
| Frontend lint | `npm run lint` | ESLint over `src`, `--max-warnings=0` |
| Frontend format | `npm run format:check` | Prettier over `src` |
| Frontend tests | `npm run test:ci` | react-scripts/jest, `--watchAll=false --coverage` |
| Frontend build | `npm run build` | `CI=true` in CI, so warnings fail the build (matching the lint gate) |
| Auth smoke | `./scripts/smoke-auth.sh` | CI job `backend` (after the build), `make check` |
| Docker | `docker build .` | CI job `docker` |

New behaviour needs tests; a bug fix needs a test that fails before the fix. Bugs found out
of scope are appended to [docs/plan/BUGS.md](plan/BUGS.md), not fixed inline.

## Tests

- **Backend** — `go test -race ./...` from `backend/`; every package has its `_test.go`
  suite (`auth` covers login/register/admin/OIDC/SAML/setup flows, `api` covers the
  handlers, `db`, `jobs`, `download`, `library`, `metadata`, `musicbrainz`, `scanner`,
  `syncconf`, `ytdlp`, `fsutil`).
- **Python helper** — `pytest backend/tests/test_update_metadata.py`.
- **Frontend** — `npm test` (react-scripts/jest), watch-all disabled in CI via
  `npm run test:ci`; suites live next to the sources (`*.test.js`).
- **Auth end-to-end smoke** — `scripts/smoke-auth.sh` boots the built server on port 18080
  with a temporary `DOWNLOAD_DIR` and asserts the auth surface (401/200/403/204 per check);
  wired into CI and `make check`.

## Project layout

```
.
├── Makefile                    # fmt / lint / test / build / check
├── Dockerfile                  # 3-stage: go build → node build → alpine runtime
├── docker-compose.yml          # development (build .)
├── docker-compose.prod.yml     # prebuilt image + healthcheck
├── .env.example                # template for the prod compose
├── CONTRIBUTING.md             # pointer to conventions
├── README.md                   # pitch, features, quick start
├── .github/
│   ├── workflows/ci.yml        # backend, python, frontend, docker jobs
│   └── pull_request_template.md
├── backend/                    # Go module `yt-scraper`
│   ├── main.go                 # server entry (auth, router, /media, SPA)
│   ├── go.mod / go.sum
│   ├── .golangci.yml
│   ├── pyproject.toml          # ruff config
│   ├── update_metadata.py      # tag writer (mutagen)
│   ├── tests/                  # pytest for the helper
│   └── internal/
│       ├── api/                # route table + handlers
│       ├── auth/               # config, store, sessions, middleware,
│       │                       # local login, admin, OIDC, SAML
│       ├── db/                 # pools + migrate()
│       ├── download/           # yt-dlp queue + dispatcher
│       ├── jobs/               # persistent job manager
│       ├── library/            # artists/albums/tracks service
│       ├── metadata/           # analyze, preview, apply, history, cover, lookup
│       ├── musicbrainz/        # rate-limited MB client
│       └── scanner/            # walk + ffprobe indexer
├── frontend/                   # CRA (react-scripts), ESLint + Prettier gates
│   ├── package.json
│   ├── public/
│   └── src/
│       ├── api.js              # apiFetch: /api prefix, CSRF header, 401 event
│       ├── App.js              # routes, page shell
│       ├── auth/               # AuthContext, redirect helpers
│       ├── components/         # ModalContext, UsersNavLink
│       └── pages/              # Downloads, Library, Metadata, Jobs, Sync
│                               # (Conflicts), Settings, Login, Register,
│                               # Users, Setup; metadata/ editor panels
├── docs/
│   ├── ARCHITECTURE.md  API.md  METADATA.md  DEVELOPMENT.md
│   ├── DEPLOYMENT.md · AUTHENTICATION.md · dev/keycloak/
│   └── plan/                 # the workflow below
│       ├── PLAN.md           # roadmap and target design
│       ├── CONVENTIONS.md    # binding rules
│       ├── STATUS.md         # wave board, decisions, ideas
│       ├── BUGS.md           # deferred bug log
│       ├── logs/jobs/        # agent job prompts, logs, reports (gitignored)
│       └── tasks/            # T1…T19 task briefs
└── scripts/agents/           # dispatch.sh, status.sh, wait.sh
```

## Environment variables for local runs

Read by `backend/main.go`, `auth.LoadConfig`, `auth.LoadOIDCConfig` and
`auth.LoadSAMLConfig`. The authoritative, complete table (names, defaults, description)
lives in [DEPLOYMENT.md](DEPLOYMENT.md); the everyday set:

`PORT` (8080) · `DOWNLOAD_DIR` (/mnt/media/music) · `AUTH_ENABLED` (true) ·
`AUTH_LOCAL_ENABLED` (true) · `ADMIN_USERNAME` (admin) · `ADMIN_PASSWORD` (—) ·
`REGISTRATION_MODE` (admin_only, seeds the database once) · `COOKIE_SECURE` (false) ·
`TRUST_PROXY` (false) · `PUBLIC_URL` (http://localhost:8080) · `CONCURRENT_DOWNLOAD`
(2) · `DOWNLOAD_MAX_RETRIES` (2, `0` disables) · `YTDLP_MANAGED` (true) ·
`SYNC_CONFLICT_POLICY` (ask, seeds the settings table once) ·
`OIDC_ENABLED`/`SAML_ENABLED` (false) and their `OIDC_*`/`SAML_*` families.

Note: `CONCURRENT_DOWNLOAD` is read at startup by `download.NewDownloader` (a missing,
invalid or below-1 value falls back to the default of 2, values above 32 are clamped to
32); the limit can still be changed at runtime through `POST /api/config` (admin), which
is not persisted across restarts.

## How the plan and agent workflow works

Development is driven by written briefs and carried out by opencode agents under an
orchestrator — humans review at the PR boundary.

- `docs/plan/PLAN.md` — the roadmap: scope, target design, the ordered task list
  (`T1` … `T19`).
- `docs/plan/tasks/T<n>-<slug>.md` — one **brief** per task: branch name, files owned, do /
  do not, and the exact `Verify` commands. One brief = one PR.
- `docs/plan/CONVENTIONS.md` — binding for every agent: Conventional Commits
  (`<type>(<scope>): <summary>`, ≤72 chars), branch naming (`feat/…`, `fix/…`, `docs/…`),
  PR format (`.github/pull_request_template.md`, squash merge after APPROVE + green CI),
  quality gates, the security baseline, and the `BUGS.md` append protocol.
- `docs/plan/STATUS.md` — the wave board: every task with branch, PR number and status
  (`TODO` → dispatched → reviewed → `MERGED`), plus dated **Decisions** and out-of-scope
  **Ideas**. The orchestrator keeps it current after every state change.
- `docs/plan/BUGS.md` — bugs found out of scope; the product owner schedules the fixes.
- `scripts/agents/dispatch.sh <job> <agent> <worktree> <prompt>` — starts a worker
  (`opencode run --pure --agent …`) detached, non-blocking, two lanes with key A (max 2
  workers) and key B (max 3); `status.sh` / `wait.sh` poll them. Job prompts, logs and
  reports land in `docs/plan/logs/jobs/` (gitignored).
- Agents (`​.opencode/agents/`): `orchestrator` (plans, dispatches, reviews, ships),
  `jelly-dev` / `jelly-dev-b` (implement one brief in one git worktree, run every gate,
  commit in Conventional Commits), `jelly-fast-b` (docs and mechanical fixes), and
  `jelly-reviewer` / `jelly-reviewer-b` (review the worktree against the brief and the
  conventions, re-run all gates, never edit). Findings from review either get fixed in the
  PR round or are ticked into `BUGS.md`.

A typical life cycle of a change: brief → worktree + branch → dev agent implements and runs
the gates → reviewer agent APPROVE → orchestrator pushes and opens the PR → CI green →
squash-merge → `STATUS.md` updated.

## See also

- [CONTRIBUTING.md](../CONTRIBUTING.md)
- [docs/plan/CONVENTIONS.md](plan/CONVENTIONS.md)
- [docs/plan/STATUS.md](plan/STATUS.md)
