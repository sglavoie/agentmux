package dashboard

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

func TestBuildProjectScopesAggregatesPriorityCasingTalliesAndAll(t *testing.T) {
	now := time.Now()
	rows := []session.RunningSession{
		{Project: "api", State: "bolt", PresentationChangedAt: now.Add(-time.Minute)},
		{FamilyLabel: "API", Project: "checkout", State: "error", PresentationChangedAt: now},
		{Project: "docs", State: "stop"},
		{Project: "DOCS", State: "", WindowID: 4},
		{State: "key"}, // contributes to All but has no project aggregate
	}
	last := map[int]time.Time{4: now.Add(-time.Hour)}
	got := BuildProjectScopes(rows, last, time.Minute)
	if len(got) != 3 {
		t.Fatalf("BuildProjectScopes() returned %d scopes, want All plus two projects", len(got))
	}
	if got[0].Label != "All projects" || got[0].Project != "" || got[0].Counts != [3]int{2, 1, 2} {
		t.Fatalf("All scope = %+v", got[0])
	}
	if got[1].Label != "API" || got[1].Project != "API" || got[1].Lead.State != "error" || got[1].Counts != [3]int{1, 1, 0} {
		t.Fatalf("first project scope = %+v", got[1])
	}
	if got[2].Label != "docs" || got[2].Counts != [3]int{0, 0, 2} {
		t.Fatalf("second project scope = %+v", got[2])
	}
}

func TestBuildProjectScopesEmptyContextStillOffersAll(t *testing.T) {
	got := BuildProjectScopes(nil, nil, time.Minute)
	if len(got) != 1 || got[0].Label != "All projects" || got[0].Counts != [3]int{} {
		t.Fatalf("empty scope list = %+v", got)
	}
}

// The identity a selection is preserved by is case-folded and All's is empty,
// which is the pair of facts that lets a rebuilt list be searched for the row
// the operator was on without an index surviving the rebuild.
func TestProjectScopeKeyFoldsCaseAndAllIsEmpty(t *testing.T) {
	scopes := []ProjectScope{{Label: "All projects"}, {Project: "API", Label: "API"}, {Project: "docs", Label: "docs"}}
	if scopes[0].Key() != "" {
		t.Fatalf("All key = %q, want empty", scopes[0].Key())
	}
	if got := FindProjectScope(scopes, "api"); got != 1 {
		t.Fatalf("FindProjectScope(%q) = %d, want 1", "api", got)
	}
	if got := FindProjectScope(scopes, "gone"); got != -1 {
		t.Fatalf("FindProjectScope of a removed project = %d, want -1", got)
	}
}

// All survives every query. A search that could hide it would be a search that
// could strand the operator inside a scope they cannot see the way out of.
func TestFilterProjectScopesKeepsAllAndFoldsCase(t *testing.T) {
	scopes := []ProjectScope{{Label: "All projects"}, {Project: "API", Label: "API"}, {Project: "docs", Label: "docs"}}
	got := FilterProjectScopes(scopes, "AP")
	if len(got) != 2 || got[0].Project != "" || got[1].Project != "API" {
		t.Fatalf("FilterProjectScopes(%q) = %+v", "AP", got)
	}
	if got := FilterProjectScopes(scopes, "nothing"); len(got) != 1 || got[0].Project != "" {
		t.Fatalf("no-match filter = %+v, want All alone", got)
	}
	if got := FilterProjectScopes(scopes, ""); len(got) != len(scopes) {
		t.Fatalf("empty query returned %d scopes, want all %d", len(got), len(scopes))
	}
}

func TestRenderProjectSwitcherClampsWithoutBreakingUnicode(t *testing.T) {
	scopes := []ProjectScope{
		{Label: "All projects"},
		{Project: "long", Label: strings.Repeat("🧬project", 12), Counts: [3]int{12, 3, 456}},
	}
	for _, width := range []int{24, 48} {
		out := RenderProjectSwitcher(ProjectSwitcherInput{Scopes: scopes, Cursor: 1, Width: width, Applied: "LONG"}).Text
		if !utf8.ValidString(out) {
			t.Fatalf("render at width %d is invalid UTF-8", width)
		}
		for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if got := layout.Width(ansi.Strip(line)); got > width {
				t.Fatalf("line %d width = %d, want <= %d: %q", i, got, width, ansi.Strip(line))
			}
		}
		plain := ansi.Strip(out)
		if !strings.Contains(plain, "need") || !strings.Contains(plain, "idle") {
			t.Fatalf("render lacks the tally legend: %q", plain)
		}
		if !strings.Contains(plain, "›●") {
			t.Fatalf("render lacks cursor+applied markers: %q", plain)
		}
	}
}

