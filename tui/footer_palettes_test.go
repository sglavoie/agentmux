package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
	"github.com/sglavoie/agentmux/tui/asl"
	"github.com/sglavoie/agentmux/tui/layout"
)

// hintTestPalette exercises the truncation engine with known priorities:
// two essential entries that must always survive and three droppable ones,
// two of which tie so the last-declared-first rule is observable.
var hintTestPalette = []footerEntry{
	{"enter", "view", footerSectionRow, footerAlways, footerPriorityEssential},
	{"a", "alpha", footerSectionRow, footerAlways, footerPriorityPrimary},
	{"b", "bravo", footerSectionRow, footerAlways, footerPrioritySecondary},
	{"c", "charlie", footerSectionRow, footerAlways, footerPrioritySecondary},
	{"esc", "back", footerSectionApp, footerAlways, footerPriorityEssential},
}

func hintText(lines []string) string {
	return stripAnsi(strings.Join(lines, "\n"))
}

func TestFooterHintLinesFitWideTerminal(t *testing.T) {
	cases := []struct {
		name    string
		palette []footerEntry
	}{
		{name: "session search", palette: sessionSearchFooterPalette},
		{name: "session view", palette: sessionViewFooterPalette},
		{name: "plan search", palette: planSearchFooterPalette},
		{name: "plan view", palette: planViewFooterPalette},
		{name: "worktrees", palette: worktreesFooterPalette},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := footerHintLines(Model{width: 200}, tc.palette, 2)
			if len(lines) > 2 {
				t.Fatalf("palette rendered %d rows at width 200, want at most 2:\n%s", len(lines), hintText(lines))
			}
			if strings.Contains(hintText(lines), footerHintMore) {
				t.Fatalf("wide palette should not truncate; got %q", hintText(lines))
			}
		})
	}
}

func TestFooterHintPalettesAdvertiseModeHints(t *testing.T) {
	cases := []struct {
		name    string
		palette []footerEntry
		wants   []string
	}{
		{
			name:    "session search",
			palette: sessionSearchFooterPalette,
			wants: []string{"/ filter", "h/p/r/m chips", "enter view", "c continue", "f fork",
				"w worktree", "H handoff", "E edit handoff", "d delete", "i ID", "Y path", "esc back"},
		},
		{
			name:    "session view",
			palette: sessionViewFooterPalette,
			wants: []string{"j/k move", "n/N match", "enter view message", "c continue", "f fork",
				"w worktree", "H handoff", "E edit handoff", "i ID", "y copy", "Y path", "esc back"},
		},
		{
			name:    "plan search",
			palette: planSearchFooterPalette,
			wants:   []string{"/ filter", "r project", "enter view", "o sort", "e editor", "Y path", "esc back"},
		},
		{
			name:    "plan view",
			palette: planViewFooterPalette,
			wants:   []string{"/ search", "n/N match", "y copy", "Y path", "e editor", "esc back"},
		},
		{
			name:    "worktrees",
			palette: worktreesFooterPalette,
			wants:   []string{"/ filter", "o shell", "O editor", "Y path", "r refresh", "? help", "esc back"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The zero Model makes every context-dependent entry unavailable;
			// unavailable entries change style only, never presence.
			out := hintText(footerHintLines(Model{width: 400}, tc.palette, 2))
			for _, want := range tc.wants {
				if !strings.Contains(out, want) {
					t.Fatalf("palette = %q, want %q hint", out, want)
				}
			}
		})
	}
}

func TestFooterHintLinesDropsHighestPriorityFirst(t *testing.T) {
	full := footerHintLines(Model{width: 500}, hintTestPalette, 1)
	if len(full) != 1 {
		t.Fatalf("wide render = %d rows, want 1:\n%s", len(full), hintText(full))
	}
	fullWidth := layout.StyledWidth(full[0])

	lines := footerHintLines(Model{width: fullWidth - 1}, hintTestPalette, 1)
	if len(lines) != 1 {
		t.Fatalf("truncated render = %d rows, want 1:\n%s", len(lines), hintText(lines))
	}
	out := hintText(lines)
	if strings.Contains(out, "c charlie") {
		t.Fatalf("last-declared tie should drop first; got %q", out)
	}
	for _, want := range []string{"enter view", "a alpha", "b bravo", "esc back", footerHintMore} {
		if !strings.Contains(out, want) {
			t.Fatalf("truncated palette = %q, want %q", out, want)
		}
	}
}

