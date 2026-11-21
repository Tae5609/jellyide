# T11: Metadata editor UI and keyboard shortcuts

Branch `feat/metadata-editor-ui`. Read `docs/plan/CONVENTIONS.md`, `docs/plan/tasks/T10-metadata-backend.md` (the API contract, already merged), `frontend/src/pages/Metadata.js`, `src/api.js`, `src/components/ModalContext.js`, and `src/index.css`.

## Do
1. **Refactor `Metadata.js`** into `src/pages/metadata/`:

   | File | Role |
   |---|---|
   | `MetadataPage.js` | container |
   | `TrackGrid.js` | editable grid |
   | `BulkEditPanel.js` | bulk edits |
   | `FindReplacePanel.js` | the existing ops plus a field selector |
   | `CoverArtPanel.js` | cover art |
   | `LookupDialog.js` | MusicBrainz lookup |
   | `HistoryDrawer.js` | history and revert |
   | `useEditorState.js` | reducer |
   | `useShortcuts.js` | shortcut hook |

   Keep the existing route `/library/:artistId/:albumId` and all existing features (analyzer suggestions, rename album, delete album/track).
2. **Editor state** (`useReducer`): the original tracks, pending edits keyed by `trackId.field`, row selection, and a client-side undo/redo stack of edit actions. Dirty cells are highlighted. A pending-changes counter and a "Review & apply" step show a diff table, then call `PATCH /metadata/tracks` and poll the job until done, then refresh.
3. **TrackGrid**:
   - Columns: #, disc, title, artist, album artist, year, genre, composer, comment, and duration (read-only).
   - Columns can be toggled, and the visible set is remembered in localStorage.
   - Inline editing: click or Enter to edit, Esc to cancel, Enter to commit, Tab/Shift+Tab to the next or previous cell.
   - Row checkboxes, shift-click range select, and select-all.
   - "Fill down": copy the focused cell's value to the selected rows.
   - "Auto-number": set track numbers 1..n in the current order.
4. **BulkEditPanel**: pick a field and a value, then apply it to the selected rows as pending edits.
5. **CoverArtPanel**: show the current cover (`GET …/cover`, with a fallback placeholder). Offer upload (file input plus drag-and-drop, client-side type/size check), extract from file, and use-from-lookup.
6. **LookupDialog**: search, defaulting to the artist and album, with an editable query. It lists the candidates; picking one shows the mapping table (current vs proposed, with low confidence highlighted under 0.6). Per-row and per-field include checkboxes. "Apply" turns the selection into pending edits (they are not written yet), and optionally sets the cover from the url.
7. **HistoryDrawer**: lists the album's change sets with expandable details and a "Revert" button that asks for confirmation, then runs the job and refreshes.
8. **Keyboard shortcuts** (`useShortcuts`, desktop only, disabled while a modal is open):

   | Keys | Action |
   |---|---|
   | `Ctrl/⌘+S` | Review & apply |
   | `Ctrl/⌘+Z` / `Ctrl/⌘+Shift+Z` (or `Ctrl+Y`) | undo / redo pending edits |
   | arrows | move the cell focus |
   | `Enter` | edit |
   | `Esc` | cancel or close |
   | `Ctrl/⌘+A` | select all rows (when the grid has focus) |
   | `Ctrl/⌘+D` | fill down |
   | `/` | focus find & replace |
   | `L` | open lookup |
   | `H` | open history |
   | `?` | open the shortcuts help overlay |
   | `g` then `d` / `l` / `j` / `s` | global navigation to downloads / library / jobs / settings, in `App.js` |

   Single-key shortcuts are ignored while typing in inputs. Save/undo still work there. `preventDefault` on the browser's Ctrl+S. The help overlay lists them all, and a "Keyboard shortcuts" button opens it.
9. **Mobile (≤768px)**:
   - The grid becomes a track card list. Tapping a card opens a bottom-sheet form with all fields.
   - Bulk edit, find/replace, cover, lookup and history move behind a sticky bottom action bar.
   - No horizontal scroll at 360px.
   - Shortcuts are not shown.
10. **Tests** (`@testing-library/react` + jest via react-scripts; add the dev deps `@testing-library/react`, `@testing-library/jest-dom`, `@testing-library/user-event` if T9 has not yet):
    - reducer: edit, undo, redo, fill down, auto-number, and the diff.
    - `useShortcuts`: key handling, and the ignore-in-inputs rule.
    - LookupDialog: selection → pending edits, with mocked fetch.
    - TrackGrid inline edit flow.

## Do NOT
- Change backend code. If the API does not match T10, log it in BUGS.md and report it.
- Add UI libraries. Small utilities are OK only if justified in the PR.

## Verify
All gates from CONVENTIONS.md that exist, plus `CI=true npm test -- --watchAll=false`. Commits, for example `refactor(metadata): split editor into components`, `feat(metadata): add editable track grid with undo/redo`, `feat(metadata): add keyboard shortcuts and help overlay`.
