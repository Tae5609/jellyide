# T10: Flexible metadata editing (backend)

Branch `feat/metadata-fields`. Read `docs/plan/CONVENTIONS.md`, `backend/internal/{metadata,library,scanner,api,jobs,db}`, and `backend/update_metadata.py`.

Today only title/album rename is supported, plus title find-and-replace ops. This task adds full tag editing, cover art, history with undo, and MusicBrainz lookup. T11 builds the UI against the API contract below, so follow it exactly.

## 1. Data model
- Add guarded `ALTER TABLE tracks ADD COLUMN` for: `artist TEXT`, `album_artist TEXT`, `track_number INTEGER`, `disc_number INTEGER`, `year INTEGER`, `genre TEXT`, `composer TEXT`, `comment TEXT`.
- Add guarded `ALTER TABLE metadata_changes ADD COLUMN change_set_id TEXT` and an index on it.
- Scanner: populate the new columns from ffprobe `format.tags`, matching keys case-insensitively: `artist`, `album_artist`/`albumartist`, `track` (parse "3/12"), `disc`, `date`/`year` (take the first 4 digits), `genre`, `composer`, `comment`.

## 2. Tag writer (`update_metadata.py`)
Keep the old flags working. Add:
- `--fields-json '<json>'` with keys `title, album, artist, album_artist, track_number, disc_number, year, genre, composer, comment`. A `null` value deletes the tag. This covers FLAC/Vorbis, MP3 (ID3 via EasyID3, registering `albumartist`/`composer` keys as needed), and M4A (atoms `©ART`, `aART`, `trkn`, `disk`, `©day`, `©gen`, `©wrt`, `©cmt`).
- `--cover <path>` embeds JPEG/PNG: FLAC `Picture`, ID3 `APIC`, MP4 `covr`, Ogg `METADATA_BLOCK_PICTURE`.
- `--extract-cover <out>` writes the embedded cover to a file, exiting with code 2 if there is none.
- `--read` prints the current tags as JSON.
- Add `backend/tests/test_update_metadata.py` (pytest). Generate 1-second files with `ffmpeg -f lavfi -i anullsrc` per format, skipping if ffmpeg is missing. Round-trip every field and the cover.
- The Go side always calls `exec.Command("python3", script, "--", ...)`-style argument lists. Never use a shell.

## 3. API contract (all under `/api`, protected, JSON)

**Tracks and bulk edits**

| Method | Path | Request | Response |
|---|---|---|---|
| GET | `/library/albums/{id}/tracks` | – | now includes every new field |
| GET | `/library/tracks/{id}` | – | single track with all fields |
| PATCH | `/metadata/tracks` | `{"edits":[{"track_id":"…","fields":{"genre":"Rock","year":2021,"comment":null}}]}` | 202 `{"job_id","change_set_id"}` |

PATCH notes: per-track fields, so bulk edits send many entries. Only keys present are changed, and `null` clears. Validate types and ranges (year 0–9999, track/disc 0–999), returning 400 with `{"error","field","track_id"}`. Run it as a `metadata_apply` job that writes the tags, updates the DB, renames the file if the title or track number changed (keep the existing naming scheme), and records `metadata_changes` rows with the same `change_set_id`.

**Find-and-replace preview**

| Method | Path | Request | Response |
|---|---|---|---|
| POST | `/metadata/preview` | the existing request gains an optional `"field"` (default `"title"`; allowed: title, artist, album_artist, genre, composer, comment) | unchanged |

The existing find/replace/prefix/suffix/regex ops then work on any text field.

**Cover art**

| Method | Path | Request | Response |
|---|---|---|---|
| GET | `/library/albums/{id}/cover` | – | the image; `404` if none |
| POST | `/library/albums/{id}/cover` | multipart field `file`: max 10 MB, content sniffed with `http.DetectContentType`, jpeg/png only, decoded with `image.DecodeConfig`, max 4000×4000 | – |
| POST | `/library/albums/{id}/cover/extract` | – | – |
| POST | `/library/albums/{id}/cover/from-url` | `{"url"}` | – |

- Upload: saves `cover.jpg|png` in the album dir, embeds it into every track via a job, and sets `albums.cover_art`.
- Extract: takes the embedded art from the first track that has it and saves it as the album cover.
- From URL: **allowlist** hosts `coverartarchive.org`, `archive.org` and `*.archive.org`, https only. Follow at most 3 redirects, re-checking the allowlist on each. Same size and type limits.

**History and undo**

| Method | Path | Response |
|---|---|---|
| GET | `/metadata/history?album_id=&limit=50` | `[{"change_set_id","created_at","track_count","changes":[{"track_id","track_title","field","old_value","new_value"}]}]`, newest first |
| POST | `/metadata/history/{change_set_id}/revert` | 202 `{"job_id","change_set_id"}` |

Revert applies the old values as a **new** change set, so it is itself revertible, including file renames. It returns 409 if any affected track no longer exists.

**MusicBrainz lookup** (package `backend/internal/musicbrainz`)

| Method | Path | Response |
|---|---|---|
| GET | `/metadata/lookup/search?album_id=&q=` | `[{"mbid","title","artist","date","country","track_count","score"}]`, max 10 |
| GET | `/metadata/lookup/release/{mbid}?album_id=` | `{"release":{…},"cover_url":"https://coverartarchive.org/release/<mbid>/front","mapping":[{"track_id","current":{fields},"proposed":{fields},"confidence":0..1}]}` |

- Search defaults the query to the album artist and album title.
- The release mapping matches local tracks to release tracks by disc/track number first, then by normalized title similarity (Levenshtein ratio).
- The UI applies the result through `PATCH /metadata/tracks` and `cover/from-url`. There is no separate apply endpoint.
- Client rules: User-Agent `JellyIDE/1.0 ( https://github.com/Tae5609/jellyide )`, a global rate limit of 1 request/second (token bucket shared by all requests), a 10-second timeout, and responses cached in memory for 10 minutes.

## 4. Tests
- Go: field validation, PATCH → job → DB update (stub the tag writer behind an interface so tests do not need python), change-set recording and revert (including revert of a revert), cover upload validation (wrong type, oversize, decompression-bomb dimensions), from-url allowlist (rejects http, other hosts, and redirects to other hosts; use `httptest` and an injectable client), MusicBrainz client with an `httptest` server (rate limiter, cache, mapping algorithm), and scanner tag parsing.
- Python: the pytest suite above.

## Do NOT
- Touch frontend code (that is T11).
- Remove existing endpoints.

## Verify
All gates from CONVENTIONS.md, plus `cd backend && python3 -m pytest tests -q`. Commits split by area, for example `feat(metadata): add per-track tag fields and bulk patch api`, `feat(metadata): add cover art management`, `feat(metadata): add change history and revert`, `feat(metadata): add musicbrainz lookup`.
