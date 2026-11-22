# T13-follow-up: wrap Register test input dispatches in act

Source: `docs/plan/BUGS.md` line 34 (found in T11 re-verification):

> `frontend/src/pages/Register.test.js:84` — `setInput` dispatches a bare `input` event without `act`, so the resulting Register state updates log 8 "not wrapped in act" warnings per run, polluting CI output and masking new warnings — wrap the dispatches in `act` (or use `userEvent`/`fireEvent`) (found in T11 re-verification)

## Goal
Make `frontend/src/pages/Register.test.js` produce **zero** "not wrapped in act" warnings while keeping every test green. This is a test-only change — no `Register.js` behaviour change, no other test files.

## Scope (strict)
- `frontend/src/pages/Register.test.js` (and ONLY a test-helper import if needed).
- `docs/plan/BUGS.md`: tick this one item (`- [ ]` → `- [x] … — FIXED in <commit>`) in the same commit as the fix.
- Nothing else. No `Register.js` changes unless a test genuinely cannot be made warning-free without them (report it first via NOTES instead of changing app code).

## Method
- Inspect how sibling test files already avoid act warnings (e.g. `frontend/src/pages/Settings.test.js`, `Jobs.test.js`, `AuthContext.test.js` — at least one uses `act` or `fireEvent`; match the house style, don't introduce a new dependency — `react-testing-library` is NOT in this repo).
- `setInput` (around line 84) dispatches `new Event('input')`/`InputEvent` directly on the input element outside `act`. Wrap the state-changing dispatches in `act(...)` (import from `react`) or switch to `fireEvent.change` if that is the established pattern in the sibling tests.
- The 8 warnings: 4 password/confirm pairs × the two form states (or however they distribute) — after the fix, run the suite and count: the output for Register.test.js must contain no "not wrapped in act" lines.

## Gates (all must pass; paste in report)
- `cd frontend && CI=true npm test -- --watchAll=false` — all suites pass (base: 16 suites / 120 tests on 48534ff), AND grep the Register.test.js section of the output for "not wrapped in act" → zero.
- `cd frontend && CI=true npm run build` — compiles.
- go/npm-lint gates: N/A (no backend files, no lint script in frontend/package.json).

## Commits
- One commit, `test(ui): wrap Register test input dispatches in act` (Conventional, ≤72 chars, scope from CONVENTIONS.md). Do NOT push — the PO ships.
- Do not touch `docs/plan/logs/`.

## Report format (final message)
```
VERDICT: DONE
GATES: <each + result, incl. the act-warning grep count before/after>
COMMIT: <sha + subject>
NOTES: <anything the reviewer should know>
```
