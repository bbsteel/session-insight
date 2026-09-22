package home

import "testing"

func TestUnfinishedReasonsUsesPositiveEvidenceOnly(t *testing.T) {
	cases := []struct {
		name    string
		signals Signals
		want    []string
	}{
		{name: "silence", signals: Signals{}, want: nil},
		{name: "live suppresses ending", signals: Signals{Live: true, TurnOpen: true, OpenTodos: true}, want: nil},
		{name: "quota", signals: Signals{Quota: true, TurnOpen: true}, want: []string{ReasonQuotaStop}},
		{name: "temporary exit", signals: Signals{UserInterrupt: true}, want: []string{ReasonTemporaryExit}},
		{name: "process exit", signals: Signals{TurnOpen: true}, want: []string{ReasonProcessExit}},
		{name: "normal close with todo", signals: Signals{OpenTodos: true}, want: []string{ReasonOpenTodo}},
		{name: "interrupt and todo", signals: Signals{UserInterrupt: true, OpenTodos: true, MissingShutdown: true}, want: []string{ReasonTemporaryExit, ReasonOpenTodo, ReasonMissingShutdown}},
		{name: "quota wins over interrupt flags", signals: Signals{Quota: true, UserInterrupt: true}, want: []string{ReasonQuotaStop}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := UnfinishedReasons(tc.signals)
			if len(got) != len(tc.want) {
				t.Fatalf("reasons = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("reasons = %v, want %v", got, tc.want)
				}
			}
		})
	}
}
