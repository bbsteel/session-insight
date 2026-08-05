package adaptertest

import (
	"testing"

	"github.com/bbsteel/session-insight/internal/model"
	"github.com/bbsteel/session-insight/internal/reader/capability"
	"github.com/bbsteel/session-insight/internal/reader/provenance"
)

// AssertProvenanceComplete validates a successful complete snapshot.
func AssertProvenanceComplete(t *testing.T, detail *model.SessionDetail, caps capability.AgentCapabilities) {
	t.Helper()
	if detail == nil || detail.Provenance == nil {
		t.Fatal("expected non-nil provenance on complete session")
	}
	p := *detail.Provenance
	if p.State != model.RecordComplete {
		t.Fatalf("state=%s want complete", p.State)
	}
	if p.AdapterRevision != caps.AdapterRevision || p.AdapterRevision <= 0 {
		t.Fatalf("adapter_revision=%d want %d", p.AdapterRevision, caps.AdapterRevision)
	}
	if errs := provenance.Validate(p); len(errs) != 0 {
		t.Fatalf("validate: %v", errs)
	}
	if errs := provenance.ValidateReplayable(p, len(detail.Turns) > 0); len(errs) != 0 {
		t.Fatalf("replayable: %v", errs)
	}
	if len(p.Sources) == 0 {
		t.Fatal("expected source inventory")
	}
	for _, s := range p.Sources {
		if s.Path == "" || !provenance.IsKnownSourceRole(s.Role) {
			t.Fatalf("bad source: %+v", s)
		}
	}
}

// AssertProvenanceDegradedOrUnsupported validates a non-complete fixture.
func AssertProvenanceDegradedOrUnsupported(t *testing.T, detail *model.SessionDetail, caps capability.AgentCapabilities) {
	t.Helper()
	if detail == nil || detail.Provenance == nil {
		t.Fatal("expected provenance on degraded/unsupported fixture")
	}
	p := *detail.Provenance
	switch p.State {
	case model.RecordDegraded, model.RecordParserUnsupported, model.RecordMetadataOnly:
	default:
		t.Fatalf("state=%s want degraded|parser_unsupported|metadata_only", p.State)
	}
	if p.AdapterRevision != caps.AdapterRevision {
		t.Fatalf("adapter_revision=%d want %d", p.AdapterRevision, caps.AdapterRevision)
	}
	if errs := provenance.Validate(p); len(errs) != 0 {
		// source_missing missing_since soft rule may not apply here
		filtered := errs[:0]
		for _, e := range errs {
			if e.Code == "source_missing_without_missing_since" {
				continue
			}
			filtered = append(filtered, e)
		}
		if len(filtered) != 0 {
			t.Fatalf("validate: %v", filtered)
		}
	}
	if len(p.Sources) == 0 {
		t.Fatal("expected source inventory on non-complete fixture")
	}
	for _, s := range p.Sources {
		if s.Path == "" || !provenance.IsKnownSourceRole(s.Role) {
			t.Fatalf("bad source: %+v", s)
		}
	}
	if p.State == model.RecordDegraded {
		hasImpact := false
		for _, w := range p.Warnings {
			if w.AffectsCompleteness {
				hasImpact = true
				if len(w.Impacts) == 0 {
					t.Fatalf("affects_completeness warning %q requires Impacts", w.Code)
				}
				for _, imp := range w.Impacts {
					if !provenance.IsKnownImpact(imp) {
						t.Fatalf("unknown impact %q", imp)
					}
				}
			}
		}
		if !hasImpact {
			t.Fatal("degraded requires affects_completeness warning")
		}
	}
}
