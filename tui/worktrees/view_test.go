package worktrees

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This source-level guard mirrors the root package's
// TestSelectableListFamiliesUseNormalizedRenderer for the worktree list that
// moved here. New semantic row styling may be composed freely, but selection
// must always pass through renderSelectedRow instead of directly nesting the
// reverse style around it.
func TestWorktreeListUsesNormalizedRenderer(t *testing.T) {
	const name = "view.go"
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
}
