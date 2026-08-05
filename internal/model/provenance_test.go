package model

import (
	"testing"
	"time"
)

func TestCompactRecordStatus(t *testing.T) {
	if CompactRecordStatus(nil) != nil {
		t.Fatal("nil provenance should compact to nil")
	}
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	p := &SessionProvenance{
		State:      RecordDegraded,
		CapturedAt: now,
		WarningSummary: WarningSummary{
			Total: 3,
		},
		Sources: []SessionSourceFile{{
			Role:  SourceRolePrimaryTranscript,
			Path:  "/secret/path.jsonl",
			State: SourcePresent,
		}},
	}
	c := CompactRecordStatus(p)
	if c == nil {
		t.Fatal("expected compact status")
	}
	if c.State != RecordDegraded || c.WarningCount != 3 {
		t.Fatalf("unexpected compact: %+v", c)
	}
	if c.CapturedAt != now {
		t.Fatalf("captured_at: got %v", c.CapturedAt)
	}
	// Compact must not expose paths — RecordStatus has no path field; assert shape.
	_ = c.State
}

func TestRecordStateConstants(t *testing.T) {
	// Stable wire values — changing these breaks API/i18n.
	want := map[RecordCompletenessState]string{
		RecordComplete:          "complete",
		RecordDegraded:          "degraded",
		RecordMetadataOnly:      "metadata_only",
		RecordSourceMissing:     "source_missing",
		RecordParserUnsupported: "parser_unsupported",
	}
	for k, v := range want {
		if string(k) != v {
			t.Fatalf("%v wire value %q != %q", k, k, v)
		}
	}
}
