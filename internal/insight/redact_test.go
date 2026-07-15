package insight

import (
	"strings"
	"testing"
)

func TestRedactScrubsKnownSecrets(t *testing.T) {
	b := Bundle{
		SchemaVersion: 1,
		Facts: []EvidenceFact{
			{ID: "turn:0", Kind: "turn", Statement: "调用 openai，key sk-ABCDEF0123456789ghij 报错", Source: "s"},
			{ID: "turn:1", Kind: "turn", Statement: "Authorization: Bearer abcdefghijklmnop 被拒绝", Source: "s"},
			{ID: "turn:2", Kind: "turn", Statement: "联系 user@example.com，路径 /home/deck/secret", Source: "s"},
			{ID: "turn:3", Kind: "turn", Statement: "普通日志，无敏感信息", Source: "s"},
		},
	}
	red, stats := Redact(b)

	joined := ""
	for _, f := range red.Facts {
		joined += f.Statement + "\n"
	}
	for _, leaked := range []string{"sk-ABCDEF0123456789ghij", "user@example.com", "/home/deck/secret", "Bearer abcdefghijklmnop"} {
		if strings.Contains(joined, leaked) {
			t.Errorf("secret leaked after redaction: %q\n%s", leaked, joined)
		}
	}
	if stats.Secrets == 0 || stats.Emails == 0 || stats.HomePaths == 0 {
		t.Errorf("redaction stats undercount: %+v", stats)
	}
	// Original bundle must be untouched (Redact returns a copy).
	if !strings.Contains(b.Facts[0].Statement, "sk-ABCDEF") {
		t.Error("Redact mutated its input")
	}
	// Benign text passes through unchanged.
	if red.Facts[3].Statement != "普通日志，无敏感信息" {
		t.Errorf("benign text altered: %q", red.Facts[3].Statement)
	}
}

func TestFingerprintStability(t *testing.T) {
	b := Bundle{
		SchemaVersion: 1,
		Findings:      []FindingBrief{{Code: "tool_loop"}},
		Facts:         []EvidenceFact{{ID: "turn:2", Statement: "x"}},
	}
	fp1 := SourceFingerprint(b)
	fp2 := SourceFingerprint(b)
	if fp1 != fp2 {
		t.Error("fingerprint not stable for identical input")
	}
	b2 := b
	b2.Facts = []EvidenceFact{{ID: "turn:2", Statement: "y"}} // changed evidence
	if SourceFingerprint(b2) == fp1 {
		t.Error("fingerprint must change when evidence changes")
	}
}
