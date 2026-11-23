// Command yt-scraper is the JellyIDE server: it serves the API, the media
// files and the SPA frontend.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"yt-scraper/internal/api"
	"yt-scraper/internal/auth"
	"yt-scraper/internal/db"
	"yt-scraper/internal/download"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
	"yt-scraper/internal/metadata"
	"yt-scraper/internal/scanner"
	"yt-scraper/internal/ytdlp"

	"github.com/gorilla/mux"
)

// spaBuildDir is where the frontend build output is expected.
const spaBuildDir = "./frontend/build"

// allowedMediaExt lists the only file types the media route will serve.
var allowedMediaExt = map[string]bool{
	"flac": true, "mp3": true, "m4a": true, "aac": true,
	"ogg": true, "opus": true, "wav": true,
	"jpg": true, "jpeg": true, "png": true, "gif": true, "webp": true,
}

// contentSecurityPolicy matches the assets the SPA loads: self-hosted
// bundles, Google Fonts and cover art from the MusicBrainz/Cover Art
// Archive CDNs.
const contentSecurityPolicy = "default-src 'self'; " +
	"img-src 'self' data: https://coverartarchive.org https://*.archive.org; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src https://fonts.gstatic.com; " +
	"object-src 'none'; base-uri 'self'; frame-ancestors 'none'"

func main() {
	// The admin recovery CLI: `server admin <command>` operates on the
	// database directly; without arguments the server runs, as before.
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		os.Exit(runAdmin(os.Args[2:], os.Stdin, os.Stdout))
	}

	// Initialize config
	downloadDir := getEnv("DOWNLOAD_DIR", "/mnt/media/music")
	dbPath := filepath.Join(downloadDir, "jellyide.db")
	if err := os.MkdirAll(downloadDir, 0o755); err != nil { //nolint:gosec // G301 - shared media library dir, world-readable on purpose
		log.Fatal("Failed to create download dir: ", err)
	}

	// Initialize DB
	database, err := db.NewDB(dbPath)
	if err != nil {
		log.Fatal("Failed to initialize database: ", err)
	}

	// Initialize auth (bootstraps the admin account when needed). The
	// database is closed explicitly before any log.Fatal below, because
	// deferred calls do not run when the process exits that way.
	authSvc, err := auth.NewService(auth.LoadConfig(), database)
	if err != nil {
		if cerr := database.Close(); cerr != nil {
			log.Printf("Failed to close database: %v", cerr)
		}
		log.Fatal("Failed to initialize auth: ", err)
	}

	// Enable OIDC before the routes are registered (they depend on it).
	if oc := auth.LoadOIDCConfig(); oc.Enabled {
		if err := authSvc.EnableOIDC(context.Background(), oc); err != nil {
			if cerr := database.Close(); cerr != nil {
				log.Printf("Failed to close database: %v", cerr)
			}
			log.Fatal("Failed to enable OIDC: ", err)
		}
	}

	// SAML 2.0 service provider (optional). Built before route registration
	// so its endpoints can be mounted below; a misconfigured SP is a fatal
	// error rather than a half-armed login page.
	samlCfg := auth.LoadSAMLConfig(downloadDir)
	if samlCfg.Enabled {
		if err := authSvc.EnableSAML(context.Background(), samlCfg); err != nil {
			if cerr := database.Close(); cerr != nil {
				log.Printf("Failed to close database: %v", cerr)
			}
			log.Fatal("Failed to enable SAML: ", err)
		}
	}

	// Initialize Services
	libService := library.NewService(database, downloadDir)
	jobManager := jobs.NewManager(database)
	musicScanner := scanner.NewScanner(libService, jobManager, downloadDir)
	downloader := download.NewDownloader(jobManager, musicScanner, downloadDir)

	// Managed yt-dlp binary (T15). The downloader resolves the binary
	// path through the manager on every job, so an update takes effect
	// without a restart. YTDLP_MANAGED=false keeps the system yt-dlp.
	ytdlpMgr := ytdlp.NewManager(database, jobManager, filepath.Join(downloadDir, ".jellyide", "bin"),
		ytdlp.ParseManagedEnv(os.Getenv("YTDLP_MANAGED")))
	downloader.SetYtDlpResolver(ytdlpMgr)
	ytdlpMgr.SetGate(downloader)
	metaService := metadata.NewServiceWith(metadata.Deps{
		DB:      database,
		Library: libService,
		Jobs:    jobManager,
	})

	// Main Router
	r := mux.NewRouter()

	// API Router (Using Subrouter natively)
	apiSub := r.PathPrefix("/api").Subrouter()
	authSvc.RegisterRoutes(apiSub)
	if samlCfg.Enabled {
		authSvc.RegisterSAMLRoutes(apiSub)
	}
	api.RegisterRoutes(apiSub, downloader, libService, metaService, jobManager, musicScanner, ytdlpMgr, authSvc.RequireAdmin)

	// Serve media files
	r.PathPrefix("/media/").Handler(http.StripPrefix("/media", newMediaHandler(downloadDir)))

	// SPA Handler for Frontend
	r.PathPrefix("/").Handler(newSPAHandler(spaBuildDir))

	// yt-dlp updater loop: installs the managed binary on first boot and
	// checks for new releases on the configured interval. A failure never
	// breaks downloads (the previous binary stays in place).
	updaterCtx, stopUpdater := context.WithCancel(context.Background())
	go ytdlpMgr.Loop(updaterCtx)

	// Wrap the whole router with the auth middleware. The SPA is
	// same-origin, so no CORS handling is needed.
	handler := securityHeaders(authSvc.Middleware(r))

	// In first-run setup mode (no users, no ADMIN_PASSWORD) the gate
	// 503s the API and media except /api/setup/* and /api/auth/providers.
	// It opens without a restart once the first user exists.
	handler = authSvc.SetupGate(handler)

	port := getEnv("PORT", "8080")
	log.Printf("JellyIDE Server starting on :%s", port)
	log.Printf("DB Path: %s", dbPath)
	log.Printf("Media Dir: %s", downloadDir)

	srv := newHTTPServer(":"+port, handler)

	// Shut down cleanly on SIGINT/SIGTERM so in-flight requests finish and
	// the database is closed once.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	select {
	case err = <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Server error: %v", err)
		}
	case <-ctx.Done():
		log.Println("Shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if serr := srv.Shutdown(shutdownCtx); serr != nil {
			log.Printf("Graceful shutdown failed: %v", serr)
		}
		cancel()
		err = <-serveErr
	}
	stop()

	if cerr := database.Close(); cerr != nil {
		log.Printf("Failed to close database: %v", cerr)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		stopUpdater()
		log.Fatal("Failed to start server: ", err)
	}
}

