# T17: URGENT, never overwrite files when renaming

Branch `fix/no-overwrite-rename`. **Priority: highest. This is a data-loss bug.** Read `docs/plan/CONVENTIONS.md`, `docs/plan/BUGS.md` (Priority section), and `backend/internal/{metadata,library}`.

Follow `docs/plan/CONVENTIONS.md` (commit, PR and quality-gate rules).

## Bug
Track title edits, legacy ApplyChanges and change-set revert all call `os.Rename(old, new)` without checking whether `new` exists. On Linux `rename(2)` replaces the target, so a duplicate title (or two titles that sanitize to the same filename) silently deletes another track's audio file.

## Do
1. Add a single helper, `fsutil.SafeRename(oldPath, newPath string) error`, in a new `backend/internal/fsutil` package, used by **every** rename of audio files and album directories (search for `os.Rename` across the backend and route all of them through it).
   - When `newPath` is the same file as `oldPath` (including case-only renames on case-insensitive filesystems; compare with `os.SameFile`), proceed.
   - When the target exists, return a typed `*ExistsError{Path}` and never touch either file.
   - Make it race-safe: use `os.Link(old,new)` followed by `os.Remove(old)` for files (`link` fails with EEXIST atomically). If hard links are unsupported (EXDEV/EPERM), fall back to `O_CREATE|O_EXCL` placeholder plus rename, and document the small window. For directories, `os.Mkdir(new)` exclusively first, then move the contents, or use `renameat2(RENAME_NOREPLACE)` via `golang.org/x/sys/unix` on Linux with a fallback.
2. Collision behaviour, until T16 adds the user-choice UI:
   - **Track title edit / revert:** auto-suffix ` (2)`, ` (3)`, … before the extension until the name is free. Record the actual final path in the DB, and add a note to the job's progress/error text, for example "renamed to 'Intro (2).flac' (name taken)".
   - **Album rename:** return 409 `{"error":"an album folder with that name already exists"}` and leave everything unchanged.
3. `PATCH /metadata/tracks` preview data: add `"rename_collision": true` and `"final_filename"` to the job result per track when a suffix was applied, so T16's UI can show it.
4. Tests, one per path:
   - a duplicate title in the same album keeps both files, with byte-identical contents afterwards
   - sanitize collisions (`A/B` vs `A_B`)
   - a case-only rename
   - a revert that collides
   - an album rename onto an existing empty or non-empty dir returns 409 with nothing moved
   - a concurrent `SafeRename` race (two goroutines, same target: exactly one wins and no file is lost)
5. Tick the two T17 items in BUGS.md.

## Verify
All gates from CONVENTIONS.md. Commits, for example `fix(metadata): never overwrite existing files on rename`, `fix(library): reject album rename onto existing folder`.
