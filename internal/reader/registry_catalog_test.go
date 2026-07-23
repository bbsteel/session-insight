package reader_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/reader"
	"github.com/bbsteel/session-insight/internal/reader/capability"
	"github.com/bbsteel/session-insight/internal/reader/chrys"
	"github.com/bbsteel/session-insight/internal/reader/claude"
	"github.com/bbsteel/session-insight/internal/reader/codex"
	"github.com/bbsteel/session-insight/internal/reader/copilot"
	"github.com/bbsteel/session-insight/internal/reader/grok"
	"github.com/bbsteel/session-insight/internal/reader/opencode"

	_ "github.com/mattn/go-sqlite3"
)

// expectedAgentTypes is the closed set of supported Agents for phase 1.
var expectedAgentTypes = []string{
	"chrys", "claude", "codex", "copilot", "grok", "opencode",
}

func TestAgentDefinitionsHasSixAgents(t *testing.T) {
	defs := reader.AgentDefinitions()
	if len(defs) != 6 {
		t.Fatalf("catalog length = %d, want 6: %v", len(defs), agentTypes(defs))
	}

	got := agentTypes(defs)
	sort.Strings(got)
	want := append([]string(nil), expectedAgentTypes...)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("agent types = %v, want %v", got, want)
	}
}

func TestAgentDefinitionsValidateAndAreComplete(t *testing.T) {
	defs := reader.AgentDefinitions()
	for _, d := range defs {
		if d.DisplayName == "" {
			t.Errorf("%s: empty DisplayName", d.AgentType)
		}
		if d.AdapterRevision < 1 {
			t.Errorf("%s: AdapterRevision=%d", d.AgentType, d.AdapterRevision)
		}
		if errs := capability.ValidateStatic(d); len(errs) != 0 {
			t.Errorf("%s: validation failed: %v", d.AgentType, errs)
		}
		// Exactly ten baseline capabilities.
		if len(d.Capabilities) != 10 {
			t.Errorf("%s: capability count = %d, want 10", d.AgentType, len(d.Capabilities))
		}
		for _, id := range capability.BaselineIDs() {
			if _, ok := d.Capabilities[id]; !ok {
				t.Errorf("%s: missing capability %s", d.AgentType, id)
			}
		}
	}
}

func TestAgentDefinitionLookup(t *testing.T) {
	for _, typ := range expectedAgentTypes {
		d, ok := reader.AgentDefinition(typ)
		if !ok {
			t.Fatalf("AgentDefinition(%q) not found", typ)
		}
		if d.AgentType != typ {
			t.Fatalf("AgentDefinition(%q).AgentType = %q", typ, d.AgentType)
		}
	}
	if _, ok := reader.AgentDefinition("nope"); ok {
		t.Fatal("unknown agent should not be found")
	}
}

func TestAgentDefinitionsStableOrder(t *testing.T) {
	a := agentTypes(reader.AgentDefinitions())
	b := agentTypes(reader.AgentDefinitions())
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("order not stable: %v vs %v", a, b)
	}
	// Sorted by AgentType.
	sorted := append([]string(nil), a...)
	sort.Strings(sorted)
	if strings.Join(a, ",") != strings.Join(sorted, ",") {
		t.Fatalf("catalog not sorted by AgentType: %v", a)
	}
}

func TestAgentDefinitionsMatchReaderIdentity(t *testing.T) {
	// Catalog AgentType/DisplayName must match what a constructed reader reports.
	// Paths need not exist for identity methods.
	tmp := t.TempDir()
	type pair struct {
		decl   capability.AgentCapabilities
		reader reader.BaseSessionReader
	}
	// opencode.New needs a real sqlite file; skip construction and only
	// check exported Capabilities vs AgentType constants via other adapters.
	pairs := []pair{
		{claude.Capabilities(), claude.New(tmp)},
		{codex.Capabilities(), codex.New(tmp)},
		{copilot.Capabilities(), copilot.New(tmp)},
		{chrys.Capabilities(), chrys.New(tmp)},
		{grok.Capabilities(), grok.New(tmp)},
	}
	for _, p := range pairs {
		if p.decl.AgentType != p.reader.AgentType() {
			t.Errorf("decl AgentType %q != reader %q", p.decl.AgentType, p.reader.AgentType())
		}
		if p.decl.DisplayName != p.reader.DisplayName() {
			t.Errorf("%s: decl DisplayName %q != reader %q",
				p.decl.AgentType, p.decl.DisplayName, p.reader.DisplayName())
		}
	}
	// OpenCode identity from declaration alone (New requires DB).
	oc := opencode.Capabilities()
	if oc.AgentType != "opencode" || oc.DisplayName != "OpenCode" {
		t.Errorf("opencode identity: %+v", oc)
	}
}

