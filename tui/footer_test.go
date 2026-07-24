package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/session"
)

// TestListFooterPaletteOrder asserts that the palette is rendered in its
// declaration order regardless of the cursor row's state. The test
// reconstructs the expected fragment positions and verifies each entry's
// `key label` substring appears in order across the stripped command rows.
func TestListFooterPaletteOrder(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty list",
			m:    Model{},
		},
		{
			name: "attention row",
			m: Model{
				filtered: []session.RunningSession{{Base: "a", State: "key", Account: "w"}},
			},
		},
		{
			name: "active row",
			m: Model{
				filtered: []session.RunningSession{{Base: "a", State: "bolt", Account: "w"}},
			},
		},
		{
			name: "idle row",
			m: Model{
				filtered: []session.RunningSession{{Base: "a", State: "", Account: "w"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := listFooterCommandBlock(tc.m)
			prev := -1
			for _, e := range listFooterPalette {
				frag := e.key + " " + e.label
				idx := strings.Index(out, frag)
				if idx < 0 {
					t.Errorf("expected fragment %q in footer; got %q", frag, out)
					continue
				}
				if idx <= prev {
					t.Errorf("fragment %q out of order (idx=%d, prev=%d) in %q", frag, idx, prev, out)
				}
				prev = idx
			}
		})
	}
}

// TestListFooterActionsReflow asserts row-targeted and global app commands
// share one wrapping flow. At this width the first global action must use the
// space remaining after the final row action instead of starting a new block.
func TestListFooterActionsReflow(t *testing.T) {
	const width = 110
	out := stripAnsi(listFooterActions(Model{
		width:    width,
		filtered: []session.RunningSession{{State: "bolt"}},
	}))

	if strings.Contains(out, "▏") {
		t.Errorf("footer should not render section bars; got %q", out)
	}

	lines := strings.Split(out, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two reflowed action rows, got %d in %q", len(lines), out)
	}
	if !strings.Contains(lines[1], "O open in editor · f profile") {
		t.Fatalf("expected global actions to continue after final row action; got %q", lines[1])
	}
	for _, frag := range []string{
		"e peek", "s project sessions", "O open in editor",
		"f profile", "l launch", ": command palette", "S sessions", "P plans", "R resort",
	} {
		if !strings.Contains(out, frag) {
			t.Errorf("expected action %q in reflowed footer; got %q", frag, out)
		}
	}
	for _, stale := range []string{"F run plan", "t tail"} {
		if strings.Contains(out, stale) {
			t.Errorf("stale loop command %q should not be advertised; got %q", stale, out)
		}
	}
}

func TestListFooterHasNoSpacerAndZebraRowsSpanViewport(t *testing.T) {
	const width = 110
	m := Model{
		mode:     ModeList,
		width:    width,
		filtered: []session.RunningSession{{State: "bolt"}},
	}

	rendered := renderFooter(m)
	rawLines := strings.Split(rendered, "\n")
	plainLines := strings.Split(stripAnsi(rendered), "\n")
	if len(rawLines) < 3 {
		t.Fatalf("expected wrapped actions followed by navigation, got %d rows:\n%s", len(rawLines), rendered)
	}
	for i, line := range plainLines {
		if strings.TrimSpace(line) == "" {
			t.Fatalf("unexpected blank footer row %d in:\n%s", i, stripAnsi(rendered))
		}
	}
	if !strings.Contains(plainLines[1], "O open in editor · f profile") {
		t.Fatalf("expected actions to reflow across the former section boundary, got %q", plainLines[1])
	}
	if !strings.Contains(plainLines[2], "j/k: up/down") {
		t.Fatalf("expected navigation immediately after actions, got %q", plainLines[2])
	}

	assertZebraFooterRows(t, rendered, width)
}

