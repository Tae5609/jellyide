package api

import (
	"net/http"
	"yt-scraper/internal/scanner"
)

func triggerScan(sc *scanner.Scanner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := sc.Scan()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}
