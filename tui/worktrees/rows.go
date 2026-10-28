package worktrees

import (
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
//
// The badges are what a row is triaged by, so a narrower terminal spends its
// columns on them first. Below wideRowWidth the identity, the column of least
// use for choosing a worktree, moves after the badges where truncation takes
// it first; below narrowRowWidth the age joins it there and the text columns
// shrink. Either way a row at the tier's narrowest width (60 at the least)
// keeps "[metadata_invalid][clean][warning]" whole.
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
	age := layout.PadLeft(layout.CompactAge(entry.CreatedAt, time.Now()), 6)
	badges := BadgeText(entry)
	var row string
	switch {
	case width >= wideRowWidth:
		row = "  " + column(project, 18) + "  " + column(branch, 24) + "  " + column(identity, 18) + "  " + age + "  " + badges
	case width >= narrowRowWidth:
		row = "  " + column(project, 14) + "  " + column(branch, 14) + "  " + age + "  " + badges + "  " + identity
	default:
		row = "  " + column(project, 9) + "  " + column(branch, 10) + "  " + badges + "  " + strings.TrimSpace(age) + "  " + identity
	}
	return layout.StyledTruncate(row, width, "…")
}

// wideRowWidth is the terminal width at which a row keeps every column in
// line; below it the identity trails the badges, and below narrowRowWidth the
// age does too.
const (
	wideRowWidth   = 110
	narrowRowWidth = 80
)

// column fits s to exactly width display cells, truncating a long value
// instead of letting it push every later column right, and measuring by
// display cells so an emoji title lines up with a plain one.
func column(s string, width int) string {
	return layout.PadRight(layout.Truncate(s, width, "…"), width)
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
	} else if entry.Ignored {
		labels = append(labels, "ignored")
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
