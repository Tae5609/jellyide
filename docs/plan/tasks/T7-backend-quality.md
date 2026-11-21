# T7: Backend quality tooling and unit tests

Branch `test/backend-quality`. Read `docs/plan/CONVENTIONS.md`, `.github/workflows/ci.yml`, and all of `backend/`.

## Do
1. **golangci-lint v2.** Add `backend/.golangci.yml` (`version: "2"`).
   - Linters: `errcheck`, `govet`, `staticcheck`, `ineffassign`, `unused`, `gosec`, `bodyclose`, `misspell`, `gocritic`, `revive` (default rules), `errorlint`, `nilerr`.
   - Formatters: `gofmt`, `goimports` (local prefix `yt-scraper`).
   - Exclude only with a written reason, inline `//nolint:<linter> // reason`, or in config for test files only.
   - Pin one exact v2.x version. Install it locally with `go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@<that version>` and use the same version in CI.
2. **Fix all lint findings** in `backend/internal/{api,download,jobs,library,metadata,scanner}` and `backend/internal/db`. Behaviour-preserving fixes only (handle errors, close bodies, and so on). If a finding is a real bug, fix it, add a test, and say so in the PR body. If it is in `backend/internal/auth/` or `backend/main.go`, fix it only when the rebase brings T1 code in. Keep those fixes lint-only.
3. **Unit tests**, table-driven, using `t.TempDir()` with no network and no real yt-dlp:
   - `metadata`: `findSignificantPatterns`, `isJunk`, and every preview operation (replace, prefix, suffix, regex, invalid regex error).
   - `library`: `sanitizeFilename` (including `..`, `/`, unicode and empty strings), and Ensure/Upsert/Get/Rename/Delete against a temp sqlite plus a temp music dir.
   - `jobs`: create, update and clear-completed.
   - `scanner`: extract ffprobe-JSON parsing into a pure function if it is not one already (minimal refactor), then test it with fixture JSON in `testdata/`.
   - `download`: test the yt-dlp argument builder. Extract a pure `buildArgs(...)` if needed.
   - `api`: `httptest` tests for input validation (bad JSON → 400, missing url → 400).
   - Target ≥ 60% statement coverage for these packages. Print `go test -cover ./...` in the PR.
4. **Python.** Add `backend/pyproject.toml` with ruff config (line-length 100, rules `E,F,W,I,B,UP`). Fix `update_metadata.py` to pass `ruff check` and `ruff format --check`, replacing the bare except with a specific one.
5. **Makefile** at the repo root, with targets:

   | Target | Runs |
   |---|---|
   | `fmt` | goimports, gofmt, ruff format, and prettier if present |
   | `lint` | golangci-lint, ruff check, and frontend lint if the script exists |
   | `test` | `go test -race -cover ./...`, plus frontend tests if they exist |
   | `build` | backend and frontend builds |
   | `check` | lint, test, build |

   Each target runs inside the correct sub-directory.
6. **CI** (`.github/workflows/ci.yml`): in the backend job add golangci-lint (official action, pinned version), `go test -race -coverprofile=coverage.out ./...`, and a step that prints the coverage total to `$GITHUB_STEP_SUMMARY`. Add a `python` job running ruff check and ruff format --check.

## Do NOT
- Change API behaviour or frontend code.
- Touch `backend/internal/auth/` beyond lint fixes after rebase.

## Verify
```
cd backend && gofmt -l . && golangci-lint run ./... && go vet ./... && go test -race -cover ./... && go build ./...
ruff check backend && ruff format --check backend
```
Commits, for example: `ci(api): add golangci-lint and race tests`, `test(metadata): cover preview operations`, `fix(...)` for each real bug.