func TestFooterHintLinesNeverDropsEssentialEntries(t *testing.T) {
	lines := footerHintLines(Model{width: 20}, hintTestPalette, 1)
	out := hintText(lines)
	for _, want := range []string{"enter view", "esc back", footerHintMore} {
		if !strings.Contains(out, want) {
			t.Fatalf("essential palette = %q, want %q", out, want)
		}
	}
	for _, gone := range []string{"a alpha", "b bravo", "c charlie"} {
		if strings.Contains(out, gone) {
			t.Fatalf("droppable entry %q survived at width 20: %q", gone, out)
		}
	}
}

func TestFooterHintLinesDegenerateWidth(t *testing.T) {
	lines := footerHintLines(Model{width: 0}, hintTestPalette, 1)
	if len(lines) != 1 {
		t.Fatalf("width 0 render = %d rows, want the single unwrapped fallback:\n%s", len(lines), hintText(lines))
	}
	want := footerHintIndent + strings.Join([]string{
		"enter view", "a alpha", "b bravo", "c charlie", "esc back",
	}, footerHintSeparator)
	if got := hintText(lines); got != want {
		t.Fatalf("width 0 render = %q, want %q", got, want)
	}
}

// footerHintCharacterizations pins the exact physical rows every specialized
// palette renders at four widths: wide enough for a single row, narrow enough
// to wrap into the two-row budget, narrow enough to force drops, and the
// degenerate pre-WindowSizeMsg width that disables wrapping entirely. The zero
// Model makes every context-dependent entry unavailable, which is the state
// each mode paints before its data loads; availability changes styling only,
// never text or row count, which is pinned separately by
// TestFooterHintAvailabilityChangesStyleOnly.
//
// These rows were captured from the footerEntry-driven renderer that predates
// prioritizedFooterLines, so a diff here is a user-visible footer change rather
// than a refactor.
var footerHintCharacterizations = []struct {
	name    string
	entries []footerEntry
	width   int
	want    []string
}{
	{
		name:    "session search/210",
		entries: sessionSearchFooterPalette,
		width:   210,
		want: []string{
			"  / filter · h/p/r/m chips · enter view · c continue · f fork · w worktree · H handoff · E edit handoff · d delete · i ID · Y path · ? help · esc back · j/k up/down · ctrl+d/u half · ctrl+f/b page · g/G top/bot",
		},
	},
	{
		name:    "session search/90",
		entries: sessionSearchFooterPalette,
		width:   90,
		want: []string{
			"  / filter · h/p/r/m chips · enter view · c continue · f fork · w worktree · H handoff",
			"  E edit handoff · d delete · i ID · Y path · esc back · j/k up/down · ? more",
		},
	},
	{
		name:    "session search/40",
		entries: sessionSearchFooterPalette,
		width:   40,
		want: []string{
			"  / filter · enter view · c continue",
			"  f fork · esc back · ? more",
		},
	},
	{
		name:    "session search/0",
		entries: sessionSearchFooterPalette,
		width:   0,
		want: []string{
			"  / filter · h/p/r/m chips · enter view · c continue · f fork · w worktree · H handoff · E edit handoff · d delete · i ID · Y path · ? help · esc back · j/k up/down · ctrl+d/u half · ctrl+f/b page · g/G top/bot",
		},
	},
	{
		name:    "session view/200",
		entries: sessionViewFooterPalette,
		width:   200,
		want: []string{
			"  j/k move · n/N match · enter view message · c continue · f fork · w worktree · H handoff · E edit handoff · i ID · y copy · Y path · ? help · esc back · ctrl+d/u half · ctrl+f/b page · g/G top/bot",
		},
	},
	{
		name:    "session view/90",
		entries: sessionViewFooterPalette,
		width:   90,
		want: []string{
			"  j/k move · n/N match · enter view message · c continue · f fork · w worktree · H handoff",
			"  E edit handoff · i ID · y copy · Y path · esc back · ctrl+d/u half · ? more",
		},
	},
	{
		name:    "session view/40",
		entries: sessionViewFooterPalette,
		width:   40,
		want: []string{
			"  enter view message · c continue",
			"  f fork · esc back · ? more",
		},
	},
	{
		name:    "session view/0",
		entries: sessionViewFooterPalette,
		width:   0,
		want: []string{
			"  j/k move · n/N match · enter view message · c continue · f fork · w worktree · H handoff · E edit handoff · i ID · y copy · Y path · ? help · esc back · ctrl+d/u half · ctrl+f/b page · g/G top/bot",
		},
	},
	{
		name:    "plan search/200",
		entries: planSearchFooterPalette,
		width:   200,
		want: []string{
			"  / filter · r project · enter view · o sort · e editor · Y path · ? help · esc back · j/k up/down · g/G top/bot · ctrl+d/u half · ctrl+f/b page",
		},
	},
	{
		name:    "plan search/90",
		entries: planSearchFooterPalette,
		width:   90,
		want: []string{
			"  / filter · r project · enter view · o sort · e editor · Y path · ? help · esc back",
			"  j/k up/down · g/G top/bot · ctrl+d/u half · ctrl+f/b page",
		},
	},
	{
		name:    "plan search/40",
		entries: planSearchFooterPalette,
		width:   40,
		want: []string{
			"  / filter · r project · enter view",
			"  o sort · e editor · esc back · ? more",
		},
	},
	{
		name:    "plan search/0",
		entries: planSearchFooterPalette,
		width:   0,
		want: []string{
			"  / filter · r project · enter view · o sort · e editor · Y path · ? help · esc back · j/k up/down · g/G top/bot · ctrl+d/u half · ctrl+f/b page",
		},
	},
	{
		name:    "plan view/200",
		entries: planViewFooterPalette,
		width:   200,
		want: []string{
			"  / search · n/N match · y copy · Y path · e editor · ? help · esc back · j/k scroll · g/G top/bot · ctrl+d/u half · ctrl+f/b page",
		},
	},
	{
		name:    "plan view/90",
		entries: planViewFooterPalette,
		width:   90,
		want: []string{
			"  / search · n/N match · y copy · Y path · e editor · ? help · esc back · j/k scroll",
			"  g/G top/bot · ctrl+d/u half · ctrl+f/b page",
		},
	},
	{
		name:    "plan view/40",
		entries: planViewFooterPalette,
		width:   40,
		want: []string{
			"  / search · n/N match · y copy · Y path",
			"  e editor · esc back · ? more",
		},
	},
	{
		name:    "plan view/0",
		entries: planViewFooterPalette,
		width:   0,
		want: []string{
			"  / search · n/N match · y copy · Y path · e editor · ? help · esc back · j/k scroll · g/G top/bot · ctrl+d/u half · ctrl+f/b page",
		},
	},
	{
		name:    "worktrees/200",
		entries: worktreesFooterPalette,
		width:   200,
		want: []string{
			"  / filter · enter reopen · o shell · O editor · d remove · D delete branch · F forget · Y path · r refresh · ? help · esc back · j/k up/down · ctrl+d/u half · ctrl+f/b page · g/G top/bot",
		},
	},
	{
		name:    "worktrees/90",
		entries: worktreesFooterPalette,
		width:   90,
		want: []string{
			"  / filter · enter reopen · o shell · O editor · d remove · D delete branch · F forget",
			"  Y path · r refresh · esc back · j/k up/down · ctrl+d/u half · ctrl+f/b page · ? more",
		},
	},
	{
		name:    "worktrees/40",
		entries: worktreesFooterPalette,
		width:   40,
		want: []string{
			"  enter reopen · o shell · O editor",
			"  d remove · esc back · ? more",
		},
	},
	{
		name:    "worktrees/0",
		entries: worktreesFooterPalette,
		width:   0,
		want: []string{
			"  / filter · enter reopen · o shell · O editor · d remove · D delete branch · F forget · Y path · r refresh · ? help · esc back · j/k up/down · ctrl+d/u half · ctrl+f/b page · g/G top/bot",
		},
	},
	{
		name:    "asl runs/200",
		entries: aslRunsFooterPalette,
		width:   200,
		want: []string{
			"  f focus · r respond · s stop · a abort · w workbench · Y path · ? help · esc back · j/k up/down · ctrl+d/u half · ctrl+f/b page · g/G top/bot",
		},
	},
	{
		name:    "asl runs/90",
		entries: aslRunsFooterPalette,
		width:   90,
		want: []string{
			"  f focus · r respond · s stop · a abort · w workbench · Y path · ? help · esc back",
			"  j/k up/down · ctrl+d/u half · ctrl+f/b page · g/G top/bot",
		},
	},
	{
		name:    "asl runs/40",
		entries: aslRunsFooterPalette,
		width:   40,
		want: []string{
			"  f focus · r respond · s stop · a abort",
			"  w workbench · esc back · ? more",
		},
	},
	{
		name:    "asl runs/0",
		entries: aslRunsFooterPalette,
		width:   0,
		want: []string{
			"  f focus · r respond · s stop · a abort · w workbench · Y path · ? help · esc back · j/k up/down · ctrl+d/u half · ctrl+f/b page · g/G top/bot",
		},
	},
	{
		name:    "asl response/200",
		entries: aslResponseFooterPalette,
		width:   200,
		want: []string{
			"  ctrl+s submit · tab editor/buttons · enter newline or button · ctrl+n/p question · esc cancel",
		},
	},
	{
		name:    "asl response/90",
		entries: aslResponseFooterPalette,
		width:   90,
		want: []string{
			"  ctrl+s submit · tab editor/buttons · enter newline or button · ctrl+n/p question",
			"  esc cancel",
		},
	},
	{
		name:    "asl response/40",
		entries: aslResponseFooterPalette,
		width:   40,
		want: []string{
			"  ctrl+s submit · tab editor/buttons",
			"  esc cancel · ? more",
		},
	},
	{
		name:    "asl response/0",
		entries: aslResponseFooterPalette,
		width:   0,
		want: []string{
			"  ctrl+s submit · tab editor/buttons · enter newline or button · ctrl+n/p question · esc cancel",
		},
	},
}

