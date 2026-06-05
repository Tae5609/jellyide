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
	jobManager := jobs.NewManager(database)
	libService := library.NewService(database, downloadDir)
	metaService := metadata.NewService(database, libService)
	scanService := scanner.NewScanner(libService, jobManager, downloadDir)
	downloader := download.NewDownloader(jobManager, downloadDir, scanService)

	// Trigger initial scan
	scanService.Scan()

	// API Router
	router := api.NewRouter(downloader, libService, metaService, jobManager, scanService)

	// Setup Server
	r := mux.NewRouter()
	r.PathPrefix("/api/").Handler(http.StripPrefix("/api", router))

	// Serve Static Files with SPA Fallback
	staticDir := "./frontend/build/"
	r.PathPrefix("/").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(staticDir, r.URL.Path)

		// Check if file exists
		_, err := os.Stat(path)
		if os.IsNotExist(err) || r.URL.Path == "/" {
			// Serve index.html for SPA routes
			http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Otherwise serve the static file
		http.FileServer(http.Dir(staticDir)).ServeHTTP(w, r)
	})

	c := cors.New(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
	})

	port := getEnv("PORT", "8080")
	log.Printf("JellyIDE Server starting on :%s", port)
	log.Printf("DB Path: %s", dbPath)
	log.Fatal(http.ListenAndServe(":"+port, c.Handler(r)))
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
