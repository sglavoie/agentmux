// Package worktrees owns the TUI's managed-worktree selection and display
// domain: the one-line row a worktree renders as, the state badges that say
// what kind of worktree it is, the compact age its creation time reads as,
// the filter query that decides whether it stays on screen, the stable
// ordering the list is kept in, and the sentences a completed mutation is
// summarized with.
//
// The package holds worktree vocabulary and pure projections only. It knows
// nothing about the dashboard Model, performs no Git or filesystem work of
// its own, and must not import the root tui package: deciding what a
// keystroke does to the worktree cursor, and running the actions it dispatches,
// stays with the model that owns them.
package worktrees

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/sglavoie/agentmux/tui/layout"
)

// formatRow renders one worktree as a fixed-column row: project, branch,
// profile/harness identity, age, and state badges, truncated to width. Empty
// fields fall back to a parenthesized placeholder so the columns never
// collapse and misalign the rows below.
func formatRow(entry actions.WorktreeView, width int) string {
	width = layout.WrapWidth(width)
	project := strings.TrimSpace(entry.ProjectLabel)
	if project == "" {
		project = "(unknown project)"
	}
	branch := strings.TrimSpace(entry.Branch)
	if branch == "" {
		branch = "(no branch)"
	}
	identity := entry.Profile
	if entry.Harness != "" {
		if identity != "" {
			identity += "/"
		}
		identity += entry.Harness
	}
	if identity == "" {
		identity = "unknown profile"
	}
	age := CompactAge(entry.CreatedAt, time.Now())
	badges := BadgeText(entry)
	row := "  " + column(project, 18) + "  " + column(branch, 24) + "  " + column(identity, 18) + "  " + layout.PadLeft(age, 6) + "  " + badges
	return layout.StyledTruncate(row, width, "…")
}

// column fits s to exactly width display cells, truncating a long value
// instead of letting it push every later column right, and measuring by
// display cells so an emoji title lines up with a plain one.
func column(s string, width int) string {
	return layout.PadRight(layout.Truncate(s, width, "…"), width)
}

// CompactAge renders how long ago created was in the six columns the age
// field budgets: "now", minutes, hours, then days. A zero time reads "?"
// rather than an absurd duration measured from the Go epoch.
func CompactAge(created, now time.Time) string {
	if created.IsZero() {
		return "?"
	}
	d := now.Sub(created)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// BadgeText renders the state labels as bracketed badges. A valid
// classification is dropped because it is the unremarkable case, and a
// condition the classification already names ("missing" after missing,
// "invalid" after metadata_invalid or git_unavailable) is dropped so the
// rightmost, first-truncated column is not spent saying it twice. A worktree
// left with nothing to say reads "[clean]" rather than an empty column.
func BadgeText(entry actions.WorktreeView) string {
	labels := StateLabels(entry)
	if len(labels) > 0 && labels[0] == string(worktree.ClassificationValid) {
		labels = labels[1:]
	} else if len(labels) > 1 && restatesClassification(worktree.Classification(labels[0]), labels[1]) {
		labels = append(labels[:1:1], labels[2:]...)
	}
	if len(labels) == 0 {
		labels = []string{"clean"}
	}
	return "[" + strings.Join(labels, "][") + "]"
}

func restatesClassification(classification worktree.Classification, label string) bool {
	switch classification {
	case worktree.ClassificationMissing:
		return label == "missing"
	case worktree.ClassificationMetadataInvalid, worktree.ClassificationGitUnavailable:
		return label == "invalid"
	}
	return false
}

// StateLabels returns the worktree's classification followed by every
// condition worth surfacing: whether it is missing or invalid, in live or
// looping use, dirty or clean, locked, merged into its source, or carrying
// warnings. The first label is always the classification, which is what lets
// BadgeText drop it and callers read it back positionally.
func StateLabels(entry actions.WorktreeView) []string {
	labels := []string{string(entry.Classification)}
	switch {
	case !entry.Exists || entry.Classification == worktree.ClassificationMissing:
		labels = append(labels, "missing")
	case entry.Classification == worktree.ClassificationMetadataInvalid ||
		entry.Classification == worktree.ClassificationGitUnavailable ||
		!entry.GitAvailable || !entry.StatusAvailable:
		labels = append(labels, "invalid")
	}
	if entry.Usage.Live || len(entry.LiveSessions) > 0 {
		labels = append(labels, "live")
	}
	if entry.Usage.Looping || entry.LoopCount > 0 {
		labels = append(labels, "looping")
	}
	if entry.Dirty {
		labels = append(labels, "dirty")
	} else if entry.Exists && entry.StatusAvailable {
		labels = append(labels, "clean")
	}
	if entry.Locked {
		labels = append(labels, "locked")
	}
	if entry.ReachableFromSource != nil && *entry.ReachableFromSource {
		labels = append(labels, "merged")
	}
	if len(entry.Warnings) > 0 {
		labels = append(labels, "warning")
	}
	return labels
}

// SourceLabel names the branch a worktree was cut from, for the confirmation
// prompt that explains what a branch deletion is checked against. A worktree
// with no recorded source reads "(unknown)" rather than an empty quote.
func SourceLabel(entry actions.WorktreeView) string {
	if strings.TrimSpace(entry.SourceBranch) == "" {
		return "(unknown)"
	}
	return entry.SourceBranch
}