func TestFooterHintPaletteCharacterization(t *testing.T) {
	for _, tc := range footerHintCharacterizations {
		t.Run(tc.name, func(t *testing.T) {
			lines := footerHintLines(Model{width: tc.width}, tc.entries, footerHintMaxLines)
			if len(lines) != len(tc.want) {
				t.Fatalf("rendered %d physical rows, want %d:\n%s", len(lines), len(tc.want), hintText(lines))
			}
			for i, want := range tc.want {
				if got := stripAnsi(lines[i]); got != want {
					t.Fatalf("row %d = %q, want %q", i, got, want)
				}
			}
		})
	}
}

// availableFooterModel returns a Model whose data satisfies the
// context-dependent availability functions of the named palette, paired with
// one entry that flips from unavailable to available with it.
type footerAvailabilityCase struct {
	name    string
	entries []footerEntry
	model   Model
	key     string
	label   string
}

func footerAvailabilityCases() []footerAvailabilityCase {
	return []footerAvailabilityCase{
		{
			name:    "session search",
			entries: sessionSearchFooterPalette,
			model: Model{sessionCursor: 0, sessionResults: []transcript.Result{
				{Meta: transcript.SessionMeta{ID: "abc123", Path: "/tmp/session.jsonl"}},
			}},
			key: "c", label: "continue",
		},
		{
			name:    "session view",
			entries: sessionViewFooterPalette,
			model: Model{
				viewerSession:    &transcript.SessionMeta{ID: "abc123", Path: "/tmp/session.jsonl"},
				viewerMessages:   []transcript.Message{{}},
				viewerMatchLines: []int{0},
			},
			key: "f", label: "fork",
		},
		{
			name:    "plan search",
			entries: planSearchFooterPalette,
			model: Model{planCursor: 0, planResults: []plans.Result{
				{Meta: plans.PlanMeta{Name: "plan", DirPath: "/tmp/plan"}},
			}},
			key: "e", label: "editor",
		},
		{
			name:    "plan view",
			entries: planViewFooterPalette,
			model: Model{
				viewerPlan:           &plans.PlanMeta{Name: "plan", DirPath: "/tmp/plan"},
				planViewerLines:      []string{"body"},
				planViewerMatchLines: []int{0},
			},
			key: "e", label: "editor",
		},
		{
			name:    "worktrees",
			entries: worktreesFooterPalette,
			model: Model{worktreeCursor: 0, worktreeFiltered: []actions.WorktreeView{
				{Entry: worktree.Entry{Path: "/tmp/worktree", Exists: true}},
			}},
			key: "o", label: "shell",
		},
		{
			name:    "asl response",
			entries: aslResponseFooterPalette,
			model:   Model{aslResponse: asl.ResponseState{QuestionIDs: []string{"q1", "q2"}}},
			key:     "ctrl+n/p", label: "question",
		},
	}
}

