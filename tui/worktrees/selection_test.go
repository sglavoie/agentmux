package worktrees

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/worktree"
)

// TestMatchesFilterIsWhitespaceAND locks in the filter contract: every token
// must match somewhere, matching is case-insensitive, and state labels are
// part of the haystack so "dirty" or "missing" narrow the list.
func TestMatchesFilterIsWhitespaceAND(t *testing.T) {
	entry := validView()
	entry.Dirty = true

	for _, query := range []string{"", "   ", "api", "API DIRTY", "claude feature", "wt-clean"} {
		if !MatchesFilter(entry, query) {
			t.Errorf("query %q should match %+v", query, entry.ID)
		}
	}
	for _, query := range []string{"api missing", "nomatch", "api locked"} {
		if MatchesFilter(entry, query) {
			t.Errorf("query %q should not match %s", query, entry.ID)
		}
	}
}

// TestViewKeyPairsIDAndPath confirms two worktrees that share an ID but sit at
// different paths are distinct identities, which is what keeps SortViews from
// mapping a sorted entry back onto the wrong view.
func TestViewKeyPairsIDAndPath(t *testing.T) {
	a := validView()
	b := validView()
	b.Path = "/tmp/wt/api-2"
	if viewKey(a) == viewKey(b) {
		t.Fatalf("same key for distinct paths: %q", viewKey(a))
	}
	if !strings.Contains(viewKey(a), a.ID) || !strings.Contains(viewKey(a), a.Path) {
		t.Errorf("viewKey = %q, want both ID and path", viewKey(a))
	}
}

// TestSortViewsFollowsDurableEntryOrdering checks that ordering is delegated
// to worktree.SortEntries rather than re-derived here, that the input slice is
// not mutated, and that a single view is copied rather than aliased.
func TestSortViewsFollowsDurableEntryOrdering(t *testing.T) {
	clean := validView()
	missing := validView()
	missing.ID = "wt-missing"
	missing.Path = "/tmp/wt/missing"
	missing.Classification = worktree.ClassificationMissing
	missing.Exists = false

	input := []actions.WorktreeView{clean, missing}
	got := SortViews(input)
	if len(got) != 2 {
		t.Fatalf("SortViews len = %d, want 2", len(got))
	}

	entries := []worktree.Entry{clean.Entry, missing.Entry}
	worktree.SortEntries(entries)
	for i := range entries {
		if got[i].ID != entries[i].ID {
			t.Fatalf("SortViews[%d] = %s, want %s (durable order)", i, got[i].ID, entries[i].ID)
		}
	}
	if input[0].ID != clean.ID {
		t.Errorf("SortViews mutated its input: %s", input[0].ID)
	}

	single := SortViews([]actions.WorktreeView{clean})
	if len(single) != 1 || single[0].ID != clean.ID {
		t.Fatalf("single-view sort = %+v", single)
	}
	single[0].ID = "mutated"
	if clean.ID == "mutated" {
		t.Error("single-view sort aliased its input")
	}
}

// TestMutationSummaryReportsDurableSteps covers the three shapes the toast
// can take: a recorded mutation lists the steps it performed, a mutation that
// performed none says so, and a result with no mutation record still reports
// that durable state changed.
func TestMutationSummaryReportsDurableSteps(t *testing.T) {
	full := actions.Result{Data: map[string]any{"mutation": worktree.MutationResult{
		CheckoutRemoved:   true,
		BranchDeleted:     true,
		MetadataForgotten: true,
	}}}
	if got, want := MutationSummary(full), "checkout removed, branch deleted, metadata forgotten"; got != want {
		t.Errorf("MutationSummary(full) = %q, want %q", got, want)
	}

	none := actions.Result{Data: map[string]any{"mutation": worktree.MutationResult{}}}
	if got, want := MutationSummary(none), "no durable step reported"; got != want {
		t.Errorf("MutationSummary(empty) = %q, want %q", got, want)
	}

	if got, want := MutationSummary(actions.Result{}), "durable state changed"; got != want {
		t.Errorf("MutationSummary(absent) = %q, want %q", got, want)
	}
}
