package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"session-insight/internal/db"
)

//go:embed frontend/dist
var frontend embed.FS

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	frontendFS, err := fs.Sub(frontend, "frontend/dist")
	if err != nil {
		log.Fatalf("failed to get frontend sub-fs: %v", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("failed to get home dir: %v", err)
	}
	dataDir := filepath.Join(homeDir, ".session-insight")
	database, err := db.Open(dataDir)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(frontendFS)))

	log.Printf("SessionInsight listening on http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
