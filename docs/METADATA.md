# Using the metadata IDE

The metadata IDE is the album editing view of JellyIDE: a spreadsheet-style grid over the
tracks of one album, with bulk edit, find/replace, cover art, MusicBrainz lookup, and a
full change history with revert. It lives at `/library/<artistId>/<albumId>` in the SPA.

> Status note: the editor described here is being landed by task T11
> (`docs/plan/tasks/T11-metadata-frontend.md`); the shortcuts and panel behaviour below are
> the ones that brief fixes. The server-side engine — endpoints, validation, jobs, history —
> is already merged (`backend/internal/metadata/`) and is described from that code. The API
> contract is in [API.md](API.md); the internals in [ARCHITECTURE.md](ARCHITECTURE.md).

## Table of contents

- [The track grid](#the-track-grid)
- [Editing cells](#editing-cells)
- [Editable fields and validation](#editable-fields-and-validation)
- [Selection, fill down and auto-number](#selection-fill-down-and-auto-number)
- [Bulk edit](#bulk-edit)
- [Find and replace](#find-and-replace)
- [Cover art](#cover-art)
- [MusicBrainz lookup](#musicbrainz-lookup)
- [History and revert](#history-and-revert)
- [File renaming rules](#file-renaming-rules)
- [Keyboard shortcuts](#keyboard-shortcuts)
- [On a small screen](#on-a-small-screen)

## The track grid

One row per track, one column per field:

| Column | Contents | Editable |
|---|---|---|
| `#` | track number | yes |
| `disc` | disc number | yes |
| `title` | track title | yes |
| `artist` | artist | yes |
| `album artist` | album artist | yes |
| `year` | year | yes |
| `genre` | genre | yes |
| `composer` | composer | yes |
| `comment` | comment | yes |
| `duration` | seconds (from the ffprobe scan) | read-only |

Columns can be toggled; the visible set is remembered in `localStorage`. The row order is the
grid order — auto-number walks it.

## Editing cells

Click a cell (or press `Enter` on the focused cell) to edit it:

- `Enter` commits the value and the cell becomes **dirty** (highlighted) — nothing is
  written yet.
- `Esc` cancels the edit and restores the pending value.
- `Tab` / `Shift+Tab` move to the next / previous cell.

Pending edits are kept client-side (keyed by `trackId.field`) with an undo/redo stack. The
**Review & apply** step (`Ctrl+S`) shows a diff table, then sends one
`PATCH /api/metadata/tracks` request with all edits. The server validates the whole batch
first — one bad field and nothing is applied (`400` names the `field` and `track_id`) —
then queues a job; the UI polls `/api/jobs` and refreshes when it completes. Each applied
batch shares one `change_set_id`, which is what powers the history view and revert.

## Editable fields and validation

A bulk edit (`PATCH /api/metadata/tracks`) may set any subset of the fields per track. A
field set to `null` is cleared (stored as SQL `NULL`); only keys that are present are
touched. The validator (`backend/internal/metadata/errors.go`, `apply.go`) rejects:

| Field | Type | Rules |
|---|---|---|
| `title` | text | must not be empty or blank |
| `album` | text | re-targets the track to that album row (created under the track's artist when missing); the file is not moved; `null` is refused (`album cannot be cleared; set a new album name instead`) |
| `artist`, `album_artist`, `genre`, `composer`, `comment` | text | any string |
| `track_number`, `disc_number` | integer | 0…999 |
| `year` | integer | 0…9999 |

Other `400` cases: empty `edits`, missing `track_id`, empty `fields`, unknown field, wrong
type (`field must be a string or null` / `field must be a non-negative integer or null`),
and the same field twice in one entry (`duplicate field in one request`). A `404` reports a
track that does not exist. The text written into the file itself goes through the Python tag
writer (mutagen), so tags are kept in sync with the database — or the track is skipped
wholly and left untouched when the tag write or the rename fails (the database never
diverges from the file).

## Selection, fill down and auto-number

- Row checkboxes; `Shift`+click selects a range; `Ctrl+A` (`⌘+A`) selects all rows (when
  the grid has the focus).
- **Fill down** (`Ctrl+D`) copies the focused cell's value into every selected row as
  pending edits.
- **Auto-number** sets `track_number` to 1…n following the current row order.

## Bulk edit

The bulk-edit panel picks one field and one value and applies it to the selected rows —
same pending-edit path as typing cells, so undo and the review diff cover it too.

## Find and replace

The find/replace panel runs one operation over the selected tracks:

| Operation | Effect |
|---|---|
| `replace` | literal: every occurrence of the target is replaced with the value |
| `prefix` | value is put in front of the field |
| `suffix` | value is appended |
| `regex` | regular expression (Go regexp syntax); an **invalid regular expression does not change the value** — preview it first |

The field selector chooses which column the operation edits: `title` (default), `artist`,
`album_artist`, `genre`, `composer` or `comment` (the backend's preview set — see
`previewFields` in `service.go`). New values are trimmed of surrounding whitespace. The
preview (`POST /api/metadata/preview`) is a dry run — nothing is written until you apply;
the response keys are historically called `old_title`/`new_title` even for non-title fields.
Unknown track ids are silently skipped by the preview. The usual workflow is
`/` (focus the panel), enter target and value, preview, apply, review the diff, save.

## Cover art

The cover panel shows the current album cover (`GET /api/library/albums/{id}/cover`, with a
placeholder when there is none) and offers three ways to set it:

- **Upload** — file input or drag-and-drop. Client-side checks first; the server enforces:
  jpeg or png only (content sniffing), at most 10 MiB, at most 4000×4000 px. It is stored
  as `cover.jpg` / `cover.png` in the album directory and served under `/media/…`; a
  background job (`embed_cover`) then embeds the image into every track of the album —
  poll the job list for `completed`.
- **Extract from file** — takes the embedded cover from the first track that carries one
  (synchronous). When no track has art: `404 no track has embedded cover art`.
- **From URL / from lookup** — only `https`, only the hosts `coverartarchive.org`,
  `archive.org` and `*.archive.org` (an allowlist, re-checked on every redirect, at most
  3 redirects); the same size and type limits apply.

## MusicBrainz lookup

Press `L` (or the lookup button). Flow:

1. **Search** — the query defaults to *artist + album*, but stays editable. Results come
   from the public MusicBrainz web service (at most 10 hits; the server caches responses for
   10 minutes and shares one 1-requests-per-second rate limiter, so repeat lookups are
   cheap). An empty query with no album falls back to the album's artist and title; when
   both are missing you get `400 q or album_id is required`.
2. **Pick a release** — the mapping table shows, per local track, the current value and the
   proposed value (title, artist, album artist, year, genre, numbers) with a **confidence**
   score: exact disc/track-number match scores 1; otherwise it is the normalised-title
   similarity (Levenshtein ratio). Rows under **0.6** are highlighted as low-confidence —
   look twice before you include them.
3. **Select** — include checkboxes per row and per field; "Apply" turns the selection into
   pending edits (still nothing written — review with the diff), and optionally sets the
   cover from the release's cover URL.

Applying goes out as ordinary `PATCH /api/metadata/tracks` edits plus, for the cover,
`POST …/cover/from-url` — there is no separate apply endpoint.

## History and revert

Press `H` for the history drawer: the album's change sets, newest first, each expandable to
the recorded field changes (`old_value` → `new_value` per track and field; see
`GET /api/metadata/history`). **Revert** asks for confirmation, then applies the recorded
`old_value`s as a **new** change set — the revert is itself revertible (a revert of a
revert is a normal change set again). It runs as a job and refreshes the grid. A change set
whose track has been deleted since cannot be reverted: `409 track no longer exists`.
File renames are reverted too, because every change row recorded keeps the old path.

## File renaming rules

Applying edits keeps files in the existing scheme — a title-based name plus the original
extension, in the album directory `<DOWNLOAD_DIR>/<artist>/<album>/`:

- The new file name is the sanitized title + the old extension.
- The sanitizer maps the characters that are illegal in file names, `/ \ : * ? " < > |`,
  each to `-` (`sanitizeFilename` in `backend/internal/metadata/service.go`).
- A title edit renames the file on disk and updates the stored `path`; album moves (the
  `album` field) repoint the database row only — the file stays where it is until a scan.
- Renaming an album (the header action of the album page, `POST …/albums/{id}/rename`)
  additionally renames the directory and rewrites the `album` tag of every file.

## Keyboard shortcuts

Desktop only; single-key shortcuts are ignored while typing into an input field, and they
are disabled while a modal is open.

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

`Ctrl+S` is captured (`preventDefault`) so the browser save dialog does not pop up. The help
overlay lists them all; a "Keyboard shortcuts" button opens it.

## On a small screen

Below 768 px the grid becomes a card list; tapping a card opens a bottom-sheet form with all
fields, and bulk edit, find/replace, cover, lookup and history move behind a sticky action
bar. The keyboard shortcuts are not shown — there is no keyboard.

## See also

- [API reference — metadata endpoints](API.md#5-metadata)
- [Architecture — job system and metadata service](ARCHITECTURE.md#job-system)
- [Authentication](AUTHENTICATION.md) — who may edit
- [Development](DEVELOPMENT.md) — build and test the editor
