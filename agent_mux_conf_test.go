package main

import (
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
		if !strings.Contains(text, want) {
			t.Fatalf("agent-mux.conf missing %q", want)
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
	if !strings.Contains(string(conf), binding) {
		t.Fatalf("agent-mux.conf missing focus-next binding %q", binding)
	}

	const documented = "`F4` for the next attention tab"
	if !strings.Contains(string(readme), documented) {
		t.Fatalf("README.md missing documented focus-next binding %q", documented)
	}
}