func TestRootFootersHaveZebraRows(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{name: "dashboard list", m: Model{mode: ModeList, width: 72}},
		{name: "dashboard search", m: Model{mode: ModeSearch, width: 72}},
		{name: "confirmation", m: Model{mode: ModeConfirm, width: 72}},
		{name: "session launcher", m: Model{mode: ModePicker, width: 72}},
		{name: "config picker", m: Model{mode: ModeConfigPicker, width: 72}},
		{name: "help", m: Model{mode: ModeHelp, width: 72}},
		{name: "actions", m: Model{mode: ModeActions, width: 72}},
		{name: "command palette", m: Model{mode: ModeCommandPalette, width: 72}},
		{name: "session browser", m: Model{mode: ModeSessionSearch, width: 72}},
		{name: "transcript viewer", m: Model{mode: ModeSessionView, width: 72}},
		{name: "delete choice", m: Model{mode: ModeDeleteChoice, width: 72}},
		{name: "chip picker", m: Model{mode: ModeChipPicker, width: 72}},
		{name: "message reader", m: Model{mode: ModeMessageDetail, width: 72}},
		{name: "plan browser", m: Model{mode: ModePlanSearch, width: 72}},
		{name: "plan viewer", m: Model{mode: ModePlanView, width: 72}},
		{name: "jump chord", m: Model{mode: ModeList, width: 72, pendingJumpDir: 1}},
		{
			name: "active filter",
			m: Model{
				mode:         ModeList,
				width:        72,
				activeFilter: Filter{Substrings: []string{"api"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertZebraFooterRows(t, renderFooter(tc.m), tc.m.width)
		})
	}
}

func TestFooterZebraExcludesViewsWithoutRootFooter(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{name: "detail overlay", m: Model{mode: ModeDetail, width: 72}},
		{name: "dashboard preview", m: Model{mode: ModeList, width: 72, previewEnabled: true}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderFooter(tc.m); got != "" {
				t.Errorf("renderFooter() = %q, want no root footer", got)
			}
		})
	}
}

func assertZebraFooterRows(t *testing.T, rendered string, width int) {
	t.Helper()
	if rendered == "" {
		t.Fatal("expected a root footer")
	}

	background := footerStripeBackgroundSequence()
	for i, line := range strings.Split(rendered, "\n") {
		if i%2 == 0 {
			if !strings.HasPrefix(line, background) {
				t.Errorf("row %d should start with zebra background", i)
			}
			if got := displayStyledWidth(line); got != width {
				t.Errorf("striped row %d width = %d, want full viewport width %d", i, got, width)
			}
			if !strings.Contains(line, ansi.ResetStyle+background) {
				t.Errorf("striped row %d should reapply background after nested style resets", i)
			}
			continue
		}
		if strings.Contains(line, background) {
			t.Errorf("row %d should retain the terminal's default background", i)
		}
	}
}

func listFooterCommandBlock(m Model) string {
	return stripAnsi(listFooterActions(m))
}

