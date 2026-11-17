package chrys

import (
	"encoding/json"
	"testing"
)

func TestInterruptionPreservesRecordedTime(t *testing.T) {
	for _, test := range []struct{ name, sourceTime, wantTime string }{
		{"recorded", "2026-04-01T12:00:00Z", "2026-04-01T12:00:00Z"},
		{"offset", "2026-04-01T20:00:00+08:00", "2026-04-01T20:00:00+08:00"},
		{"missing", "", ""},
		{"invalid", "not-a-time", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `{"state":{"messages":[{"role":"user","contents":[{"type":"text","text":"Synthetic interruption fixture"}]},{"additional_properties":{"_chrys_kind":"interrupted","_interrupted_by":"user","_chrys_created_at":"` + test.sourceTime + `"}}]}}`
			var session sessionFile
			if err := json.Unmarshal([]byte(source), &session); err != nil {
				t.Fatal(err)
			}
			turns := buildTurns(&session)
			if len(turns) != 1 || len(turns[0].Events) != 1 {
				t.Fatalf("turns = %+v", turns)
			}
			event := turns[0].Events[0]
			if event.Type != "interrupted" || event.Timestamp != test.wantTime || event.Data["by"] != "user" {
				t.Fatalf("interruption = %+v", event)
			}
		})
	}
}
