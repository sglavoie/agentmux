package dashboard

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

// bandTestWidths are the widths every responsive assertion in this file is made
// at: one well inside compact, both sides of each boundary, and one well inside
// spacious. A preset that only misbehaves one cell from a boundary is exactly
// the failure a hand-picked "narrow / wide" pair misses.
var bandTestWidths = []int{36, 79, 80, 119, 120, 160}

func TestBandForBoundariesAndUnmeasuredWidth(t *testing.T) {
	for _, tc := range []struct {
		width int
		want  Band
	}{
		{1, BandCompact},
		{36, BandCompact},
		{79, BandCompact},
		{80, BandStandard},
		{119, BandStandard},
		{120, BandSpacious},
		{160, BandSpacious},
		// A width nothing has measured yet is not a narrow terminal; it is no
		// terminal. Classifying it as scarcity would make the first paint drop
		// fields it has no reason to drop.
		{0, BandStandard},
		{-4, BandStandard},
	} {
		if got := BandFor(tc.width); got != tc.want {
			t.Errorf("BandFor(%d) = %s, want %s", tc.width, got, tc.want)
		}
	}
}

// TestBandsNoLongerReachTheSessionRow is the negative half of the queue
// redesign, and it is a test rather than a comment because the regression it
// guards is silent: a future band-keyed branch inside the row would restore the
// step function the queue exists to remove, and every other assertion in the
// package would still pass. FormatRow takes no band at all now — the type system
// carries most of this — so what is left to pin is that the row's OUTPUT does not
// change in kind across a boundary, which TestQueueRowIsContinuousAcrossTheRetiredBandBoundaries
// asserts, and that the surviving callers are chrome rather than rows.
//
// The band remains a real classifier for the footer and the legend; this file
// keeps its boundary test above for them.
func TestBandsNoLongerReachTheSessionRow(t *testing.T) {
	q := QueueRow{Name: "api-gateway-service", Status: statusWorking, Elapsed: "0:42"}
	cols := ScanQueueColumns([]QueueRow{q})

	// Same tokens at every width that can pay for them: what a band used to decide
	// is now decided by the row's own budget, so two widths in different bands that
	// afford the same columns produce the same row.
	wide := stripAnsi(FormatRow(RowInput{Queue: q, Width: 160, Columns: cols}))
	standard := stripAnsi(FormatRow(RowInput{Queue: q, Width: 100, Columns: cols}))
	compact := stripAnsi(FormatRow(RowInput{Queue: q, Width: 60, Columns: cols}))
	for _, want := range []string{"api-gateway-service", statusWorking, "0:42"} {
		for name, row := range map[string]string{"spacious": wide, "standard": standard, "compact": compact} {
			if !strings.Contains(row, want) {
				t.Errorf("%s row dropped %q: %q", name, want, row)
			}
		}
	}
}

// ---- detail strip ----

func detailStripFixture() (ItemList, session.RunningSession) {
	s := session.RunningSession{
		WindowID:  7,
		Account:   "personal",
		Profile:   "personal",
		Harness:   "codex",
		Base:      "api",
		Project:   "api",
		Directory: "/home/user/projects/worktrees/api-feature-x",
		Branch:    "amx/feat-x",
		Dirty:     true,
		BusySince: time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC),
	}
	return BuildItemsFor(ViewUrgency, []session.RunningSession{s}, nil), s
}

// TestDetailStripIsAlwaysExactlyTwoClampedLines is the property every height
// budget that charges for the strip depends on. A strip that returned one line
// for a session with no branch would move the list's bottom edge on a cursor
// move, and the cursor arithmetic would be wrong for exactly one frame.
func TestDetailStripIsAlwaysExactlyTwoClampedLines(t *testing.T) {
	items, _ := detailStripFixture()
	item, _ := items.At(0)
	for _, width := range append(bandTestWidths, 12, 20) {
		for _, tc := range []struct {
			name string
			in   DetailStripInput
		}{
			{"session", DetailStripInput{Width: width, Items: items, Item: item, Selected: true}},
			{"no selection", DetailStripInput{Width: width, Items: items, Selected: false}},
			{"zero item", DetailStripInput{Width: width, Items: items, Selected: true}},
		} {
			lines := RenderDetailStrip(tc.in)
			if len(lines) != DetailStripLines {
				t.Fatalf("%s at width %d: %d lines, want %d", tc.name, width, len(lines), DetailStripLines)
			}
			for i, line := range lines {
				if w := layout.StyledWidth(line); w > width {
					t.Errorf("%s at width %d: line %d is %d cells: %q", tc.name, width, i, w, line)
				}
				if strings.Contains(line, "\n") {
					t.Errorf("%s at width %d: line %d contains a newline: %q", tc.name, width, i, line)
				}
				if !utf8.ValidString(line) {
					t.Errorf("%s at width %d: line %d is not valid UTF-8: %q", tc.name, width, i, line)
				}
			}
		}
	}
}

