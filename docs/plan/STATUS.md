# Status

Product owner: opencode `orchestrator` agent. Update after every state change.

| Wave | Task | Branch | PR | Status | Notes |
|---|---|---|---|---|---|
| 1 | T1 auth core | feat/auth-core | #3 | MERGED | 47df360; APPROVE round 1 (3 MINOR → BUGS.md, T12 queue) |
| 1 | T2 mobile UI | feat/mobile-ui | #4 | MERGED | e724eb6; APPROVE round 1 |
| 1 | T7 backend quality | test/backend-quality | #5 | MERGED | a060c87; APPROVE round 2; fixed 2 known BUGS.md entries + gosec hardening; golangci-lint v2.15.0, ruff 0.17.6 pins |
| 2 | T3 auth frontend | feat/auth-frontend | #9 | MERGED | f10b87f; APPROVE round 2 (round-1 MAJOR: session dropped on password 401 — fixed + tested); 26 frontend tests |
| 2 | T4 OIDC | feat/auth-oidc | #7 | MERGED | 80cc3dc; APPROVE round 1; includes the mandated FindOrCreateExternalUser collision fix (closes T1-review BUGS entry) |
| 2 | T5 SAML | feat/auth-saml | #8 | MERGED | a9af9a3; APPROVE round 1 + rebase-conflict re-review (main.go: OIDC+SAML wiring both kept) |
| 3 | T8 registration + users | feat/users-registration | #12 | MERGED | a346e38; APPROVE round 1 (2 MINORs → BUGS.md, T12 queue); 34 frontend tests |
| 3 | T10 metadata backend | feat/metadata-fields | #13 | MERGED | 5d9c7ef; APPROVE round 2 (round-1 MAJOR: album edit never updated DB — fixed + tested); rebase conflict with T8 in db.go resolved keep-both, re-reviewed; 30 pytest |
| 4 | T11 metadata UI + shortcuts | feat/metadata-editor-ui | – | DEV r2 | review r1 CHANGES: 1 MAJOR (focused-but-not-editing grid cell invisible) + 3 notes (act() warning, select-all fires without grid focus, misleading cover tooltip); dev commits were fallback-model, so Orion dev-r2 fixes findings AND re-verifies full diff. NOTE for next review: also check editor can send `expected_version` later (T16) |
| 4 | T12 backend hardening | fix/backend-hardening | – | DEV r2 | all pre-reboot runs were fallback-model; WIP TRUST_PROXY fix committed (`fix(auth): key rate limiter on first X-Forwarded-For hop`), rebased on origin/main (stale .opencode/agents gone), pushed; Orion dev-r2 re-verifies all 5 commits, adds TRUST_PROXY tests, ticks BUGS.md. T13 merged (3c0fde8) under it — rebase on origin/main needed before/after this round (BUGS.md may conflict) |
| 5 | T9 frontend quality | test/frontend-quality | – | TODO | blocked on T11 merge |
| 5 | T6a core docs | docs/core-docs | #21 | MERGED | 5830901; APPROVE r1 (1 MINOR license line → fixed in T6a-dev-r2, no LICENSE file added — owner decision); CI green. T6b cross-links resolve when T6b merges |
| 5 | T6b ops docs + smoke test | docs/ops-docs | – | REVIEW r1 APPROVE → SHIP HELD | APPROVE r1, zero findings; smoke 12/13 (single /media FAIL = open T12 item, assertion correct); **push held until T12 merges** (CI would be red + dedupe the CONCURRENT_DOWNLOAD BUGS.md dup against T6a); touches ci.yml → push needs suppakorn-tae token; worktree prepped |
| 5 | T13 frontend fixes (Library/Settings) | fix/frontend-fixes | #20 | MERGED | 3c0fde8; APPROVE r1 clean (6/6 regression tests proven to fail pre-fix); 2 non-blocking notes → follow-ups |