// TestFooterHintAvailabilityChangesStyleOnly is the unavailable-action half of
// the characterization: an entry whose action is unavailable keeps its place and
// its text, and only its key style changes. Layout therefore cannot shift as a
// mode's data loads.
func TestFooterHintAvailabilityChangesStyleOnly(t *testing.T) {
	for _, tc := range footerAvailabilityCases() {
		t.Run(tc.name, func(t *testing.T) {
			styled := 0
			for _, width := range []int{200, 90, 40, 0} {
				loaded := tc.model
				loaded.width = width
				available := footerHintLines(loaded, tc.entries, footerHintMaxLines)
				unavailable := footerHintLines(Model{width: width}, tc.entries, footerHintMaxLines)

				if len(available) != len(unavailable) {
					t.Fatalf("width %d: available rendered %d rows, unavailable %d", width, len(available), len(unavailable))
				}
				if got, want := hintText(available), hintText(unavailable); got != want {
					t.Fatalf("width %d: availability changed the text\n got %q\nwant %q", width, got, want)
				}

				// A narrow width may drop the sampled entry outright — the two
				// renders having already agreed on text, they dropped the same
				// one. Assert the styles only where it survived.
				if !strings.Contains(hintText(available), tc.key+" "+tc.label) {
					continue
				}
				styled++
				if raw := strings.Join(available, "\n"); !strings.Contains(raw, renderFooterBinding(tc.key, tc.label)) {
					t.Fatalf("width %d: %q is available but not rendered in the available style", width, tc.key+" "+tc.label)
				}
				if raw := strings.Join(unavailable, "\n"); !strings.Contains(raw, renderUnavailableFooterBinding(tc.key, tc.label)) {
					t.Fatalf("width %d: %q is unavailable but not rendered in the muted style", width, tc.key+" "+tc.label)
				}
			}
			if styled == 0 {
				t.Fatalf("%q survived no width, so this case asserted no styling", tc.key+" "+tc.label)
			}
		})
	}
}

