package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkOrderFreezeAndStaleness(t *testing.T) {
	s := testStore(t)
	checkout := t.TempDir()
	rec := importPNG(t, s, "claude", "04-thinking", 1)

	wo, err := generateWorkOrder(s, checkout, "claude", map[string]*Candidate{
		"04-thinking": {ItemID: "04-thinking", SessionID: "s1", ResumeID: "r1",
			ResumeCommand: "cd /tmp/x && claude --resume r1", TurnIndex: 2, Precision: PrecisionExact},
	})
	if err != nil {
		t.Fatalf("generateWorkOrder: %v", err)
	}
	if wo.Frozen["04-thinking"] != rec.Hash {
		t.Fatalf("frozen hash = %s, want %s", wo.Frozen["04-thinking"], rec.Hash)
	}
	dir := filepath.Join(checkout, ".runtime", "reference-work", wo.ID)
	for _, want := range []string{
		"WORK_ORDER.md",
		"selected-reference-assets/04-thinking.png",
		"local-candidate-context/candidates.txt",
	} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("work order file %s missing: %v", want, err)
		}
	}
	md, _ := os.ReadFile(filepath.Join(dir, "WORK_ORDER.md"))
	if !strings.Contains(string(md), rec.Hash[:12]) {
		t.Error("WORK_ORDER.md must record the frozen hash prefix")
	}
	if !strings.Contains(string(md), "thinking") {
		t.Error("WORK_ORDER.md must map the input to its feature")
	}
	ctx, _ := os.ReadFile(filepath.Join(dir, "local-candidate-context", "candidates.txt"))
	if !strings.Contains(string(ctx), "claude --resume r1") {
		t.Error("candidate context must keep the local resume command")
	}

	cat, err := s.catalogs.load("claude")
	if err != nil {
		t.Fatal(err)
	}
	if got := workOrderState(cat.WorkOrders[0], cat); got != WorkOrderActive {
		t.Fatalf("fresh work order state = %s, want active", got)
	}

	// Input changed after freezing: the work order is stale.
	importPNG(t, s, "claude", "04-thinking", 2)
	cat, _ = s.catalogs.load("claude")
	if got := workOrderState(cat.WorkOrders[0], cat); got != WorkOrderStale {
		t.Fatalf("after input change state = %s, want stale", got)
	}

	// Accepting the frozen content consumes the work order.
	acceptCapture(t, s, "claude", "04-thinking")
	cat, _ = s.catalogs.load("claude")
	if got := workOrderState(cat.WorkOrders[0], cat); got != WorkOrderStale {
		// accepted content moved past the frozen hash: still not active
		t.Fatalf("accepted-after-update state = %s", got)
	}
}

func TestWorkOrderConsumedWhenFrozenAccepted(t *testing.T) {
	s := testStore(t)
	checkout := t.TempDir()
	importPNG(t, s, "claude", "04-thinking", 1)
	wo, err := generateWorkOrder(s, checkout, "claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	acceptCapture(t, s, "claude", "04-thinking")
	cat, _ := s.catalogs.load("claude")
	if got := workOrderState(cat.WorkOrders[0], cat); got != WorkOrderConsumed {
		t.Fatalf("state = %s, want consumed (wo frozen=%v)", got, wo.Frozen)
	}
}

func TestWorkOrderRequiresPendingInputs(t *testing.T) {
	s := testStore(t)
	if _, err := generateWorkOrder(s, t.TempDir(), "claude", nil); err == nil {
		t.Fatal("work order with no pending inputs must fail")
	}
}

func TestAllowedGaps(t *testing.T) {
	s := testStore(t)
	importPNG(t, s, "claude", "04-thinking", 1)
	cat, _ := s.catalogs.load("claude")
	gaps := allowedGaps(cat, map[string]*Candidate{
		"02-user-prompt": {ItemID: "02-user-prompt"},
	})
	for _, g := range gaps {
		if g == "04-thinking" || g == "02-user-prompt" {
			t.Fatalf("%s is covered and must not be an allowed gap", g)
		}
	}
	found := false
	for _, g := range gaps {
		if g == "04-thinking-toggled" {
			found = true
		}
	}
	if !found {
		t.Fatal("uncaptured fold variant must appear as an allowed gap")
	}
}
