package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

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

func main() {
	// Initialize config
	downloadDir := getEnv("DOWNLOAD_DIR", "/mnt/media/music")
	dbPath := filepath.Join(downloadDir, "jellyide.db")
	os.MkdirAll(downloadDir, 0755)

	// Initialize DB
	database, err := db.NewDB(dbPath)
	if err != nil {
		log.Fatal("Failed to initialize database: ", err)
	}
	defer database.Close()

	// Initialize auth (bootstraps the admin account when needed)
	authSvc, err := auth.NewService(auth.LoadConfig(), database)
	if err != nil {
		log.Fatal("Failed to initialize auth: ", err)
	}

	// Initialize Services
	libService := library.NewService(database, downloadDir)
	jobManager := jobs.NewManager(database)
	musicScanner := scanner.NewScanner(libService, jobManager, downloadDir)
	downloader := download.NewDownloader(jobManager, musicScanner, downloadDir)
	metaService := metadata.NewService(database, libService)

	// Main Router
	r := mux.NewRouter()

	// API Router (Using Subrouter natively)
	apiSub := r.PathPrefix("/api").Subrouter()
	authSvc.RegisterRoutes(apiSub)
	api.RegisterRoutes(apiSub, downloader, libService, metaService, jobManager, musicScanner)

	// Serve media files
	r.PathPrefix("/media/").Handler(http.StripPrefix("/media", http.FileServer(http.Dir(downloadDir))))

	// SPA Handler for Frontend
	r.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		fullPath := filepath.Join("./frontend/build", path)

		// If path is "/" or file doesn't exist, serve index.html
		if path == "/" {
			http.ServeFile(w, r, "./frontend/build/index.html")
			return
		}

		info, err := os.Stat(fullPath)
		if os.IsNotExist(err) || info.IsDir() {
			http.ServeFile(w, r, "./frontend/build/index.html")
			return
		}

		// Otherwise serve the static file
		http.FileServer(http.Dir("./frontend/build")).ServeHTTP(w, r)
	})

	// Wrap the whole router with the auth middleware. The SPA is
	// same-origin, so no CORS handling is needed.
	handler := authSvc.Middleware(r)

	port := getEnv("PORT", "8080")
	log.Printf("JellyIDE Server starting on :%s", port)
	log.Printf("DB Path: %s", dbPath)
	log.Printf("Media Dir: %s", downloadDir)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
