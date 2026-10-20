package main

import (
	"bufio"
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/kittykeys"
)

func TestAgentMuxConf_DisablesBellMarkersAndAudioBell(t *testing.T) {
	data, err := os.ReadFile("agent-mux.conf")
	if err != nil {
		t.Fatalf("ReadFile(agent-mux.conf): %v", err)
	}

	text := string(data)
	for _, want := range []string{
		"enable_tab_bar_bell_markers no",
		"enable_audio_bell no",
	} {
		if !hasActiveConfigLine(text, want) {
			t.Fatalf("agent-mux.conf missing active line %q", want)
		}
	}
}

func TestReadmeDocumentsFocusNextBinding(t *testing.T) {
	conf, err := os.ReadFile("agent-mux.conf")
	if err != nil {
		t.Fatalf("ReadFile(agent-mux.conf): %v", err)
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("ReadFile(README.md): %v", err)
	}

	const binding = "map f4 launch --type=background --allow-remote-control amx focus-next --window-id @active-kitty-window-id"
	if !hasActiveConfigLine(string(conf), binding) {
		t.Fatalf("agent-mux.conf missing active focus-next binding %q", binding)
	}

	const documented = "`F4` for the next attention tab"
	if !strings.Contains(string(readme), documented) {
		t.Fatalf("README.md missing documented focus-next binding %q", documented)
	}
}

func TestDashboardBindingPassesActiveWindowID(t *testing.T) {
	conf, err := os.ReadFile("agent-mux.conf")
	if err != nil {
		t.Fatalf("ReadFile(agent-mux.conf): %v", err)
	}

	const binding = "map ctrl+shift+d  launch --type=overlay --allow-remote-control amx --window-id @active-kitty-window-id"
	if !hasActiveConfigLine(string(conf), binding) {
		t.Fatalf("agent-mux.conf missing dashboard source-window binding %q", binding)
	}
}

// The snippet an operator actually includes and the catalog the F1 reference is
// drawn from are two copies of the same binding, so the shipped line is asserted
// to be exactly the one the catalog generates rather than merely similar to it.
func TestAgentMuxConfShipsTheOverviewBindingTheCatalogGenerates(t *testing.T) {
	conf, err := os.ReadFile("agent-mux.conf")
	if err != nil {
		t.Fatalf("ReadFile(agent-mux.conf): %v", err)
	}

	var overview kittykeys.Action
	for _, action := range kittykeys.Actions {
		if action.Key == "f2" {
			overview = action
		}
	}
	if overview.Key == "" {
		t.Fatal("the shared binding catalog has no f2 overview binding")
	}

	const binding = "map f2 launch --type=overlay --allow-remote-control amx overview --window-id @active-kitty-window-id"
	if got := overview.ConfigLine(); got != binding {
		t.Fatalf("catalog f2 ConfigLine() = %q, want %q", got, binding)
	}
	if !hasActiveConfigLine(string(conf), binding) {
		t.Fatalf("agent-mux.conf missing active overview binding %q", binding)
	}

	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("ReadFile(README.md): %v", err)
	}
	const documented = "`F2` for the live session overview"
	if !strings.Contains(string(readme), documented) {
		t.Fatalf("README.md missing documented overview binding %q", documented)
	}
}

func TestAgentMuxConfContainsIconPickerBinding(t *testing.T) {
	conf, err := os.ReadFile("agent-mux.conf")
	if err != nil {
		t.Fatalf("ReadFile(agent-mux.conf): %v", err)
	}

	const binding = "map f5 launch --type=overlay --allow-remote-control amx icon-picker"
	if !hasActiveConfigLine(string(conf), binding) {
		t.Fatalf("agent-mux.conf missing active icon-picker binding %q", binding)
	}
}

// The 🔁 marker stays passive and the cockpit is an intervention surface, not a
// loop runner. That boundary is the thing an operator most easily assumes wrong,
// so the guide has to state it and this pins that it still does.
func TestUsageGuideDocumentsTheLoopMarkerAndCockpitBoundary(t *testing.T) {
	guide, err := os.ReadFile("docs/usage.md")
	if err != nil {
		t.Fatalf("ReadFile(docs/usage.md): %v", err)
	}

	for _, documented := range []string{
		"Rows marked with `🔁` have at least one active `agent-session-loop` run",
		"`run_finished_at` in `state.json`",
		"`AgentSessionLoopFinished` hook",
		"never starts, schedules, or tails a loop",
		"never retries a mutation on your behalf.",
	} {
		if !strings.Contains(string(guide), documented) {
			t.Fatalf("docs/usage.md missing documented text %q", documented)
		}
	}
}

func hasActiveConfigLine(text, want string) bool {
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == want {
			return true
		}
	}
	return false
}

func TestAgentMuxConfShipsThePastSessionsBindingTheCatalogGenerates(t *testing.T) {
	conf, err := os.ReadFile("agent-mux.conf")
	if err != nil {
		t.Fatalf("ReadFile(agent-mux.conf): %v", err)
	}
	const binding = "map ctrl+shift+y launch --type=overlay --allow-remote-control amx past-sessions"
	for _, action := range kittykeys.Actions {
		if action.Key != "ctrl+shift+y" {
			continue
		}
		if got := action.ConfigLine(); got != binding {
			t.Fatalf("catalog ctrl+shift+y ConfigLine() = %q, want %q", got, binding)
		}
		if !hasActiveConfigLine(string(conf), binding) {
			t.Fatalf("agent-mux.conf missing active Past Sessions binding %q", binding)
		}
		return
	}
	t.Fatal("the shared binding catalog has no ctrl+shift+y Past Sessions binding")
}
