# T13: Frontend bug fixes (Library, Settings)

Branch `fix/frontend-fixes`. Read `docs/plan/CONVENTIONS.md`, `docs/plan/BUGS.md`, `frontend/src/pages/Library.js`, `frontend/src/pages/Settings.js`, `frontend/src/components/ModalContext.js`, and one existing test for style (`frontend/src/api.test.js`, `frontend/src/pages/Login.test.js`).

## Do
Fix the open frontend items from `docs/plan/BUGS.md` that live in files this task owns. For each fix: a regression test that fails before the fix, then tick the item (`- [x] … — fixed in <commit>`).

1. `frontend/src/pages/Library.js:46` — scan and error handling use blocking `alert()`/`window.confirm()`. Replace with the existing `ModalContext` flow used by Downloads/Jobs/Settings (error modals, confirm dialog with busy state).
2. `frontend/src/pages/Library.js:13` — `fetchArtists` runs once on mount with empty deps and is never re-fetched after a scan completes; newly scanned artists do not appear until reload. Re-fetch artists when a scan job completes (and on error paths that change the library).
3. `frontend/src/pages/Settings.js:27` — `parseInt(limit)` on empty/invalid input yields `NaN` → serialised as `null` → backend `SetLimit(0)` silently stops all concurrent downloads while the UI shows "Settings Saved". Validate the number client-side (positive integer); on invalid input show an inline error and keep the previous value; never send `NaN`/`null`.

Tests (existing plain-jest style, no new dependencies):
- Library: scan failure shows the modal (not `alert`), artist list refreshes after a completed scan (mocked fetch + jobs poll), delete-artist confirm uses the modal.
- Settings: empty/invalid limit input → inline error, no save request with `null`; valid limit saves.

## Do NOT
- Touch `frontend/package.json`, `package-lock.json`, `src/index.css`, `src/App.js`, `src/pages/metadata/` (T11 is in flight on those). Reuse existing CSS classes and `ModalContext` API as-is; if a needed class is missing, use an inline style on your own elements.
- Add dependencies.
- Fix the `Metadata.js` items from BUGS.md (lines 70 and the alert/confirm usages): the T11 rewrite owns that page.

## Verify
All gates from CONVENTIONS.md that exist, plus `CI=true npm test -- --watchAll=false` and `CI=true npm run build`. Commits, one per bug, e.g. `fix(ui): replace blocking alerts with modal flow in Library`, `fix(ui): refetch artists after scan completes`, `fix(ui): validate concurrent download limit input`.
