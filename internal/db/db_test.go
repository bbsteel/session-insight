package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	database, err := Open(dir)
	if err != nil {
		t.Fatalf("Open() failed: %v", err)
	}
	defer database.Close()

	dbPath := filepath.Join(dir, "index.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("index.db was not created")
	}
}