// TestFooterHintASLRunsMixedAvailability covers the ASL Runs palette, whose
// availability is answered by the bridge rather than by a Model field: the
// fixture's row-verify permits stop and refuses abort, so one render must carry
// both styles at once.
func TestFooterHintASLRunsMixedAvailability(t *testing.T) {
	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-verify")
	m := d.Model()
	m.width = 200

	if !aslStopAvailable(m) || aslAbortAvailable(m) {
		t.Fatalf("fixture changed: stop available = %v, abort available = %v, want true/false",
			aslStopAvailable(m), aslAbortAvailable(m))
	}

	lines := footerHintLines(m, aslRunsFooterPalette, footerHintMaxLines)
	raw := strings.Join(lines, "\n")
	if !strings.Contains(raw, renderFooterBinding("s", "stop")) {
		t.Fatalf("permitted stop is not rendered in the available style: %q", hintText(lines))
	}
	if !strings.Contains(raw, renderUnavailableFooterBinding("a", "abort")) {
		t.Fatalf("refused abort is not rendered in the muted style: %q", hintText(lines))
	}

	zero := footerHintLines(Model{width: 200}, aslRunsFooterPalette, footerHintMaxLines)
	if got, want := hintText(lines), hintText(zero); got != want {
		t.Fatalf("bridge availability changed the ASL Runs text\n got %q\nwant %q", got, want)
	}
}

