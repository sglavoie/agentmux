package tui

import (
	"strings"
	"testing"
)

func TestKeybindingsViewIncludesHarnessIconPicker(t *testing.T) {
	view := (keybindingsModel{}).View().Content
	for _, want := range []string{
		"Sessions",
		"f5",
		"set tab harness icon (overlay picker)",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("keybinding reference missing %q:\n%s", want, view)
		}
	}
}
