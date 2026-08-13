package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRunShPortIsolation(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "test-run-port-isolation.sh")
	cmd := exec.Command("bash", script)
	cmd.Dir = root
	// Agent shells often export PORT=8080; the script must isolate itself.
	cmd.Env = append(os.Environ(), "PORT=8080")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("scripts/test-run-port-isolation.sh failed: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