// hintTestTokens is hintTestPalette rendered into prioritized tokens, so the
// helper-level tests below exercise the same priorities the entry-level tests
// above do.
func hintTestTokens() []prioritizedFooterToken {
	return footerHintTokens(Model{}, hintTestPalette)
}

// TestPrioritizedFooterLinesMatchesEntryAdapter is the refactor's load-bearing
// assertion: for every characterized palette and width, driving the helper
// directly reproduces the adapter's rows byte for byte, ANSI included.
func TestPrioritizedFooterLinesMatchesEntryAdapter(t *testing.T) {
	for _, tc := range footerHintCharacterizations {
		t.Run(tc.name, func(t *testing.T) {
			m := Model{width: tc.width}
			want := footerHintLines(m, tc.entries, footerHintMaxLines)
			got := prioritizedFooterLines(footerHintTokens(m, tc.entries), footerHintIndent, tc.width, footerHintMaxLines)
			if len(got) != len(want) {
				t.Fatalf("helper rendered %d rows, adapter %d", len(got), len(want))
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("row %d\n got %q\nwant %q", i, got[i], want[i])
				}
			}
		})
	}
}

func TestPrioritizedFooterLinesDropsHighestPriorityFirst(t *testing.T) {
	full := prioritizedFooterLines(hintTestTokens(), footerHintIndent, 500, 1)
	if len(full) != 1 {
		t.Fatalf("wide render = %d rows, want 1:\n%s", len(full), hintText(full))
	}
	fullWidth := layout.StyledWidth(full[0])

	lines := prioritizedFooterLines(hintTestTokens(), footerHintIndent, fullWidth-1, 1)
	if len(lines) != 1 {
		t.Fatalf("truncated render = %d rows, want 1:\n%s", len(lines), hintText(lines))
	}
	out := hintText(lines)
	if strings.Contains(out, "c charlie") {
		t.Fatalf("last-declared tie should drop first; got %q", out)
	}
	for _, want := range []string{"enter view", "a alpha", "b bravo", "esc back", footerHintMore} {
		if !strings.Contains(out, want) {
			t.Fatalf("truncated tokens = %q, want %q", out, want)
		}
	}
}

func TestPrioritizedFooterLinesNeverDropsEssentialTokens(t *testing.T) {
	out := hintText(prioritizedFooterLines(hintTestTokens(), footerHintIndent, 20, 1))
	for _, want := range []string{"enter view", "esc back", footerHintMore} {
		if !strings.Contains(out, want) {
			t.Fatalf("essential tokens = %q, want %q", out, want)
		}
	}
	for _, gone := range []string{"a alpha", "b bravo", "c charlie"} {
		if strings.Contains(out, gone) {
			t.Fatalf("droppable token %q survived at width 20: %q", gone, out)
		}
	}
}

func TestPrioritizedFooterLinesDegenerateWidth(t *testing.T) {
	lines := prioritizedFooterLines(hintTestTokens(), footerHintIndent, 0, 1)
	if len(lines) != 1 {
		t.Fatalf("width 0 render = %d rows, want the single unwrapped fallback:\n%s", len(lines), hintText(lines))
	}
	want := footerHintIndent + strings.Join([]string{
		"enter view", "a alpha", "b bravo", "c charlie", "esc back",
	}, footerHintSeparator)
	if got := hintText(lines); got != want {
		t.Fatalf("width 0 render = %q, want %q", got, want)
	}
}

