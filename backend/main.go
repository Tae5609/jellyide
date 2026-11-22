// Command yt-scraper is the JellyIDE server: it serves the API, the media
// files and the SPA frontend.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"yt-scraper/internal/api"
	"yt-scraper/internal/auth"
	"yt-scraper/internal/db"
	"yt-scraper/internal/download"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
	"yt-scraper/internal/metadata"
	"yt-scraper/internal/scanner"

	"github.com/gorilla/mux"
)

// spaBuildDir is where the frontend build output is expected.
const spaBuildDir = "./frontend/build"

func main() {
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
	api.RegisterRoutes(apiSub, downloader, libService, metaService, jobManager, musicScanner)

	// Serve media files
	r.PathPrefix("/media/").Handler(http.StripPrefix("/media", http.FileServer(http.Dir(downloadDir))))

	// SPA Handler for Frontend
	r.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// Resolve the request against the build dir and refuse anything
		// that could escape it (gosec G703): the relative path must be
		// non-empty and must not contain a ".." segment.
		rel := strings.TrimPrefix(path, "/")
		if rel == "" || strings.Contains(rel, "..") {
			http.ServeFile(w, r, filepath.Join(spaBuildDir, "index.html"))
			return
		}
		fullPath := filepath.Join(spaBuildDir, rel)

		// If the file doesn't exist, serve index.html.
		info, err := os.Stat(fullPath) //nolint:gosec // G703 - rel was validated above (non-empty, no "..") and Join keeps fullPath under spaBuildDir; FileServer re-validates when serving
		if os.IsNotExist(err) || info.IsDir() {
			http.ServeFile(w, r, filepath.Join(spaBuildDir, "index.html"))
			return
		}

		// Otherwise serve the static file
		http.FileServer(http.Dir(spaBuildDir)).ServeHTTP(w, r)
	})

	// Wrap the whole router with the auth middleware. The SPA is
	// same-origin, so no CORS handling is needed.
	handler := authSvc.Middleware(r)

	port := getEnv("PORT", "8080")
	log.Printf("JellyIDE Server starting on :%s", port)
	log.Printf("DB Path: %s", dbPath)
	log.Printf("Media Dir: %s", downloadDir)

	// http.Server with a read-header timeout (gosec G114); no write timeout
	// so long media streams are unaffected.
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	err = srv.ListenAndServe()
	if cerr := database.Close(); cerr != nil {
		log.Printf("Failed to close database: %v", cerr)
	}
	if err != nil {
		log.Fatal("Failed to start server: ", err)
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
