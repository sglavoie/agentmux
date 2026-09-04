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

// bandRowFixture is one session carrying every optional field at once, so a
// band's decision to keep or drop each of them is visible in a single row.
func bandRowFixture() session.RunningSession {
	return session.RunningSession{
		TabNumber: 3,
		WindowID:  11,
		Account:   "engineering",
		Profile:   "engineering",
		Harness:   "claude",
		Base:      "api",
		Project:   "api-gateway-service",
		Directory: "/home/user/projects/worktrees/api-gateway-service",
		Branch:    "amx/feat-x",
		Dirty:     true,
		State:     "bolt",
	}
}

func bandRowInput(s session.RunningSession, width int) RowInput {
	band := BandFor(width)
	rows := []session.RunningSession{s}
	return RowInput{
		Session:       s,
		Band:          band,
		Width:         width,
		TabNumberCell: renderTabNumberCell(s.TabNumber, tabNumberColumnWidth(rows)),
		ActivityCell:  "0:42 ",
		BranchCell:    renderBranchCell(s, branchColumnWidth(rows)),
		ProfileWidth:  profileColumnWidth(rows),
		TitleWidth:    titleColumnWidth(rows, band),
	}
}

// TestRowBandDropsAndKeepsTheDocumentedFields is the presets themselves: which
// fields each band draws, asserted on the rendered row rather than on the
// allocator, so a field that survives the arithmetic but not the composition
// (or the reverse) still fails.
func TestRowBandDropsAndKeepsTheDocumentedFields(t *testing.T) {
	s := bandRowFixture()
	for _, width := range bandTestWidths {
		row := stripAnsi(FormatRow(bandRowInput(s, width)))
		band := BandFor(width)

		// The title is in every band: it is the field a row exists to carry.
		if !strings.Contains(row, "api-gate") {
			t.Errorf("width %d (%s): row lost its title: %q", width, band, row)
		}

		switch band {
		case BandCompact:
			for _, dropped := range []struct{ name, text string }{
				{"profile", "[E]"},
				{"full profile", "engineering"},
				{"harness", "claude"},
				{"activity", "0:42"},
				{"branch", "amx/feat-x"},
				{"directory", "projects"},
			} {
				if strings.Contains(row, dropped.text) {
					t.Errorf("width %d: compact row should not carry the %s (%q): %q",
						width, dropped.name, dropped.text, row)
				}
			}
		case BandStandard:
			if !strings.Contains(row, "[E]") {
				t.Errorf("width %d: standard row should carry the profile initial: %q", width, row)
			}
			if strings.Contains(row, "engineering") {
				t.Errorf("width %d: only a spacious row spells the profile out: %q", width, row)
			}
			if !strings.Contains(row, "claude") {
				t.Errorf("width %d: standard row should carry the harness: %q", width, row)
			}
		case BandSpacious:
			if !strings.Contains(row, "[engineering]") {
				t.Errorf("width %d: spacious row should spell the profile out: %q", width, row)
			}
			if !strings.Contains(row, "claude") {
				t.Errorf("width %d: spacious row should carry the harness: %q", width, row)
			}
			if !strings.Contains(row, "amx/feat-x") {
				t.Errorf("width %d: spacious row should carry the branch: %q", width, row)
			}
		}
	}
}

// TestCompactTitleStaysUsefulAtEveryCompactWidth pins the floor the band's
// whole point rests on: dropping five fields buys the title room, and a row
// that spent it and still showed three characters would not have been worth the
// trade.
func TestCompactTitleStaysUsefulAtEveryCompactWidth(t *testing.T) {
	s := bandRowFixture()
	for width := MinRowWidth(); width < bandStandardMinWidth; width++ {
		in := bandRowInput(s, width)
		row := stripAnsi(FormatRow(in))
		prefixWidth := layout.Width(in.TabNumberCell) + lockColumnWidth + muteColumnWidth +
			layout.IconCellWidth + 1
		if got := layout.Width(row) - prefixWidth; got < compactTitleMinWidth-1 {
			t.Errorf("width %d: compact title got %d cells, want at least %d",
				width, got, compactTitleMinWidth-1)
		}
		if !utf8.ValidString(row) {
			t.Errorf("width %d: row is not valid UTF-8: %q", width, row)
		}
	}
}

