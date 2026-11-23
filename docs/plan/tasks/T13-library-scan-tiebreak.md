# T13-follow-up: deterministic newestScanJob tiebreak (Library scan watch)

Source: T13 review, non-blocking note (queued in STATUS.md "Queued follow-ups"):

> T13 `newestScanJob` `created_at` tie → id tiebreak

## Bug
`frontend/src/pages/Library.js:56-58`:
```js
const newestScanJob = (scanJobs) =>
  scanJobs.reduce((a, b) =>
    new Date(a.created_at) >= new Date(b.created_at) ? a : b
  );
```
When two scan jobs share a `created_at` timestamp (same-second starts, identical RFC3339 strings), the winner depends on the ARRAY ORDER of the `/jobs` response — which is not guaranteed stable across polls. `scanSettled` uses this on its first poll to decide whether the watch should stop, so a tie can make the "scan settled" decision flip-flop between polls (missed or premature refetch of artists).

## Fix
Make the comparison a total order: compare `created_at`; on a tie (equal after `Date` parse), compare `id` (string comparison) so the result is independent of array order. Keep the one-line reduce shape (or a tiny named helper) — no restructure.

## Tests
`frontend/src/pages/Library.test.js` (exists from T13): add a regression test — two scan jobs with identical `created_at`, fed to the watch/settled logic (or the helper if it is extracted testably) in BOTH array orders → the same job wins in both, and the settled decision is stable across repeated calls. If the helper is not exported, test through the existing component-level watch flow that T13 already set up (match the house style of the file).

## Scope (strict)
- `frontend/src/pages/Library.js` (+ its test file). Nothing else. No BUGS.md change (this was a reviewer note, not a BUGS.md item — log it to the reviewer as closed, no file edit).
- Gates: `CI=true npm test -- --watchAll=false` (all suites pass, base now 16 suites / 121 tests on main d962281), `CI=true npm run build`.
- One commit: `fix(ui): make newest scan job selection stable on timestamp ties` (or closer to the code — Conventional, scope from CONVENTIONS.md, ≤72 chars). Do NOT push.

## Report format
```
VERDICT: DONE
GATES: <each + result, with suite/test counts>
COMMIT: <sha + subject>
NOTES: <anything the reviewer should know>
```
