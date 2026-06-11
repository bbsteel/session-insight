package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
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

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(frontendFS)))

	log.Printf("SessionInsight listening on http://localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
