package main

import (
	"path/filepath"
	"testing"
)

func TestEvaluatePreflightOK(t *testing.T) {
	stubEmptyBaseline(t)
	s := testStore(t)
	checkout := t.TempDir()
	importPNG(t, s, "claude", "04-thinking", 1)
	wo, err := generateWorkOrder(s, checkout, "claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := evaluatePreflight(s, checkout, "claude", *wo)
	if !got.OK || got.ResultCode != ResultOK {
		t.Fatalf("preflight = %+v", got)
	}
}

func TestEvaluatePreflightWorkOrderChanged(t *testing.T) {
	stubEmptyBaseline(t)
	s := testStore(t)
	checkout := t.TempDir()
	importPNG(t, s, "claude", "04-thinking", 1)
	wo, err := generateWorkOrder(s, checkout, "claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	importPNG(t, s, "claude", "04-thinking", 2)
	got := evaluatePreflight(s, checkout, "claude", *wo)
	if got.OK || got.ResultCode != ResultWorkOrderChanged {
		t.Fatalf("preflight = %+v, want work_order_changed", got)
	}
}

func TestEvaluatePreflightUnsupportedSchema(t *testing.T) {
	stubEmptyBaseline(t)
	s := testStore(t)
	got := evaluatePreflight(s, t.TempDir(), "claude", WorkOrderRecord{ID: "old", SchemaVersion: 1})
	if got.OK || got.ResultCode != ResultUnsupportedWorkOrderSchema {
		t.Fatalf("preflight = %+v", got)
	}
}

func TestParseWorkOrderHeader(t *testing.T) {
	md := "# Terminal reference work order: grok\n\n- Work order ID: `grok-1`\n- work_order_schema_version: 2\n"
	agent, id, err := parseWorkOrderHeader(md)
	if err != nil || agent != "grok" || id != "grok-1" {
		t.Fatalf("parse = %s %s %v", agent, id, err)
	}
	if _, _, err := parseWorkOrderHeader("# Terminal reference work order: grok\n- Work order ID: `x`\n"); err == nil {
		t.Fatal("schema v1 markdown must be rejected")
	}
}

func TestPreflightWorkOrderFile(t *testing.T) {
	stubEmptyBaseline(t)
	s := testStore(t)
	checkout := t.TempDir()
	importPNG(t, s, "claude", "04-thinking", 1)
	wo, err := generateWorkOrder(s, checkout, "claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(checkout, wo.Dir, "WORK_ORDER.md")
	got := preflightWorkOrderFile(s, checkout, path)
	if !got.OK {
		t.Fatalf("file preflight = %+v", got)
	}
}
