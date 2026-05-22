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
