package main

import (
	"bufio"
	"os"
	"strings"
	"testing"
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

func TestReadmeDocumentsDashboardTailBinding(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("ReadFile(README.md): %v", err)
	}

	const documented = "`t`: tail an active `agent-session-loop` run"
	if !strings.Contains(string(readme), documented) {
		t.Fatalf("README.md missing documented dashboard tail binding %q", documented)
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