// TestOperationDeclarationsMatchOptionalInterfaces checks delete/terminate/realtime
// claims against optional Go interfaces. Lives outside the leaf capability package
// to avoid import cycles.
func TestOperationDeclarationsMatchOptionalInterfaces(t *testing.T) {
	tmp := t.TempDir()
	// Build one reader per adapter. OpenCode needs a minimal sqlite file.
	ocDB := filepath.Join(tmp, "opencode.db")
	if err := writeMinimalOpenCodeDB(ocDB); err != nil {
		t.Fatalf("seed opencode db: %v", err)
	}
	ocReader, err := opencode.New(ocDB)
	if err != nil {
		t.Fatalf("opencode.New: %v", err)
	}

	readers := map[string]reader.BaseSessionReader{
		"claude":   claude.New(tmp),
		"codex":    codex.New(tmp),
		"copilot":  copilot.New(tmp),
		"chrys":    chrys.New(tmp),
		"grok":     grok.New(tmp),
		"opencode": ocReader,
	}

	for _, def := range reader.AgentDefinitions() {
		r, ok := readers[def.AgentType]
		if !ok {
			t.Fatalf("no test reader for %s", def.AgentType)
		}

		del := def.Capabilities[capability.CapabilityDelete]
		if del.State == capability.CapabilityExact {
			if _, ok := r.(reader.SessionDeleter); !ok {
				t.Errorf("%s: delete=exact but reader does not implement SessionDeleter", def.AgentType)
			}
		}

		term := def.Capabilities[capability.CapabilityTerminate]
		if term.State == capability.CapabilityExact {
			if _, ok := r.(reader.SessionProcessFinder); !ok {
				t.Errorf("%s: terminate=exact but reader does not implement SessionProcessFinder", def.AgentType)
			}
		}
		if term.State == capability.CapabilityEstimated {
			t.Errorf("%s: terminate=estimated is forbidden", def.AgentType)
		}

		rt := def.Capabilities[capability.CapabilityRealtime]
		if rt.State == capability.CapabilityExact || rt.State == capability.CapabilityEstimated {
			if _, ok := r.(reader.LiveRevisionProvider); !ok {
				t.Errorf("%s: realtime=%s but reader does not implement LiveRevisionProvider",
					def.AgentType, rt.State)
			}
		}
	}
}

// TestCatalogMatrixDump prints the six×ten matrix for evidence capture.
// Always asserts; also writes a human-readable dump when CAPABILITY_MATRIX_OUT is set.
func TestCatalogMatrixDump(t *testing.T) {
	var b strings.Builder
	b.WriteString("agent_type\tcapability\tstate\treason_code\n")
	for _, def := range reader.AgentDefinitions() {
		for _, id := range capability.BaselineIDs() {
			d := def.Capabilities[id]
			fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", def.AgentType, id, d.State, d.ReasonCode)
		}
	}
	matrix := b.String()
	// Structural proof of catalog content.
	lines := strings.Split(strings.TrimSpace(matrix), "\n")
	// header + 6*10
	if len(lines) != 1+60 {
		t.Fatalf("matrix lines = %d, want 61", len(lines))
	}
	if out := os.Getenv("CAPABILITY_MATRIX_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(matrix), 0o644); err != nil {
			t.Fatalf("write matrix: %v", err)
		}
		t.Logf("wrote capability matrix to %s", out)
	}
	t.Log("\n" + matrix)
}

func agentTypes(defs []capability.AgentCapabilities) []string {
	out := make([]string, len(defs))
	for i, d := range defs {
		out[i] = d.AgentType
	}
	return out
}

func writeMinimalOpenCodeDB(path string) error {
	// Minimal schema so opencode.New can open and Ping the file.
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS session (
  id text PRIMARY KEY,
  directory text NOT NULL DEFAULT '',
  title text NOT NULL DEFAULT '',
  time_created integer NOT NULL DEFAULT 0,
  time_updated integer NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS message (
  id text PRIMARY KEY,
  session_id text NOT NULL,
  time_created integer NOT NULL DEFAULT 0,
  data text NOT NULL DEFAULT ''
);
`)
	return err
}
