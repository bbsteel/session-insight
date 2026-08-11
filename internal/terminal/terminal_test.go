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

func TestKonsoleServicePIDParsesOnlyPidSuffixedNames(t *testing.T) {
	pid, ok := konsoleServicePID("org.kde.konsole-16923")
	if !ok || pid != 16923 {
		t.Fatalf("%d %v", pid, ok)
	}
	for _, service := range []string{"org.kde.konsole", ":1.217", "org.kde.konsole-", "org.kde.konsole-abc"} {
		if _, ok := konsoleServicePID(service); ok {
			t.Fatalf("%s must not parse as a pid-suffixed service", service)
		}
	}
}

func TestPickKonsoleTargetPrefersBackendHostInstance(t *testing.T) {
	targets := []konsoleTarget{
		{service: "org.kde.konsole-10387", windowID: "/Windows/1", pid: 10387},
		{service: "org.kde.konsole-16923", windowID: "/Windows/1", pid: 16923},
		{service: "org.kde.konsole-16923", windowID: "/Windows/2", pid: 16923},
	}
	resolve := func(service string) int {
		if service == ":1.217" {
			return 16923
		}
		return 0
	}
	got, ok := pickKonsoleTarget(targets, ":1.217", "/Windows/2", resolve)
	if !ok || got.service != "org.kde.konsole-16923" || got.windowID != "/Windows/2" {
		t.Fatalf("%+v %v", got, ok)
	}
	// A preferred window that does not exist on the matched instance falls
	// back to that instance's first discovered window.
	got, ok = pickKonsoleTarget(targets, ":1.217", "/Windows/9", resolve)
	if !ok || got.service != "org.kde.konsole-16923" || got.windowID != "/Windows/1" {
		t.Fatalf("%+v %v", got, ok)
	}
}

func TestPickKonsoleTargetIgnoresStalePreference(t *testing.T) {
	targets := []konsoleTarget{{service: "org.kde.konsole-16923", windowID: "/Windows/1", pid: 16923}}
	resolve := func(string) int { return 99999 }
	got, ok := pickKonsoleTarget(targets, ":1.999", "/Windows/1", resolve)
	if !ok || got.service != "org.kde.konsole-16923" {
		t.Fatalf("stale preference must fall back to the first window: %+v %v", got, ok)
	}
	got, ok = pickKonsoleTarget(targets, "", "", resolve)
	if !ok || got.service != "org.kde.konsole-16923" {
		t.Fatalf("missing preference must fall back to the first window: %+v %v", got, ok)
	}
	if _, ok := pickKonsoleTarget(nil, ":1.217", "/Windows/1", resolve); ok {
		t.Fatal("no running Konsole means no tab target")
	}
}
