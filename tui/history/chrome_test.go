package history

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

// TestHistoryListsUseNormalizedRenderer is this package's copy of the root
// TUI's source-level selection guard, which the past-session results list was
// covered by before it moved here. New semantic row styling may be composed
// freely, but selection must always pass through renderSelectedRow instead of
// directly nesting the reverse style around it.
func TestHistoryListsUseNormalizedRenderer(t *testing.T) {
	for _, name := range []string{"search.go", "viewer.go"} {
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
