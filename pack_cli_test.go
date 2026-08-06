package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSelection(t *testing.T) {
	tests := []struct {
		arg       string
		wantAgent string
		wantID    string
		wantErr   bool
	}{
		{arg: "claude:abc-123", wantAgent: "claude", wantID: "abc-123"},
		// Grok ids contain '-' but never ':'; split on the FIRST colon.
		{arg: "grok:1-2-3", wantAgent: "grok", wantID: "1-2-3"},
		{arg: "opencode:ses_abc", wantAgent: "opencode", wantID: "ses_abc"},
		// Extra colons stay in the id.
		{arg: "agent:id:with:colons", wantAgent: "agent", wantID: "id:with:colons"},
		{arg: "nocolon", wantErr: true},
		{arg: ":id", wantErr: true},
		{arg: "claude:", wantErr: true},
		{arg: "", wantErr: true},
	}
	for _, tt := range tests {
		sel, err := parseSelection(tt.arg)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseSelection(%q): want error, got %+v", tt.arg, sel)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSelection(%q): %v", tt.arg, err)
			continue
		}
		if sel.AgentType != tt.wantAgent || sel.ID != tt.wantID {
			t.Errorf("parseSelection(%q) = %+v, want %s:%s", tt.arg, sel, tt.wantAgent, tt.wantID)
		}
	}
}

func TestRunPackCLIMissingSubcommand(t *testing.T) {
	err := runPackCLI(nil, t.TempDir(), nil, "test")
	if err == nil || !strings.Contains(err.Error(), "missing subcommand") {
		t.Fatalf("want missing-subcommand error, got %v", err)
	}
}

func TestRunPackCLIUnknownSubcommand(t *testing.T) {
	err := runPackCLI([]string{"ship"}, t.TempDir(), nil, "test")
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("want unknown-subcommand error, got %v", err)
	}
}

func TestRunPackExportRequiresOutputAndSelectors(t *testing.T) {
	err := runPackExport(nil, nil, "test")
	if err == nil || !strings.Contains(err.Error(), "-o") {
		t.Fatalf("want -o required error, got %v", err)
	}
	err = runPackExport([]string{"-o", filepath.Join(t.TempDir(), "x.sibundle")}, nil, "test")
	if err == nil || !strings.Contains(err.Error(), "selector") {
		t.Fatalf("want selector-required error, got %v", err)
	}
}

func TestRunPackImportRequiresPath(t *testing.T) {
	err := runPackImport(nil, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "sibundle") {
		t.Fatalf("want path-required error, got %v", err)
	}
	// Missing file surfaces as open error before any DB touch.
	missing := filepath.Join(t.TempDir(), "no-such.sibundle")
	err = runPackImport([]string{missing}, t.TempDir(), nil)
	if err == nil {
		t.Fatal("want open error for missing file")
	}
	if !os.IsNotExist(err) && !strings.Contains(err.Error(), "open") && !strings.Contains(err.Error(), "no such") {
		t.Fatalf("want open/missing error, got %v", err)
	}
}
