package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"yt-scraper/internal/api"
	"yt-scraper/internal/db"
	"yt-scraper/internal/download"
	"yt-scraper/internal/jobs"
	"yt-scraper/internal/library"
	"yt-scraper/internal/metadata"
	"yt-scraper/internal/scanner"

	"github.com/gorilla/mux"
	"github.com/rs/cors"
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

	c := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
	})

	port := getEnv("PORT", "8080")
	log.Printf("JellyIDE Server starting on :%s", port)
	log.Printf("DB Path: %s", dbPath)
	log.Printf("Media Dir: %s", downloadDir)
	log.Fatal(http.ListenAndServe(":"+port, c.Handler(r)))
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
