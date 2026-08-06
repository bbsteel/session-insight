package db

import (
	"testing"
	"time"
)

func TestTerminalBindingRoundTrip(t *testing.T) {
	database, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	launched := time.Now().UTC().Truncate(time.Microsecond)
	want := TerminalBindingRecord{
		AgentType: "codex", SessionID: "s1", TerminalID: "konsole", TerminalName: "Konsole",
		InstanceID: "org.kde.konsole", WindowID: "/Windows/1", TabID: "7",
		TerminalPID: 123, Confidence: "exact", Focusable: true, State: "launching", LaunchedAt: launched,
	}
	if err := database.UpsertTerminalBinding(want); err != nil {
		t.Fatal(err)
	}
	got, ok, err := database.GetTerminalBinding("codex", "s1")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.TabID != "7" || !got.Focusable || !got.LaunchedAt.Equal(launched) {
		t.Fatalf("%+v", got)
	}

	want.State = "active"
	want.AgentPID = 456
	want.LastVerifiedAt = launched.Add(time.Second)
	if err := database.UpsertTerminalBinding(want); err != nil {
		t.Fatal(err)
	}
	got, ok, err = database.GetTerminalBinding("codex", "s1")
	if err != nil || !ok || got.State != "active" || got.AgentPID != 456 {
		t.Fatalf("ok=%v err=%v binding=%+v", ok, err, got)
	}
}
