# Bug log

The product owner triages this list. Agents append bugs they find out of scope here, and T12 (backend) or follow-up tasks fix them.

## Known (found during planning)
- [ ] `backend/internal/api/router.go` getInfo: the user `url` is passed to `yt-dlp` as a positional arg with no `--` separator, so a value starting with `-` is parsed as a yt-dlp option (option injection, potentially `--exec`). Security.
- [ ] `backend/internal/api/router.go` setConfig: JSON decode errors are ignored, so a bad body silently sets limit 0 and proxy "".
- [ ] `backend/internal/library/service.go` RenameAlbum / `sanitizeFilename`: verify that a new title cannot escape the music dir (`..`, `/`, absolute paths) and that renaming onto an existing directory is handled.
- [ ] `backend/main.go`: `/media/` serves the whole music dir including `jellyide.db` (the database, which will hold password hashes and sessions). Must be blocked.
- [ ] `backend/main.go`: CORS `AllowedOrigins: *` (removed by T1; verify).
- [ ] `backend/main.go` SPA handler: `filepath.Join("./frontend/build", path)`. Confirm there is no traversal, and serve 404 for missing assets with a file extension instead of index.html.
- [ ] `backend/update_metadata.py`: bare `except:`, and only title/album are supported (extended in T10).
