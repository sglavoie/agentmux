package worktrees

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/worktree"
)

// SortViews orders views by the durable entry ordering worktree.SortEntries
// defines. Views are bucketed by identity first so the sorted entries can be
// mapped back to the views they came from without re-deriving anything the
// entry ordering already decided.
func SortViews(views []actions.WorktreeView) []actions.WorktreeView {
	if len(views) < 2 {
		return append([]actions.WorktreeView(nil), views...)
	}
	byKey := make(map[string][]actions.WorktreeView, len(views))
	entries := make([]worktree.Entry, 0, len(views))
	for _, view := range views {
		key := viewKey(view)
		byKey[key] = append(byKey[key], view)
		entries = append(entries, view.Entry)
	}
	worktree.SortEntries(entries)
	ordered := make([]actions.WorktreeView, 0, len(views))
	for _, entry := range entries {
		key := entry.ID + "\x00" + entry.Path
		bucket := byKey[key]
		ordered = append(ordered, bucket[0])
		byKey[key] = bucket[1:]
	}
	return ordered
}

// viewKey is the identity a view is bucketed and matched by: its worktree ID
// paired with its checkout path, joined by a byte that cannot occur in either.
func viewKey(view actions.WorktreeView) string {
	return view.ID + "\x00" + view.Path
}

// MatchesFilter reports whether entry survives the filter query. Every
// whitespace-separated token must appear somewhere in the entry's
// case-folded identity, location, profile, classification, or state labels,
// so tokens narrow rather than widen the list.
func MatchesFilter(entry actions.WorktreeView, query string) bool {
	state := strings.Join(StateLabels(entry), " ")
	haystack := strings.ToLower(strings.Join([]string{
		entry.ID, entry.ProjectLabel, entry.Branch, entry.Path,
		entry.Profile, entry.Harness, string(entry.Classification), state,
	}, "\n"))
	for _, token := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

// MutationSummary describes which durable steps a completed worktree mutation
// actually performed, for the toast and the partial-failure notice. A result
// carrying no mutation record still reports that durable state changed,
// because the action reached the point of reporting one.
func MutationSummary(result actions.Result) string {
	mutation, ok := result.Data["mutation"].(worktree.MutationResult)
	if !ok {
		return "durable state changed"
	}
	var steps []string
	if mutation.CheckoutRemoved {
		steps = append(steps, "checkout removed")
	}
	if mutation.BranchDeleted {
		steps = append(steps, "branch deleted")
	}
	if mutation.MetadataForgotten {
		steps = append(steps, "metadata forgotten")
	}
	if len(steps) == 0 {
		return "no durable step reported"
	}
	return strings.Join(steps, ", ")
}
