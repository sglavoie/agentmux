package worktrees

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/sglavoie/agentmux/tui/layout"
)

func validView() actions.WorktreeView {
	return actions.WorktreeView{Entry: worktree.Entry{
		ID:              "wt-clean",
		Classification:  worktree.ClassificationValid,
		ProjectLabel:    "api",
		Path:            "/tmp/wt/api",
		Branch:          "feature/login",
		Profile:         "work",
		Harness:         "claude",
		Exists:          true,
		GitAvailable:    true,
		StatusAvailable: true,
	}}
}

// TestCompactAgeBuckets locks in the six-column age field: a zero creation
// time is unknown rather than ancient, a future timestamp clamps to "now",
// and each bucket boundary rolls over to the next coarser unit.
func TestCompactAgeBuckets(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		created time.Time
		want    string
	}{
		{name: "zero", created: time.Time{}, want: "?"},
		{name: "future", created: now.Add(time.Hour), want: "now"},
		{name: "seconds", created: now.Add(-30 * time.Second), want: "now"},
		{name: "minutes", created: now.Add(-90 * time.Second), want: "1m"},
		{name: "minute boundary", created: now.Add(-59 * time.Minute), want: "59m"},
		{name: "hours", created: now.Add(-90 * time.Minute), want: "1h"},
		{name: "hour boundary", created: now.Add(-23 * time.Hour), want: "23h"},
		{name: "days", created: now.Add(-49 * time.Hour), want: "2d"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := layout.CompactAge(tc.created, now); got != tc.want {
				t.Errorf("CompactAge(%v) = %q, want %q", tc.created, got, tc.want)
			}
		})
	}
}

// TestStateLabelsLeadWithClassification confirms the classification is always
// the first label — BadgeText and the root's attention ordering both read it
// back positionally — and that each condition appends its own label.
func TestStateLabelsLeadWithClassification(t *testing.T) {
	merged := true
	entry := validView()
	entry.Dirty = true
	entry.Locked = true
	entry.Usage.Live = true
	entry.Usage.Looping = true
	entry.ReachableFromSource = &merged
	entry.Warnings = []string{"stale lock"}

	got := StateLabels(entry)
	want := []string{"valid", "live", "looping", "dirty", "locked", "merged", "warning"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("StateLabels = %v, want %v", got, want)
	}

	missing := validView()
	missing.Exists = false
	if got := StateLabels(missing); len(got) < 2 || got[1] != "missing" {
		t.Errorf("absent checkout labels = %v, want missing second", got)
	}

	invalid := validView()
	invalid.StatusAvailable = false
	if got := StateLabels(invalid); len(got) < 2 || got[1] != "invalid" {
		t.Errorf("unavailable status labels = %v, want invalid second", got)
	}
}

// TestBadgeTextDropsValidAndFallsBackToClean covers the two shaping rules:
// the unremarkable "valid" classification never reaches the badge column, and
// a worktree with nothing else to report still renders a badge.
func TestBadgeTextDropsValidAndFallsBackToClean(t *testing.T) {
	entry := validView()
	if got := BadgeText(entry); got != "[clean]" {
		t.Errorf("BadgeText(valid clean) = %q, want [clean]", got)
	}

	entry.Dirty = true
	entry.Locked = true
	if got := BadgeText(entry); got != "[dirty][locked]" {
		t.Errorf("BadgeText(dirty locked) = %q, want [dirty][locked]", got)
	}

	entry = validView()
	entry.Classification = worktree.ClassificationMissing
	entry.Exists = false
	if got := BadgeText(entry); got != "[missing]" {
		t.Errorf("BadgeText(missing) = %q, want the classification once", got)
	}

	entry = validView()
	entry.Classification = worktree.ClassificationMetadataInvalid
	if got := BadgeText(entry); strings.Contains(got, "[invalid]") || !strings.HasPrefix(got, "[metadata_invalid]") {
		t.Errorf("BadgeText(metadata_invalid) = %q, want the classification without a restating [invalid]", got)
	}
}

// Long and wide values are fitted to their column, so the age and badge
// columns start at the same cell on every row.
func TestFormatRowKeepsColumnsAlignedForLongAndWideValues(t *testing.T) {
	plain := validView()
	plain.ProjectLabel, plain.Profile, plain.Harness = "api", "work", "claude"
	long := plain
	long.ProjectLabel, long.Profile = "🐱amx", "removed-profile-with-a-long-name"
	badgeAt := func(v actions.WorktreeView) int {
		row := FormatRow(v, 200)
		return layout.Width(row[:strings.Index(row, "[")])
	}
	if a, b := badgeAt(plain), badgeAt(long); a != b {
		t.Fatalf("badge column starts at %d and %d; want the same cell", a, b)
	}
}

// TestFormatRowPlaceholdersAndTruncation checks that empty columns keep the
// row aligned and that the row is truncated to the caller's width, including
// the pre-resize fallback that must not collapse to zero.
func TestFormatRowPlaceholdersAndTruncation(t *testing.T) {
	bare := actions.WorktreeView{Entry: worktree.Entry{ID: "wt-bare"}}
	row := FormatRow(bare, 160)
	for _, want := range []string{"(unknown project)", "(no branch)", "unknown profile", "?"} {
		if !strings.Contains(row, want) {
			t.Errorf("bare row missing %q: %q", want, row)
		}
	}

	long := validView()
	long.ProjectLabel = strings.Repeat("project-", 12)
	if got := len([]rune(FormatRow(long, 40))); got > 40 {
		t.Errorf("row rune count = %d, want <= 40", got)
	}
	if got := len([]rune(FormatRow(long, 0))); got == 0 || got > 120 {
		t.Errorf("zero-width row rune count = %d, want a bounded fallback", got)
	}
}

// TestSourceLabelFallsBackWhenUnrecorded keeps the confirmation prompt from
// quoting an empty branch name.
func TestSourceLabelFallsBackWhenUnrecorded(t *testing.T) {
	entry := validView()
	if got := SourceLabel(entry); got != "(unknown)" {
		t.Errorf("SourceLabel(unset) = %q, want (unknown)", got)
	}
	entry.SourceBranch = "   "
	if got := SourceLabel(entry); got != "(unknown)" {
		t.Errorf("SourceLabel(blank) = %q, want (unknown)", got)
	}
	entry.SourceBranch = "main"
	if got := SourceLabel(entry); got != "main" {
		t.Errorf("SourceLabel(main) = %q, want main", got)
	}
}