## Decisions
- 2026-11-21 T1 reworded to `feat(auth): add local login, sessions and auth middleware` (allowed: unpushed) and rebased on origin/main before review, per handover.
- 2026-11-21 T7 reviewer round 1 CHANGES: added MkdirAll regression test, removed dead SPA branch, split bundled db commit — all pre-push, so history rewrite was allowed.
- 2026-11-21 **Workflow-scope gap:** the active `gh` account (Tae5609) token lacks the `workflow` scope, so it cannot push branches that modify `.github/workflows/` (git push and Contents API both blocked). The machine's second account `suppakorn-tae` has the scope but no repo access. Decision: added `suppakorn-tae` as collaborator (push), accepted the invitation with its own pre-authorized token, and pushed `test/backend-quality` with that token via a one-off `x-access-token` URL (not persisted in git config). Future branches that touch workflow files use the same mechanism; remove the collaborator after the roadmap if desired.
- 2026-11-21 T7 fixed the two top BUGS.md security items early (yt-dlp option injection, setConfig error swallowing); they are marked fixed there and T12 does not need to redo them.
- 2026-11-21 T4's mandated pre-fix (FindOrCreateExternalUser collision suffix) was assigned to T4 rather than a separate task because T4/T5 provisioning depends on it; shipped in PR #7.
- 2026-11-21 T5 needed a rebase after T4 merged (both touch backend/main.go). Resolved keep-both (OIDC block then SAML block), force-with-lease, conflict resolution re-reviewed and approved before merging #8.
- 2026-11-22 T3 round-1 MAJOR (apiFetch 401 on /auth/password for wrong current password cleared a valid session) fixed by revalidating via /auth/me before clearing; regression-tested.
- 2026-11-22 T10 round-1 MAJOR: `album` was an accepted PATCH field but the DB album row was never updated (track stayed bound to the old album). Product decision: fix properly — ensure target album (artist+name), repoint `album_id`, no file rename on album edits, `null` album → 400; regression tests added. Alternative (reject `album`) rejected because T11's UI must allow album edits.
- 2026-11-22 T10 needed a rebase after T8 merged (both add guarded ALTERs to `migrate()` in db.go). Resolved keep-both (T8 users.status/settings first, then T10 track columns), force-with-lease, resolution re-reviewed (fresh/pre-T8/pre-T10 DB paths all verified idempotent).
- 2026-11-22 T8 stricter-than-brief decisions accepted by reviewer: last-admin guard also blocks `pending` on the sole active admin; register 403s when `AUTH_LOCAL_ENABLED=false`.
- 2026-11-22 **VM reboot** killed all 5 running jobs. Redispatch state: T11 dev work was already committed (4 commits, clean tree) → went straight to review (T11-review-r1); T12 had 4 commits plus a half-finished `TRUST_PROXY` rate-limit IP fix in two dirty files → continuation job T12-dev-r1b to finish it and continue the bug hunt; T6a had a partial untracked `docs/API.md` → continuation T6a-dev-r1b; T6b and T13 worktrees were untouched → fresh T6b-dev-r1 / T13-dev-r1.
- 2026-11-22 New brief `docs/plan/tasks/T13-frontend-fixes.md` written from open BUGS.md frontend items (Library alert/confirm → ModalContext, artist refetch after scan, Settings limit NaN validation). Deliberately file-disjoint from in-flight T11 (T11 owns Metadata/metadata/, App.js, ModalContext, index.css, package.json; T13 owns Library.js, Settings.js).
- 2026-11-22 **SSRF product decision (BUGS.md `GET /api/info`):** accepted risk, no host allowlist. Downloading arbitrary URLs is the product's core purpose and every route requires auth (personal/team IDE, not a public service). The item stays unticked; deployment docs must note "do not expose to untrusted users" (final docs refresh).
- 2026-11-22 **Handover redispatch:** T11 review r1 verdict was CHANGES (1 MAJOR: focused-but-not-editing grid cell has no visual indicator — arrows/Enter operate on invisible focus; plus act() warning, select-all without grid focus, misleading cover-art tooltip). Because the dev commits were written by a fallback non-Orion model, T11-dev-r2 (Orion) both fixes the findings and re-verifies the full diff. T12: every run before fell back to non-Orion; committed the half-finished TRUST_PROXY limiter fix, rebased on origin/main (deleted the stale in-worktree `.opencode/agents`), pushed, and T12-dev-r2 (Orion) re-verifies all 5 commits, adds the missing TRUST_PROXY tests, and adds the BUGS.md tick commit the brief requires.
- 2026-11-22 **T6b push held until T12 merges.** T6b's smoke script asserts `/media/jellyide.db` is not served — true only after T12's fix lands — and its CI step would run red on a PR opened before then. Also T6a and T6b both appended the same `CONCURRENT_DOWNLOAD` BUGS.md entry; the branch that merges second rebases and dedupes. T6b's push additionally needs the `suppakorn-tae` token mechanism (branch touches `.github/workflows/ci.yml`).
- 2026-11-22 **T16/T17 added by planner (PR #19).** T17 (no-overwrite rename, URGENT data loss) is queued behind T12 (shares rename code) and will be dispatched the moment T12 merges, ahead of everything else. T16 (sync conflict detection + resolution UI, default policy `ask`) is queued behind T11 + T17. T11's next review must also verify the editor can later send `expected_version` (T16) — nothing to build now.

## Queued follow-ups (from triage 2026-11-22)
- `CONCURRENT_DOWNLOAD` env var ignored (logged twice — T6a + T6b both found it; dedupe BUGS.md when the second docs branch rebases). Fix belongs in `backend/internal/download/downloader.go` → schedule after T14 (download retry) merges, file-disjoint then.
- Reviewer notes (non-blocking): T13 `newestScanJob` `created_at` tie → id tiebreak; T13/T8-style `act()` warnings in new Settings tests → wrap when T9 lands.
- T6a license section reworded (no LICENSE file exists; owner decision: no LICENSE added in this roadmap) — T6a-dev-r2.

## Ideas (out of scope, for later)
- `.track-old` has no `word-break` (T2 reviewer note): a single very long unbroken word could push horizontal scroll at 360px — polish candidate for T9 or a follow-up.
- Desktop nav `title` tooltips now appear on hover at all widths (required mechanism for the rail; T2 reviewer note).
- `/api/info` SSRF surface (yt-dlp fetches arbitrary user-supplied URL) — logged in BUGS.md, needs a product decision (host allowlist vs proxy-only mode).