// TestSpaciousTitleExpandsToContentBeforeTheDirectory pins the direction the
// spacious band spends its surplus in. The standard column is fixed at
// titleColWidth; spacious grows it to the widest title on screen, so the
// directory's budget is what shrinks rather than the title's.
func TestSpaciousTitleExpandsToContentBeforeTheDirectory(t *testing.T) {
	rows := []session.RunningSession{
		{Base: "api", Project: "api"},
		{Base: "long", Project: "an-unusually-long-project-title"},
	}
	if got := titleColumnWidth(rows, BandStandard); got != titleColWidth {
		t.Errorf("standard title column = %d, want the fixed %d", got, titleColWidth)
	}
	spacious := titleColumnWidth(rows, BandSpacious)
	if spacious <= titleColWidth {
		t.Errorf("spacious title column = %d, want it grown past the fixed %d", spacious, titleColWidth)
	}
	if spacious > spaciousTitleMaxWidth {
		t.Errorf("spacious title column = %d, want it capped at %d", spacious, spaciousTitleMaxWidth)
	}
	if got := titleColumnWidth(rows, BandCompact); got != 0 {
		t.Errorf("compact title column = %d, want none (the title is flexible there)", got)
	}

	// The same growth, seen through the allocator: at one width, the spacious
	// title is wider and the directory is narrower by what the title took.
	in := bandRowInput(rows[1], 160)
	in.Session = rows[1]
	in.ProfileWidth = profileColumnWidth(rows)
	wide := allocateRow(in, 12, 8)
	in.Band = BandStandard
	in.TitleWidth = titleColumnWidth(rows, BandStandard)
	narrow := allocateRow(in, 12, 8)
	if wide.title <= narrow.title {
		t.Errorf("spacious title = %d, standard = %d: spacious must expand to content",
			wide.title, narrow.title)
	}
	if wide.dir >= narrow.dir {
		t.Errorf("spacious dir = %d, standard = %d: the title's growth must come out of the directory",
			wide.dir, narrow.dir)
	}
}

// TestProfileNameCellIsBoundedAndValidUTF8 pins the two things a profile name
// can do that nothing else in the row can: it is operator-chosen so it has no
// natural bound, and it can start with a multibyte rune the initial badge would
// otherwise cut in half.
func TestProfileNameCellIsBoundedAndValidUTF8(t *testing.T) {
	long := session.RunningSession{Profile: strings.Repeat("veryverylong", 4)}
	text, cell := profileCellFor(RowInput{Session: long, Band: BandSpacious,
		ProfileWidth: profileColumnWidth([]session.RunningSession{long})})
	if layout.Width(text) > profileNameMaxWidth || cell > profileNameMaxWidth+2 {
		t.Errorf("spacious profile cell = %q (%d cells), want bounded by %d",
			text, cell, profileNameMaxWidth)
	}

	unicode := session.RunningSession{Profile: "élan"}
	initial, cell := profileCellFor(RowInput{Session: unicode, Band: BandStandard})
	if cell != profileInitialCellWidth {
		t.Errorf("standard profile cell width = %d, want %d", cell, profileInitialCellWidth)
	}
	if !utf8.ValidString(initial) || initial != "É" {
		t.Errorf("standard profile initial = %q, want the uppercased first rune", initial)
	}
	if _, cell := profileCellFor(RowInput{Session: unicode, Band: BandCompact}); cell != 0 {
		t.Errorf("compact profile cell width = %d, want none", cell)
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
		if strings.HasPrefix(plain, strings.TrimSpace(detailStripSeparator)) ||
			strings.HasPrefix(plain, detailStripSeparator) {
			t.Errorf("line %d leads with a separator for an absent token: %q", i, plain)
		}
		if strings.Contains(plain, detailStripSeparator+detailStripSeparator) {
			t.Errorf("line %d doubles its separator around an absent token: %q", i, plain)
		}
		if strings.HasSuffix(plain, strings.TrimRight(detailStripSeparator, " ")) {
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