// TestSessionDetailStripCarriesWhatTheCompactRowDropped is the contract between
// the two: every field the compact band takes off the row has to be somewhere,
// and this is where.
func TestSessionDetailStripCarriesWhatTheCompactRowDropped(t *testing.T) {
	items, s := detailStripFixture()
	item, _ := items.At(0)
	lines := RenderDetailStrip(DetailStripInput{
		Width: 78, Items: items, Item: item, Selected: true,
		Watchdog: nil, Now: s.BusySince.Add(42 * time.Second),
	})
	first, second := stripAnsi(lines[0]), stripAnsi(lines[1])

	for _, want := range []string{"api", "personal", "codex", "0:42"} {
		if !strings.Contains(first, want) {
			t.Errorf("strip line 1 %q is missing %q", first, want)
		}
	}
	for _, want := range []string{"amx/feat-x*", "api-feature-x"} {
		if !strings.Contains(second, want) {
			t.Errorf("strip line 2 %q is missing %q", second, want)
		}
	}
}

// TestDetailStripOmitsEmptyTokensRatherThanSpacingThem pins the difference
// between "this session has no branch" and "this session's branch is blank": an
// omitted token leaves no separator behind, at either end of the line.
func TestDetailStripOmitsEmptyTokensRatherThanSpacingThem(t *testing.T) {
	bare := session.RunningSession{Base: "scratch", Project: "scratch", Directory: "/tmp/scratch"}
	items := BuildItemsFor(ViewUrgency, []session.RunningSession{bare}, nil)
	item, _ := items.At(0)
	lines := RenderDetailStrip(DetailStripInput{Width: 60, Items: items, Item: item, Selected: true})
	for i, line := range lines {
		plain := stripAnsi(line)
		if strings.HasPrefix(plain, strings.TrimSpace(metaSeparator)) ||
			strings.HasPrefix(plain, metaSeparator) {
			t.Errorf("line %d leads with a separator for an absent token: %q", i, plain)
		}
		if strings.Contains(plain, metaSeparator+metaSeparator) {
			t.Errorf("line %d doubles its separator around an absent token: %q", i, plain)
		}
		if strings.HasSuffix(plain, strings.TrimRight(metaSeparator, " ")) {
			t.Errorf("line %d trails a separator: %q", i, plain)
		}
	}
	if got := stripAnsi(lines[1]); got != "/tmp/scratch" {
		t.Errorf("second line = %q, want just the directory", got)
	}
}

// TestFamilyDetailStripDescribesTheFamilyAndItsLead pins the summary half: a
// family summary owns no session, so the strip says what the family is and then
// describes the member that decided its urgency rather than picking one
// arbitrarily.
func TestFamilyDetailStripDescribesTheFamilyAndItsLead(t *testing.T) {
	rows := []session.RunningSession{
		{WindowID: 1, Base: "api", Project: "api", Profile: "work", Harness: "claude",
			FamilyKey: "api", FamilyLabel: "api",
			Directory: "/home/user/projects/worktrees/api-feat-x", Branch: "amx/feat-x", State: "key"},
		{WindowID: 2, Base: "api", Project: "api", Profile: "work", Harness: "claude",
			FamilyKey: "api", FamilyLabel: "api",
			Directory: "/home/user/projects/api", Branch: "main", State: "bolt"},
	}
	items := BuildItemsFor(ViewProjects, rows, map[string]bool{})
	var summary Item
	var found bool
	for i := 0; i < items.Len(); i++ {
		if it, _ := items.At(i); it.Kind == ItemFamilySummary {
			summary, found = it, true
			break
		}
	}
	if !found {
		t.Fatalf("projects view produced no family summary for %d checkouts of one project", len(rows))
	}

	lines := RenderDetailStrip(DetailStripInput{
		Width: 78, Items: items, Item: summary, Selected: true, IdleThreshold: time.Hour,
	})
	first, second := stripAnsi(lines[0]), stripAnsi(lines[1])
	if !strings.Contains(first, summary.FamilyLabel) {
		t.Errorf("family strip line 1 %q is missing the label %q", first, summary.FamilyLabel)
	}
	// The tally is per urgency bucket rather than a bare member count: "2" would
	// not tell the operator that one of the two is the reason the family sorted
	// where it did.
	for _, want := range []string{"1 attention", "1 active"} {
		if !strings.Contains(first, want) {
			t.Errorf("family strip line 1 %q is missing the %q tally", first, want)
		}
	}
	// The lead is the attention row, and its context is what tells it apart from
	// the sibling checkout.
	if !strings.Contains(second, "amx/feat-x") || !strings.Contains(second, "api-feat-x") {
		t.Errorf("family strip line 2 %q does not describe the leading member", second)
	}
}
