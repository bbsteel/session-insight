package insight

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bbsteel/session-insight/internal/analytics"
)

// testBundle builds a bundle with a known fact ("turn:2") and finding code.
func testBundle(t *testing.T) Bundle {
	t.Helper()
	detail := mkDetail()
	res := analytics.Compute(detail)
	b := BuildBundle(detail, res, nil)
	if !factIDsOf(b)["turn:2"] {
		t.Fatal("fixture must expose turn:2 fact")
	}
	return b
}

func validOutputJSON(evID, code string) string {
	o := Output{
		SchemaVersion: 1,
		Summary:       "审查—修复级联放大了调用",
		Insights: []Insight{{
			Title:        "级联",
			FindingCodes: []string{code},
			Confidence:   "medium",
			Cause: Cause{
				Statement: "串行审查修复", EpistemicStatus: "inferred", CausalStrength: "moderate",
				EvidenceIDs: []string{evID},
			},
			Impact: Impact{Statement: "调用增多", EvidenceIDs: []string{evID}},
		}},
		EvidenceGaps: []string{"缺少 provider input usage"},
	}
	data, _ := json.Marshal(o)
	return string(data)
}

func TestParseValidOutput(t *testing.T) {
	b := testBundle(t)
	raw := validOutputJSON("turn:2", "tool_loop")
	out, warnings, err := ParseAndValidate(raw, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Insights) != 1 {
		t.Fatalf("want 1 insight kept, got %d (warnings %v)", len(out.Insights), warnings)
	}
	md := RenderMarkdown(out, b)
	// Cited evidence must be expanded to its original statement inline.
	if !strings.Contains(md, "turn:2") || !strings.Contains(md, "修复完成") {
		t.Errorf("markdown must resolve cited evidence statement, got:\n%s", md)
	}
}

func TestParseDropsUnknownEvidenceID(t *testing.T) {
	b := testBundle(t)
	raw := validOutputJSON("turn:999", "tool_loop") // nonexistent id
	out, warnings, err := ParseAndValidate(raw, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Insights) != 0 {
		t.Errorf("insight citing nonexistent id must be dropped, got %d", len(out.Insights))
	}
	if !hasWarning(warnings, "不存在的 evidence_id") {
		t.Errorf("expected a dangling-citation warning, got %v", warnings)
	}
}

func TestParseInvalidEnumDropped(t *testing.T) {
	b := testBundle(t)
	raw := strings.Replace(validOutputJSON("turn:2", "tool_loop"), `"confidence":"medium"`, `"confidence":"very-high"`, 1)
	out, warnings, err := ParseAndValidate(raw, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Insights) != 0 {
		t.Error("insight with invalid confidence must be dropped")
	}
	if !hasWarning(warnings, "非法 confidence") {
		t.Errorf("expected invalid-confidence warning, got %v", warnings)
	}
}

func TestParseFiltersUnknownFindingCode(t *testing.T) {
	b := testBundle(t)
	raw := validOutputJSON("turn:2", "not_a_real_code")
	out, warnings, err := ParseAndValidate(raw, b)
	if err != nil {
		t.Fatal(err)
	}
	// The insight survives (evidence is valid) but the bogus code is filtered.
	if len(out.Insights) != 1 || len(out.Insights[0].FindingCodes) != 0 {
		t.Errorf("unknown finding_code must be filtered, insight=%+v", out.Insights)
	}
	if !hasWarning(warnings, "不在输入中") {
		t.Errorf("expected finding-code warning, got %v", warnings)
	}
}

func TestParseEmptyInsightsIsValid(t *testing.T) {
	b := testBundle(t)
	raw := `{"schema_version":1,"summary":"证据不足","insights":[],"evidence_gaps":["无 subagent 数据"]}`
	out, _, err := ParseAndValidate(raw, b)
	if err != nil {
		t.Fatalf("empty insights must be a valid success, got %v", err)
	}
	if len(out.Insights) != 0 || len(out.EvidenceGaps) == 0 {
		t.Errorf("expected empty insights with gaps, got %+v", out)
	}
	md := RenderMarkdown(out, b)
	if !strings.Contains(md, "证据不足") {
		t.Errorf("empty-insight markdown must explain, got:\n%s", md)
	}
}

func TestParseUnparseableFallsBack(t *testing.T) {
	b := testBundle(t)
	_, _, err := ParseAndValidate("这是一段自由文本，不是 JSON", b)
	if err != ErrUnparseable {
		t.Errorf("want ErrUnparseable, got %v", err)
	}
}

func TestParseStripsCodeFence(t *testing.T) {
	b := testBundle(t)
	raw := "```json\n" + validOutputJSON("turn:2", "tool_loop") + "\n```"
	out, _, err := ParseAndValidate(raw, b)
	if err != nil || len(out.Insights) != 1 {
		t.Errorf("fenced JSON should still parse, err=%v insights=%d", err, len(out.Insights))
	}
}

func TestParseSchemaVersionMismatch(t *testing.T) {
	b := testBundle(t)
	raw := `{"schema_version":99,"summary":"x","insights":[]}`
	if _, _, err := ParseAndValidate(raw, b); err == nil {
		t.Error("unsupported schema_version must error")
	}
}

func hasWarning(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
