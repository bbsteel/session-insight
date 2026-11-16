package home

import (
	"testing"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

func TestCodeCoverageSeparatesMissingLinesFromUntimedChanges(t *testing.T) {
	at := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	outside := at.AddDate(0, 0, -10)
	zero, additions, deletions := 0, 7, 2
	envelope := model.SessionGitEvidenceEnvelope{Repositories: []model.SessionGitEvidence{{Files: []model.GitFileChange{
		{DisplayPath: "binary", Binary: true, Evidence: []model.GitEvidenceLink{{RecordedAt: &at}}},
		{DisplayPath: "partial", Additions: &additions, Evidence: []model.GitEvidenceLink{{RecordedAt: &at}}},
		{DisplayPath: "measured-zero", Additions: &zero, Deletions: &zero, Evidence: []model.GitEvidenceLink{{RecordedAt: &at}}},
		{DisplayPath: "measured", Additions: &additions, Deletions: &deletions, Evidence: []model.GitEvidenceLink{{RecordedAt: &at}}},
		{DisplayPath: "old-binary", Evidence: []model.GitEvidenceLink{{RecordedAt: &outside}}},
		{DisplayPath: "untimed"},
	}}}}
	changes, untimed := CodeFromEnvelope(envelope)
	if len(changes) != 5 || untimed != 1 {
		t.Fatalf("changes = %+v, untimed = %d", changes, untimed)
	}
	parent := SessionFact{ID: "root", UpdatedAt: at}
	AbsorbChild(&parent, SessionFact{Code: changes, UntimedCode: untimed})
	report := Aggregate([]SessionFact{parent}, Query{WindowDays: 7, Now: at, Location: time.UTC}, at)
	if report.Coverage.MissingCodeLineChanges != 2 || report.Coverage.UntimedCodeChanges != 1 {
		t.Fatalf("coverage = %+v", report.Coverage)
	}
	if report.Summary.CodeFiles != 4 || report.Summary.Additions != 7 || report.Summary.Deletions != 2 || report.Coverage.CodeSessions != 1 {
		t.Fatalf("summary = %+v, coverage = %+v", report.Summary, report.Coverage)
	}
}
