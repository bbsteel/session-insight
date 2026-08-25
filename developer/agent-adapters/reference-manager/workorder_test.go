package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkOrderFreezeAndStaleness(t *testing.T) {
	stubEmptyBaseline(t)
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
	if wo.SchemaVersion != WorkOrderSchemaV2 {
		t.Fatalf("schema = %d, want %d", wo.SchemaVersion, WorkOrderSchemaV2)
	}
	if wo.Frozen["04-thinking"] != rec.Hash {
		t.Fatalf("frozen hash = %s, want %s", wo.Frozen["04-thinking"], rec.Hash)
	}
	if wo.BaselineSHA == "" || wo.PreflightCommand == "" {
		t.Fatalf("v2 work order missing baseline or preflight: %+v", wo)
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
	if !strings.Contains(string(md), rec.Hash) {
		t.Error("WORK_ORDER.md must record the full frozen SHA-256")
	}
	if !strings.Contains(string(md), "work_order_schema_version: 2") {
		t.Error("WORK_ORDER.md must declare schema v2")
	}
	if !strings.Contains(string(md), "thinking") {
		t.Error("WORK_ORDER.md must map the input to its feature")
	}
	if !strings.Contains(string(md), "verify-work-order") {
		t.Error("WORK_ORDER.md must include the preflight command")
	}
	ctx, _ := os.ReadFile(filepath.Join(dir, "local-candidate-context", "candidates.txt"))
	if !strings.Contains(string(ctx), "claude --resume r1") {
		t.Error("candidate context must keep the local resume command")
	}

	cat, err := s.catalogs.load("claude")
	if err != nil {
		t.Fatal(err)
	}
	if got := workOrderState(cat.WorkOrders[0], cat, nil); got != WorkOrderActive {
		t.Fatalf("fresh work order state = %s, want active", got)
	}

	importPNG(t, s, "claude", "04-thinking", 2)
	cat, _ = s.catalogs.load("claude")
	if got := workOrderState(cat.WorkOrders[0], cat, nil); got != WorkOrderStale {
		t.Fatalf("after input change state = %s, want stale", got)
	}
}

func TestWorkOrderConsumedWhenMainLockMatches(t *testing.T) {
	stubEmptyBaseline(t)
	s := testStore(t)
	checkout := t.TempDir()
	rec := importPNG(t, s, "claude", "04-thinking", 1)
	wo, err := generateWorkOrder(s, checkout, "claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	lockHashes := map[string]string{"04-thinking": rec.Hash, "04-thinking.png": rec.Hash}
	cat, _ := s.catalogs.load("claude")
	if got := workOrderState(*wo, cat, lockHashes); got != WorkOrderConsumed {
		t.Fatalf("state = %s, want consumed (wo frozen=%v)", got, wo.Frozen)
	}
}

func TestPendingInputsSkipMainLockMatches(t *testing.T) {
	s := testStore(t)
	rec := importPNG(t, s, "claude", "04-thinking", 1)
	stubLockFor(t, "04-thinking", rec.Hash)
	_, err := generateWorkOrder(s, t.TempDir(), "claude", nil)
	if err == nil {
		t.Fatal("matching main lock should leave nothing pending")
	}
	if !strings.Contains(err.Error(), "no pending reference inputs") {
		t.Fatalf("error = %v, want no-pending-inputs", err)
	}
}

func TestWorkOrderUnsupportedSchema(t *testing.T) {
	cat := newAgentCatalog("claude")
	rec := WorkOrderRecord{ID: "old", Frozen: map[string]string{"04-thinking": "abc"}}
	if got := workOrderState(rec, cat, nil); got != WorkOrderUnsupported {
		t.Fatalf("state = %s, want unsupported_schema", got)
	}
}

func TestCreateWorkOrderDirSameSecondCollision(t *testing.T) {
	checkout := t.TempDir()
	orig := workOrderTimestamp
	workOrderTimestamp = func() string { return "20060102-150405" }
	t.Cleanup(func() { workOrderTimestamp = orig })

	firstID, firstDir, err := createWorkOrderDir(checkout, "claude")
	if err != nil {
		t.Fatal(err)
	}
	secondID, secondDir, err := createWorkOrderDir(checkout, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if firstID == secondID || firstDir == secondDir {
		t.Fatalf("same-second directories collided: %s %s", firstID, secondID)
	}
}

func TestGenerateWorkOrderRefusesActiveDuplicate(t *testing.T) {
	stubEmptyBaseline(t)
	s := testStore(t)
	checkout := t.TempDir()
	importPNG(t, s, "claude", "04-thinking", 1)

	first, err := generateWorkOrder(s, checkout, "claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateWorkOrder(s, checkout, "claude", nil)
	if err == nil {
		t.Fatalf("duplicate freeze succeeded as %s", second.ID)
	}
	var frozen *alreadyFrozenError
	if !errors.As(err, &frozen) || frozen.Record.ID != first.ID {
		t.Fatalf("duplicate freeze error = %v, want alreadyFrozenError for %s", err, first.ID)
	}
	cat, _ := s.catalogs.load("claude")
	if len(cat.WorkOrders) != 1 {
		t.Fatalf("catalog work orders = %d, want 1", len(cat.WorkOrders))
	}
	entries, err := os.ReadDir(workOrderRoot(checkout))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("work-order directories = %d, want 1", len(entries))
	}
}

func TestGenerateWorkOrderAfterInputChange(t *testing.T) {
	stubEmptyBaseline(t)
	s := testStore(t)
	checkout := t.TempDir()
	importPNG(t, s, "claude", "04-thinking", 1)
	first, err := generateWorkOrder(s, checkout, "claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	importPNG(t, s, "claude", "04-thinking", 2)
	second, err := generateWorkOrder(s, checkout, "claude", nil)
	if err != nil {
		t.Fatalf("changed input should freeze a new work order: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("changed input reused the stale work order id")
	}
	cat, _ := s.catalogs.load("claude")
	if len(cat.WorkOrders) != 2 {
		t.Fatalf("catalog work orders = %d, want 2", len(cat.WorkOrders))
	}
}

func TestGenerateWorkOrderAllowsNewPendingFile(t *testing.T) {
	stubEmptyBaseline(t)
	s := testStore(t)
	checkout := t.TempDir()
	importPNG(t, s, "claude", "04-thinking", 1)
	first, err := generateWorkOrder(s, checkout, "claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	importPNG(t, s, "claude", "01-session-overview", 2)
	second, err := generateWorkOrder(s, checkout, "claude", nil)
	if err != nil {
		t.Fatalf("new pending file should freeze a new work order: %v", err)
	}
	if second.ID == first.ID {
		t.Fatal("expanded pending set reused the previous work order id")
	}
	if len(second.Items) != 2 {
		t.Fatalf("new work order items = %v, want both pending files", second.Items)
	}
}

func TestWorkOrderRequiresPendingInputs(t *testing.T) {
	stubEmptyBaseline(t)
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