func TestPrioritizedFooterLinesUnlimitedRowsNeverTruncate(t *testing.T) {
	lines := prioritizedFooterLines(hintTestTokens(), footerHintIndent, 24, 0)
	if len(lines) < 2 {
		t.Fatalf("maxLines 0 should wrap freely; got %d rows:\n%s", len(lines), hintText(lines))
	}
	out := hintText(lines)
	if strings.Contains(out, footerHintMore) {
		t.Fatalf("maxLines 0 must not truncate; got %q", out)
	}
	for _, want := range []string{"enter view", "a alpha", "b bravo", "c charlie", "esc back"} {
		if !strings.Contains(out, want) {
			t.Fatalf("untruncated render = %q, want %q", out, want)
		}
	}
}

// TestPrioritizedFooterLinesWrapsWholeTokens pins that a token is never split
// mid-token: one too wide for the terminal takes a row of its own and is
// clamped there.
func TestPrioritizedFooterLinesWrapsWholeTokens(t *testing.T) {
	tokens := []prioritizedFooterToken{
		{token: footerBindingToken("a", "short"), priority: footerPriorityEssential},
		{token: footerBindingToken("b", strings.Repeat("wide", 12)), priority: footerPriorityEssential},
		{token: footerBindingToken("c", "tail"), priority: footerPriorityEssential},
	}
	lines := prioritizedFooterLines(tokens, footerHintIndent, 24, 0)
	if len(lines) != 3 {
		t.Fatalf("render = %d rows, want one per token:\n%s", len(lines), hintText(lines))
	}
	for i, line := range lines {
		if got := layout.StyledWidth(line); got > 24 {
			t.Fatalf("row %d width = %d, want it clamped to 24: %q", i, got, stripAnsi(line))
		}
	}
	if got := stripAnsi(lines[0]); got != footerHintIndent+"a short" {
		t.Fatalf("first row = %q, want the short token alone", got)
	}
	if got := stripAnsi(lines[2]); got != footerHintIndent+"c tail" {
		t.Fatalf("last row = %q, want the trailing token alone", got)
	}
	if strings.Contains(stripAnsi(lines[1]), "c tail") {
		t.Fatalf("an oversized token shared its row: %q", stripAnsi(lines[1]))
	}
}

// TestPrioritizedFooterLinesOwnsSeparators pins that the helper assigns every
// separator itself: a caller-supplied one is ignored, and the block's last
// token never trails one even after drops move which token is last.
func TestPrioritizedFooterLinesOwnsSeparators(t *testing.T) {
	tokens := hintTestTokens()
	for i := range tokens {
		tokens[i].token.sep = "|BOGUS|"
		tokens[i].token.sepWidth = 7
	}
	out := hintText(prioritizedFooterLines(tokens, footerHintIndent, 500, 1))
	if strings.Contains(out, "BOGUS") {
		t.Fatalf("caller separator leaked into the render: %q", out)
	}
	if want := footerHintIndent + strings.Join([]string{
		"enter view", "a alpha", "b bravo", "c charlie", "esc back",
	}, footerHintSeparator); out != want {
		t.Fatalf("render = %q, want %q", out, want)
	}

	truncated := hintText(prioritizedFooterLines(tokens, footerHintIndent, 20, 1))
	if strings.HasSuffix(truncated, footerHintSeparator) {
		t.Fatalf("truncated render trails a separator: %q", truncated)
	}
	if !strings.HasSuffix(truncated, footerHintMore) {
		t.Fatalf("truncated render = %q, want it to end in %q", truncated, footerHintMore)
	}
}

// TestPrioritizedFooterLinesDoesNotMutateCallerTokens keeps the drop loop's
// slice surgery off the caller's backing array.
func TestPrioritizedFooterLinesDoesNotMutateCallerTokens(t *testing.T) {
	tokens := hintTestTokens()
	before := make([]prioritizedFooterToken, len(tokens))
	copy(before, tokens)

	prioritizedFooterLines(tokens, footerHintIndent, 20, 1)

	if len(tokens) != len(before) {
		t.Fatalf("caller slice length changed to %d, want %d", len(tokens), len(before))
	}
	for i := range before {
		if tokens[i] != before[i] {
			t.Fatalf("token %d was mutated: %+v, want %+v", i, tokens[i], before[i])
		}
	}
}
