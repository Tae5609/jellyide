# T16: Sync conflict detection and user-chosen resolution

Branch `feat/sync-conflicts`. Read `docs/plan/CONVENTIONS.md`, `docs/plan/BUGS.md`, `backend/internal/{scanner,library,metadata,db,api}`, `backend/update_metadata.py` (`--read`), and `frontend/src/pages/metadata/` (T11 editor).

Follow `docs/plan/CONVENTIONS.md` (commit, PR and quality-gate rules).

JellyIDE keeps three copies of the truth in sync: the DB row, the file's embedded tags, and the filename. Today every writer silently wins: rescans overwrite DB edits, concurrent users overwrite each other, and moved files leave ghost rows. This task detects conflicts and lets the **user choose**.

## Product rules
- Policy setting `sync_conflict_policy` in the `settings` table (env default `SYNC_CONFLICT_POLICY`), admin-editable:

  | Policy | Behaviour |
  |---|---|
  | `ask` (**default**) | record a conflict and wait for a user decision |
  | `file_wins` | today's scan behaviour, but still logged in history |
  | `db_wins` | re-write the file tags from the DB on scan |
- Nothing is ever silently lost. Every resolution goes through the normal edit path, so it is recorded in history and can be reverted.

## Backend
1. **Fingerprints:** add guarded columns `tracks.file_size`, `tracks.file_mtime`, `tracks.tags_hash` (sha256 of the normalized tag map), `tracks.version INTEGER NOT NULL DEFAULT 1`, `tracks.synced_at`, and `tracks.state TEXT NOT NULL DEFAULT 'ok'` (`ok | missing | conflict`). Update them on scan, on apply and on resolve. Bump `version` on every DB write to the row.
2. **Scan detection:** for an existing path whose size, mtime or tags hash changed since `synced_at`:
   - If no JellyIDE edit happened since the last sync (no `metadata_changes` after `synced_at`), the file wins as today. This is not a conflict.
   - Otherwise apply the policy. Under `ask`, insert one row per differing field into the new `sync_conflicts` table (`id, track_id, field, db_value, file_value, source('scan'|'apply'|'concurrent'), detected_at, status('open'|'resolved'|'dismissed'), resolved_by, resolved_at, resolution`), set `state='conflict'`, and leave both the DB and the file untouched.
3. **Apply-time check:** before writing tags, re-read the file (`--read`) and compare it with the DB snapshot. If the file changed externally in a field being edited, do not write that track. Record a `source='apply'` conflict and report it in the job result.
4. **Concurrent users (optimistic locking):** `PATCH /metadata/tracks` edits gain an optional `"expected_version"` (the T11 UI must send it). On a mismatch that track is skipped and the job result lists `{"track_id","reason":"version_conflict","current":{fields},"current_version"}`. Return 409 instead of 202 if *all* tracks conflict.
5. **Missing and moved files:**
   - Rows whose file no longer exists become `state='missing'`, and are kept rather than deleted.
   - A new file with a matching fingerprint (same size and duration ±1s and the same tags hash) is offered as a relink candidate.
6. **API** (authenticated; resolving is allowed for any user who can edit metadata):

   | Method | Path | Body | Response / notes |
   |---|---|---|---|
   | GET | `/api/sync/conflicts?status=open&album_id=` | – | grouped by track: `[{"track_id","track_title","album_id","fields":[{"conflict_id","field","db_value","file_value","source","detected_at"}]}]` |
   | GET | `/api/sync/summary` | – | `{"open_conflicts","missing_tracks"}`, for the sidebar badge |
   | POST | `/api/sync/conflicts/resolve` | `{"resolutions":[{"conflict_id","choice":"db"\|"file"\|"custom","value"?}]}` | applies through the normal edit job, so it is recorded in history: `db` rewrites the file tags, `file` updates the DB, `custom` writes both. 202 `{"job_id"}` |
   | POST | `/api/sync/conflicts/resolve-all` | `{"album_id"?,"choice":"db"\|"file"}` | |
   | POST | `/api/sync/conflicts/{id}/dismiss` | – | |
   | GET | `/api/sync/missing` | – | the missing tracks, each with its relink candidates |
   | POST | `/api/sync/missing/{track_id}/relink` | `{"path"}` | keeps the id and history, re-fingerprints |
   | DELETE | `/api/sync/missing/{track_id}` | – | removes the row; history rows go via the FK fix |
   | GET / PUT | `/api/admin/settings/sync` | `{"policy"}` | |
7. **Rename collisions** (builds on T17's `SafeRename`): the preview endpoint and the job result report collisions, and the PATCH body accepts a per-track `"on_collision":"suffix"|"skip"|"replace"`. `suffix` is the default, as in T17. `replace` moves the existing file to `<DOWNLOAD_DIR>/.jellyide/trash/<timestamp>/` first (never delete), and records it so it can be restored. Add `GET /api/sync/trash` and `POST /api/sync/trash/{id}/restore`, plus a purge of entries older than 30 days on startup.
8. **Tests:** each detection path (scan with and without prior edits, under each policy), apply-time external change, version conflict (two clients), missing-file marking and relink keeping history, resolve with db / file / custom (verifying both file and DB end states with the fake tag writer), the collision modes, and trash and restore.

## Frontend
1. A **Conflicts** page (`/sync`) with a sidebar badge from `/api/sync/summary`, visible only when the count is above 0:
   - Per track, a table: field | JellyIDE value | file value | your choice. Each row has radio buttons (JellyIDE / File / Custom), and Custom shows an input. Per-track "use all file" / "use all JellyIDE". A global "resolve all on this album" with a confirm dialog. Dismiss.
   - A source label for each conflict ("changed on disk by another program", "changed by another user", "changed during apply").
   - A **Missing files** tab: each missing track with relink candidates (pick one) or delete row (with confirm).
   - A **Trash** tab: restore replaced files.
2. **Metadata editor (T11):**
   - Send `expected_version`.
   - When a version conflict comes back, show a dialog: "Someone else changed N tracks", with a per-field diff of theirs vs yours, and the options keep theirs / overwrite with mine / merge per field. The chosen result is re-applied with the new version.
   - Tracks in `state=conflict` show a warning chip linking to `/sync`.
   - The review step shows rename collisions with a per-track select (add suffix / skip rename / replace (moves the old file to trash)).
3. Settings (admin): a policy select with an explanation of each option.
4. Works at 360px. Use `ModalContext`. RTL tests for the resolution table choices, the version-conflict dialog, and the collision select.

## Verify
All gates from CONVENTIONS.md. Commits split by area, for example `feat(scanner): detect external tag changes and missing files`, `feat(metadata): add optimistic locking for concurrent edits`, `feat(api): add sync conflict resolution endpoints`, `feat(ui): add conflicts page and resolution flow`.