// A row's label budget is the content width minus its fixed prefix and the tally
// column, so the tally column constant has to be exactly as wide as the tally the
// row renders and as the legend the header reserves. When it was one short the
// row overran the box by two columns and the final clamp ate the idle count
// rather than the padding it exists to trim — a failure the width-clamp test
// above cannot see, because a row that is too long still measures "<= width"
// once it has been truncated.
func TestRenderProjectSwitcherTalliesLineUpUnderTheirHeadings(t *testing.T) {
	scopes := []ProjectScope{
		{Label: "All projects", Counts: [3]int{2, 2, 3}},
		{Project: "api", Label: "api", Counts: [3]int{1, 1, 1}},
	}
	const width = 46
	out := ansi.Strip(RenderProjectSwitcher(ProjectSwitcherInput{Scopes: scopes, Cursor: 0, Width: width}).Text)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("render produced %d lines, want header plus two scopes: %q", len(lines), out)
	}
	for i, line := range lines {
		if got := layout.Width(line); got != width {
			t.Fatalf("line %d width = %d, want exactly %d: %q", i, got, width, line)
		}
		if strings.Contains(line, "…") {
			t.Fatalf("line %d was truncated at a width that fits it: %q", i, line)
		}
	}
	// Each line ends on the same column, so the legend and both tallies line up
	// when they end with the same-width strings. Suffixes rather than byte offsets:
	// the cursor gutter, the applied marker and the state icon are all multi-byte,
	// so an index into the row names a different column than the same index into
	// the ASCII header.
	for i, want := range []string{"need act idle", "   2   2    3", "   1   1    1"} {
		if !strings.HasSuffix(lines[i], want) {
			t.Fatalf("line %d = %q, want it to end with %q", i, lines[i], want)
		}
	}
}

// The applied marker reports the filter, not the cursor. The overlay has to be
// able to say "the keys are here, the list is narrowed to there" at once, and a
// renderer that derived one from the other could not.
func TestRenderProjectSwitcherSeparatesCursorFromAppliedScope(t *testing.T) {
	scopes := []ProjectScope{{Label: "All projects"}, {Project: "API", Label: "API"}, {Project: "docs", Label: "docs"}}
	out := ansi.Strip(RenderProjectSwitcher(ProjectSwitcherInput{Scopes: scopes, Cursor: 1, Width: 40, Applied: "docs"}).Text)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("render produced %d lines, want header plus three scopes: %q", len(lines), out)
	}
	if !strings.HasPrefix(lines[2], "› ") {
		t.Fatalf("cursor row = %q, want the cursor gutter with no applied marker", lines[2])
	}
	if !strings.HasPrefix(lines[3], " ●") {
		t.Fatalf("applied row = %q, want the applied marker with no cursor", lines[3])
	}
}

// The row rectangles come out of the same pagination walk as the text, so they
// name the rows actually drawn rather than the rows the list holds.
func TestRenderProjectSwitcherHitsFollowTheVisibleWindow(t *testing.T) {
	scopes := make([]ProjectScope, 10)
	scopes[0] = ProjectScope{Label: "All projects"}
	for i := 1; i < len(scopes); i++ {
		scopes[i] = ProjectScope{Project: string(rune('a' + i)), Label: string(rune('A' + i))}
	}
	render := RenderProjectSwitcher(ProjectSwitcherInput{Scopes: scopes, Cursor: 8, Offset: 5, Width: 40, Height: 5})
	plain := ansi.Strip(render.Text)
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("render produced %d lines, want a header plus a 5-row window: %q", len(lines), plain)
	}
	if !strings.Contains(plain, "▲") || !strings.Contains(plain, "▼") {
		t.Fatalf("scrolled window lacks both markers: %q", plain)
	}
	if len(render.Hits) != 3 {
		t.Fatalf("hits = %d, want one per drawn scope row", len(render.Hits))
	}
	for i, hit := range render.Hits {
		if hit.Project != scopes[5+i].Project {
			t.Fatalf("hit %d names %q, want %q", i, hit.Project, scopes[5+i].Project)
		}
		wantY := 2 + i // header, then the ▲ marker
		if hit.Rect.Y != wantY || hit.Rect.Height != 1 {
			t.Fatalf("hit %d rect = %+v, want row %d", i, hit.Rect, wantY)
		}
	}
}

func TestRenderProjectSwitcherSearchRowAndNoMatch(t *testing.T) {
	out := ansi.Strip(RenderProjectSwitcher(ProjectSwitcherInput{Width: 40, Query: "zzz", Searching: true}).Text)
	if !strings.Contains(out, "/zzz") {
		t.Fatalf("search row missing: %q", out)
	}
	if !strings.Contains(out, "(no projects match)") {
		t.Fatalf("empty scope list lacks its notice: %q", out)
	}
}
