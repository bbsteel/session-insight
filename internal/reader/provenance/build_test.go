package provenance

import (
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestDeriveStatePriority(t *testing.T) {
	warn := Warning(model.WarnMalformedRecordSkipped, model.WarningSeverityWarning, true,
		[]string{model.ImpactReplay}, model.SourceRolePrimaryTranscript, nil, 2)

	cases := []struct {
		name    string
		hasBody bool
		warns   []model.ParseWarning
		want    model.RecordCompletenessState
	}{
		{"complete", true, nil, model.RecordComplete},
		{"degraded", true, []model.ParseWarning{warn}, model.RecordDegraded},
		{"metadata_only", false, nil, model.RecordMetadataOnly},
		{"metadata_even_with_warn", false, []model.ParseWarning{warn}, model.RecordMetadataOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveState(tc.hasBody, tc.warns, nil)
			if got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}

func TestBuildCompleteAndDegraded(t *testing.T) {
	now := time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)
	mt := now.Add(-time.Minute)
	sz := int64(100)
	src := model.SessionSourceFile{
		Role: model.SourceRolePrimaryTranscript, Path: "/tmp/a.jsonl",
		State: model.SourcePresent, UpdatedAt: &mt, SizeBytes: &sz,
	}

	complete := Build(Input{
		CapturedAt: now, AdapterRevision: 2,
		Sources: []model.SessionSourceFile{src}, HasReplayableBody: true,
	})
	if complete.State != model.RecordComplete {
		t.Fatalf("state=%s", complete.State)
	}
	if errs := Validate(complete); len(errs) != 0 {
		t.Fatalf("validate complete: %v", errs)
	}
	if complete.SourceUpdatedAt == nil || !complete.SourceUpdatedAt.Equal(mt.UTC()) {
		t.Fatalf("source_updated_at: %v", complete.SourceUpdatedAt)
	}

	warn := Warning(model.WarnMalformedRecordSkipped, model.WarningSeverityWarning, true,
		[]string{model.ImpactReplay}, model.SourceRolePrimaryTranscript, ptrInt64(10), 3)
	degraded := Build(Input{
		CapturedAt: now, AdapterRevision: 2,
		Sources:           []model.SessionSourceFile{src},
		Warnings:          []model.ParseWarning{warn, warn}, // aggregate
		HasReplayableBody: true,
	})
	if degraded.State != model.RecordDegraded {
		t.Fatalf("state=%s", degraded.State)
	}
	if len(degraded.Warnings) != 1 || degraded.Warnings[0].Count != 6 {
		t.Fatalf("warnings aggregated: %+v", degraded.Warnings)
	}
	if degraded.WarningSummary.Total != 6 {
		t.Fatalf("summary total=%d", degraded.WarningSummary.Total)
	}
	if errs := Validate(degraded); len(errs) != 0 {
		t.Fatalf("validate degraded: %v", errs)
	}
}

func TestBuildOverrides(t *testing.T) {
	now := time.Now().UTC()
	ms := now.Add(-time.Hour)
	p := Build(Input{
		StateOverride:     model.RecordSourceMissing,
		ReasonCode:        "source_missing",
		CapturedAt:        now,
		AdapterRevision:   1,
		MissingSince:      &ms,
		HasReplayableBody: false,
		Sources: []model.SessionSourceFile{{
			Role: model.SourceRolePrimaryTranscript, Path: "/gone.jsonl", State: model.SourceMissing,
		}},
	})
	if p.State != model.RecordSourceMissing {
		t.Fatalf("state=%s", p.State)
	}
	if p.MissingSince == nil {
		t.Fatal("missing_since required")
	}
	if errs := Validate(p); len(errs) != 0 {
		t.Fatalf("validate: %v", errs)
	}

	unsup := Build(Input{
		StateOverride:   model.RecordParserUnsupported,
		CapturedAt:      now,
		AdapterRevision: 1,
		Sources: []model.SessionSourceFile{{
			Role: model.SourceRolePrimaryTranscript, Path: "/x", State: model.SourceUnsupported,
		}},
	})
	if unsup.State != model.RecordParserUnsupported {
		t.Fatalf("state=%s", unsup.State)
	}
}

func TestAggregateWarningsDedupesImpacts(t *testing.T) {
	a := Warning("malformed_record_skipped", "warning", true, []string{"replay", "tools", "replay"}, "primary_transcript", nil, 1)
	b := Warning("malformed_record_skipped", "warning", true, []string{"tools", "replay"}, "primary_transcript", nil, 2)
	out := AggregateWarnings([]model.ParseWarning{a, b})
	if len(out) != 1 || out[0].Count != 3 {
		t.Fatalf("got %+v", out)
	}
	if len(out[0].Impacts) != 2 {
		t.Fatalf("impacts: %v", out[0].Impacts)
	}
}

func TestNormalizeSourcesDropsEmptyAndDedupes(t *testing.T) {
	out := NormalizeSources([]model.SessionSourceFile{
		{Role: "primary_transcript", Path: "/a", State: model.SourcePresent},
		{Role: "primary_transcript", Path: "/a", State: model.SourcePresent},
		{Role: "metadata", Path: "  ", State: model.SourcePresent},
		{Role: "events", Path: "/b", State: model.SourceMissing},
	})
	if len(out) != 2 {
		t.Fatalf("len=%d %+v", len(out), out)
	}
}

func TestValidateRejectsCompleteWithAffects(t *testing.T) {
	p := model.SessionProvenance{
		State: model.RecordComplete, AdapterRevision: 1, CapturedAt: time.Now().UTC(),
		Warnings: []model.ParseWarning{{
			Code: "x", Severity: "warning", AffectsCompleteness: true, Count: 1,
		}},
	}
	errs := Validate(p)
	if len(errs) == 0 {
		t.Fatal("expected errors")
	}
}

func TestValidateReplayable(t *testing.T) {
	p := model.SessionProvenance{State: model.RecordSourceMissing}
	if errs := ValidateReplayable(p, true); len(errs) == 0 {
		t.Fatal("expected body conflict")
	}
	if errs := ValidateReplayable(p, false); len(errs) != 0 {
		t.Fatalf("unexpected: %v", errs)
	}
}

func ptrInt64(v int64) *int64 { return &v }