// TestListFooterKeyHintsAlwaysPresent locks in the discoverability
// requirement from the brief: `e` (peek), `C` (launch), `x` (clear), and
// `X` (close) must appear in the footer in every state group, even when dimmed.
func TestListFooterKeyHintsAlwaysPresent(t *testing.T) {
	cases := []struct {
		name string
		m    Model
	}{
		{
			name: "empty list",
			m:    Model{},
		},
		{
			name: "attention",
			m: Model{
				filtered: []session.RunningSession{{State: "key"}},
			},
		},
		{
			name: "active",
			m: Model{
				filtered: []session.RunningSession{{State: "bolt"}},
			},
		},
		{
			name: "idle",
			m: Model{
				filtered: []session.RunningSession{{State: ""}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripAnsi(listFooterActions(tc.m))
			if !strings.Contains(out, "e peek") {
				t.Errorf("expected `e peek` in footer; got %q", out)
			}
			for _, frag := range []string{"C launch", "x clear", "X close"} {
				if !strings.Contains(out, frag) {
					t.Errorf("expected %q in footer; got %q", frag, out)
				}
			}
			for _, stale := range []string{"F run plan", "t tail"} {
				if strings.Contains(out, stale) {
					t.Errorf("stale loop command %q should not be advertised; got %q", stale, out)
				}
			}
		})
	}
}

func TestViewerFootersAdvertiseCopy(t *testing.T) {
	cases := []struct {
		name  string
		mode  Mode
		wants []string
	}{
		{name: "session search", mode: ModeSessionSearch, wants: []string{"Y path"}},
		{name: "session viewer", mode: ModeSessionView, wants: []string{"y copy", "Y path"}},
		{name: "plan search", mode: ModePlanSearch, wants: []string{"Y path"}},
		{name: "plan viewer", mode: ModePlanView, wants: []string{"y copy", "Y path"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripAnsi(renderFooter(Model{mode: tc.mode}))
			for _, want := range tc.wants {
				if !strings.Contains(out, want) {
					t.Fatalf("footer = %q, want %q hint", out, want)
				}
			}
		})
	}
}

func TestCommandPaletteFooterHints(t *testing.T) {
	out := stripAnsi(renderFooter(Model{mode: ModeCommandPalette, width: 100}))
	for _, want := range []string{"ctrl+j/k", "ctrl+n/p move", "enter run", "esc close", "ctrl+c quit"} {
		if !strings.Contains(out, want) {
			t.Fatalf("palette footer = %q, want %q", out, want)
		}
	}
}

func TestPlanViewsAdvertiseLessNavigationKeys(t *testing.T) {
	planSearch := Model{
		mode:   ModePlanSearch,
		width:  100,
		height: 20,
	}
	planView := Model{
		mode:             ModePlanView,
		width:            100,
		height:           20,
		planViewerLines:  []string{"first", "second"},
		planViewerCursor: 0,
	}
	cases := []struct {
		name string
		m    Model
	}{
		{name: "plan search", m: planSearch},
		{name: "plan viewer", m: planView},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := stripAnsi(tc.m.render())
			for _, want := range []string{"ctrl+d/u half", "ctrl+f/b page"} {
				if !strings.Contains(out, want) {
					t.Fatalf("rendered plan view missing %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestRootRenderPinsStatusBarToFinalRow(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width = 140
	m.height = 20

	out := stripAnsi(m.render())
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("rendered line count = %d, want terminal height %d:\n%s", len(lines), m.height, out)
	}

	last := lines[len(lines)-1]
	if got := runewidth.StringWidth(last); got != m.width {
		t.Fatalf("status row width = %d, want %d; row %q", got, m.width, last)
	}
	if !strings.Contains(strings.TrimSpace(last), "Sessions: 3 running") {
		t.Fatalf("final row = %q, want list status", last)
	}

	footerLine := lines[len(lines)-2]
	for _, want := range []string{"j/k: up/down", "q: quit", "p: preview"} {
		if !strings.Contains(footerLine, want) {
			t.Fatalf("footer line above status missing %q: %q", want, footerLine)
		}
	}
}

func TestRootRenderUsesStatusBarForToasts(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width = 80
	m.height = 20
	m.toasts = []Toast{
		{Severity: toastSuccess, Text: "launch finished", Expires: time.Now().Add(time.Minute)},
		{Severity: toastInfo, Text: "older toast", Expires: time.Now().Add(time.Minute)},
	}

	lines := strings.Split(stripAnsi(m.render()), "\n")
	if last := strings.TrimSpace(lines[len(lines)-1]); !strings.Contains(last, "launch finished") {
		t.Fatalf("final row = %q, want latest toast status", last)
	}
	bodyAndFooter := strings.Join(lines[:len(lines)-1], "\n")
	if strings.Contains(bodyAndFooter, "older toast") {
		t.Fatalf("older toast rendered outside status bar:\n%s", bodyAndFooter)
	}
}

func TestRootRenderListPreviewSuppressesFooterButKeepsStatusBar(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width = 140
	m.height = 20
	m.previewEnabled = true

	out := stripAnsi(m.render())
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("rendered line count = %d, want terminal height %d:\n%s", len(lines), m.height, out)
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if !strings.Contains(last, "Sessions: 3 running") {
		t.Fatalf("final row = %q, want list status", last)
	}
	if strings.Contains(out, "q: quit") || strings.Contains(out, "j/k: up/down") {
		t.Fatalf("normal footer hints should be suppressed while preview is on:\n%s", out)
	}
	if !strings.Contains(out, "p: hide preview") {
		t.Fatalf("preview header hint should remain visible:\n%s", out)
	}
}

// TestListFooterActionsWrapNarrow asserts that the action flow emits at least one
// newline and every line fits within the configured terminal width when the
// width is too narrow for the full palette.
func TestListFooterActionsWrapNarrow(t *testing.T) {
	const width = 60
	m := Model{width: width}
	out := listFooterActions(m)
	if strings.Count(out, "\n") < 1 {
		t.Fatalf("expected actions to wrap at width %d, got single line:\n%s", width, out)
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line %d width %d exceeds max %d: %q", i, w, width, line)
		}
	}
}

// TestListFooterActionsNoWrapWide asserts no wrapping happens when the width
// comfortably exceeds the rendered palette.
func TestListFooterActionsNoWrapWide(t *testing.T) {
	m := Model{width: 260}
	out := listFooterActions(m)
	if strings.Contains(out, "\n") {
		t.Errorf("expected actions to fit on one line at width 260, got:\n%s", out)
	}
}

// TestListFooterActionsZeroWidth asserts that a zero-width model (before the
// first WindowSizeMsg) emits a single unwrapped line — preserving the
// pre-wrap fallback behavior.
func TestListFooterActionsZeroWidth(t *testing.T) {
	m := Model{}
	out := listFooterActions(m)
	if strings.Contains(out, "\n") {
		t.Errorf("expected single line at zero width, got:\n%s", out)
	}
}

// TestListFooterLine2WrapsNarrow asserts that line 2 wraps when the nav
// fragments overflow the configured width.
func TestListFooterLine2WrapsNarrow(t *testing.T) {
	const width = 40
	m := Model{width: width}
	nav := []string{
		"j/k: up/down", "]/[: jump-chord", "}/{: nav+focus",
		"enter: focus", "a: actions", "/: filter",
		"?: help", "q: quit", "p: preview",
	}
	out := listFooterLine2(m, nav)
	if strings.Count(out, "\n") < 1 {
		t.Fatalf("expected line2 to wrap at width %d, got single line:\n%s", width, out)
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > width {
			t.Errorf("line %d width %d exceeds max %d: %q", i, w, width, line)
		}
	}
}

// TestListFooterLine2ZeroWidth asserts that a zero-width model emits a single
// unwrapped line for line 2.
func TestListFooterLine2ZeroWidth(t *testing.T) {
	m := Model{}
	nav := []string{"j/k: up/down", "enter: focus", "q: quit"}
	out := listFooterLine2(m, nav)
	if strings.Contains(out, "\n") {
		t.Errorf("expected single line at zero width, got:\n%s", out)
	}
}

// TestListFooterAvailability verifies the per-entry availability rules: rows
// in the attention group activate `x`; bolt rows activate `e`; empty lists
// keep every row-targeted entry unavailable.
func TestListFooterAvailability(t *testing.T) {
	cases := []struct {
		name  string
		m     Model
		want  map[string]bool // expected availability per entry key
		extra map[string]bool // optional additional checks not driven by the map default
	}{
		{
			name: "empty list — only global actions available",
			m:    Model{},
			want: map[string]bool{
				"e": false, "T": false, "r": false, "c": false,
				"C": false, "s": false, "x": false, "X": false,
				"o": false, "O": false,
				"f": true, "l": true, ":": true, "S": true, "P": true, "R": true,
			},
		},
		{
			name: "attention row — x active, e unavailable",
			m: Model{
				filtered: []session.RunningSession{{State: "key"}},
			},
			want: map[string]bool{
				"e": false, "T": true, "r": true, "c": true,
				"C": true, "s": true, "x": true, "X": true,
				"o": false, "O": false,
				"f": true, "l": true, ":": true, "S": true, "P": true, "R": true,
			},
		},
		{
			name: "active row — e active, x unavailable",
			m: Model{
				filtered: []session.RunningSession{{State: "bolt"}},
			},
			want: map[string]bool{
				"e": true, "T": true, "r": true, "c": true,
				"C": true, "s": true, "x": false, "X": true,
				"o": false, "O": false,
				"f": true, "l": true, ":": true, "S": true, "P": true, "R": true,
			},
		},
		{
			name: "idle row — neither e nor x available",
			m: Model{
				filtered: []session.RunningSession{{State: ""}},
			},
			want: map[string]bool{
				"e": false, "T": true, "r": true, "c": true,
				"C": true, "s": true, "x": false, "X": true,
				"o": false, "O": false,
				"f": true, "l": true, ":": true, "S": true, "P": true, "R": true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, e := range listFooterPalette {
				want, ok := tc.want[e.key]
				if !ok {
					t.Fatalf("test case missing entry for key %q", e.key)
				}
				got := e.availFn(tc.m)
				if got != want {
					t.Errorf("availability for %q (%s): got %v, want %v", e.key, e.label, got, want)
				}
			}
		})
	}
}
