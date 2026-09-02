package customization

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripAnsi removes ANSI CSI escape sequences from a Lipgloss-rendered
// string so tests can assert against the visible content.
func stripAnsi(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// TestOverlayHintRowsUseFooterBindings mirrors the root TUI's footer-binding
// assertion for the three standalone overlays this package owns: every hint
// row must render its key through the accent style rather than inlining a
// plain string, so the overlays stay visually consistent with the dashboard
// footers they were split from.
func TestOverlayHintRowsUseFooterBindings(t *testing.T) {
	cases := []struct {
		name string
		view string
		key  string
	}{
		{name: "color picker", view: newColorPickerModel(ColorContext{DefaultColor: "blue"}).View().Content, key: "j/k"},
		{name: "icon picker", view: (iconPickerModel{items: []iconPickerItem{{name: "codex", emoji: "📟"}}}).View().Content, key: "j/k"},
		{name: "keybinding reference", view: (keybindingsModel{}).View().Content, key: "j/k"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(tc.view, styleFooterKey.Render(tc.key)) {
				t.Fatalf("hint row does not accent key %q:\n%s", tc.key, tc.view)
			}
		})
	}
}

// TestOverlayListsUseNormalizedRenderer is this package's copy of the root
// TUI's source-level selection guard, which the two picker files were covered
// by before they moved here. New semantic row styling may be composed freely,
// but selection must always pass through renderSelectedRow instead of
// directly nesting the reverse style around it.
func TestOverlayListsUseNormalizedRenderer(t *testing.T) {
	for _, name := range []string{"color.go", "icon.go"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(".", name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			text := string(source)
			if !strings.Contains(text, "renderSelectedRow(") {
				t.Fatalf("%s no longer routes its selected rows through renderSelectedRow", name)
			}
			if strings.Contains(text, "styleSelected.Render(") {
				t.Fatalf("%s directly wraps a styled row with styleSelected", name)
			}
		})
	}
}
