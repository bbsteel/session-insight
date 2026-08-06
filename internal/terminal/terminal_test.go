package terminal

import (
	"reflect"
	"strings"
	"testing"
)

func TestLinuxTerminalArgsPreserveStructuredArguments(t *testing.T) {
	command := Command{Executable: "/usr/bin/codex", Args: []string{"resume", "id with spaces"}, CWD: "/tmp/project"}
	got, _, err := linuxTerminalArgs("gnome-terminal", command)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--tab", "--working-directory=/tmp/project", "--", "/usr/bin/codex", "resume", "id with spaces"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args=%q want %q", got, want)
	}
}

func TestFormatPOSIXCommandQuotesCWDAndArguments(t *testing.T) {
	got := formatPOSIXCommand("/opt/My Agent/bin", []string{"resume", "it's-id"})
	if got != `'/opt/My Agent/bin' 'resume' 'it'"'"'s-id'` {
		t.Fatalf("%q", got)
	}
	full := "cd " + quotePOSIX("/tmp/a b") + " && " + got
	if !strings.Contains(full, "cd '/tmp/a b'") {
		t.Fatal(full)
	}
}

func TestQuoteAppleScript(t *testing.T) {
	got := quoteAppleScript("say \"hello\"\\next\nline")
	if got != `say \"hello\"\\next\nline` {
		t.Fatalf("quoteAppleScript() = %q", got)
	}
}

func TestTerminalIdentityIsConservative(t *testing.T) {
	tests := map[string]string{
		"konsole":               "Konsole",
		"gnome-terminal-server": "GNOME Terminal",
		"wezterm-gui":           "WezTerm",
	}
	for executable, want := range tests {
		_, got, ok := terminalIdentity(executable)
		if !ok || got != want {
			t.Fatalf("terminalIdentity(%q) = %q, %v", executable, got, ok)
		}
	}
	if _, _, ok := terminalIdentity("bash"); ok {
		t.Fatal("shells must not be reported as terminal instances")
	}
}

func TestNewKonsoleSessionRequiresOneUnambiguousTab(t *testing.T) {
	before := []konsoleSnapshot{{service: "org.kde.konsole", windowID: "/Windows/1", sessions: map[string]bool{"1": true}}}
	after := []konsoleSnapshot{{service: "org.kde.konsole", windowID: "/Windows/1", sessions: map[string]bool{"1": true, "2": true}}}
	service, window, tab, ok := newKonsoleSession(before, after)
	if !ok || service != "org.kde.konsole" || window != "/Windows/1" || tab != "2" {
		t.Fatalf("%q %q %q %v", service, window, tab, ok)
	}
	after[0].sessions["3"] = true
	if _, _, _, ok := newKonsoleSession(before, after); ok {
		t.Fatal("ambiguous concurrent tabs must not be reported exact")
	}
}