// newHTTPServer builds the server with conservative timeouts. The write
// timeout is left at zero so long media streams are not cut off.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}
}

// securityHeaders adds the browser hardening headers to every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

// newMediaHandler serves only known audio and image files from musicDir.
// Dotfiles, the SQLite database, the SAML metadata directory and any path
// that would escape musicDir are hidden behind a 404.
func newMediaHandler(musicDir string) http.Handler {
	fileServer := http.FileServer(http.Dir(musicDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/")
		if rel == "" || strings.ContainsRune(rel, 0) {
			http.NotFound(w, r)
			return
		}

		clean := filepath.Clean(rel)
		if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			http.NotFound(w, r)
			return
		}
		slashPath := filepath.ToSlash(clean)
		for _, segment := range strings.Split(slashPath, "/") {
			if strings.HasPrefix(segment, ".") {
				http.NotFound(w, r)
				return
			}
		}
		if strings.HasPrefix(filepath.Base(clean), "jellyide.db") {
			http.NotFound(w, r)
			return
		}
		if slashPath == "saml" || strings.HasPrefix(slashPath, "saml/") {
			http.NotFound(w, r)
			return
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(clean), "."))
		if !allowedMediaExt[ext] {
			http.NotFound(w, r)
			return
		}

		full := filepath.Join(musicDir, clean)
		if !withinDir(musicDir, full) {
			http.NotFound(w, r)
			return
		}

		r2 := r.Clone(r.Context())
		r2.URL.Path = "/" + slashPath
		fileServer.ServeHTTP(w, r2)
	})
}

// newSPAHandler serves the built frontend. Existing files are served as-is;
// unknown extensionless paths fall back to index.html so client-side routes
// work. A missing asset (a path with an extension) returns 404 instead of
// the HTML shell.
func newSPAHandler(buildDir string) http.Handler {
	fileServer := http.FileServer(http.Dir(buildDir))
	index := filepath.Join(buildDir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(r.URL.Path, "/")
		if rel != "" && !strings.ContainsRune(rel, 0) {
			clean := filepath.Clean(rel)
			full := filepath.Join(buildDir, clean)
			if withinDir(buildDir, full) {
				//nolint:gosec // G703 - rel came from the request path, was NUL-checked and Cleaned, and full is confined to buildDir by the withinDir guard
				info, err := os.Stat(full)
				switch {
				case err == nil && !info.IsDir():
					r2 := r.Clone(r.Context())
					r2.URL.Path = "/" + filepath.ToSlash(clean)
					fileServer.ServeHTTP(w, r2)
					return
				case err == nil && info.IsDir():
					// Directory: fall through to the SPA shell.
				case os.IsNotExist(err):
					if filepath.Ext(clean) != "" {
						http.NotFound(w, r)
						return
					}
				default:
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
			}
		}
		http.ServeFile(w, r, index)
	})
}

// withinDir reports whether path is base itself or lies inside base.
func withinDir(base, path string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
