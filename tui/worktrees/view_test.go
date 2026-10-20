package worktrees

import (
	"errors"
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

// A first load that failed leaves nothing on the list, and that is not the
// same as having no worktrees: the line says so and names the retry key.
func TestFailedFirstLoadSaysHowToRetry(t *testing.T) {
	got := (ListInput{RefreshErr: errors.New("permission denied")}).RenderList(10)
	if !strings.Contains(got, "could not load worktrees — r retries") || strings.Contains(got, "w in Past Sessions") {
		t.Fatalf("RenderList = %q, want the load failure and its retry key", got)
	}
}

func TestEmptyWorktreeListSaysHowToMakeOne(t *testing.T) {
	if got := (ListInput{}).RenderList(10); !strings.Contains(got, "w in Past Sessions") {
		t.Fatalf("RenderList = %q, want it to say how a worktree is created", got)
	}
}
