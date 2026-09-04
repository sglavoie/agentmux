package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/session"
)

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
	if !strings.Contains(strings.TrimSpace(last), "Sessions: 3 · ") {
		t.Fatalf("final row = %q, want list status", last)
	}

	footerLine := lines[len(lines)-2]
	for _, want := range []string{"j/k up/down", "q quit", "p preview"} {
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
	if !strings.Contains(last, "Sessions: 3 · ") {
		t.Fatalf("final row = %q, want list status", last)
	}
	if strings.Contains(out, "q quit") || strings.Contains(out, "j/k up/down") {
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
	nav := listFooterNavTokens(m)
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
	nav := []string{renderFooterBinding("j/k", "up/down"), renderFooterBinding("enter", "focus"), renderFooterBinding("q", "quit")}
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
				"v": true,
			},
		},
		{
			name: "attention row — x active, e unavailable",
			m:    withListRows(Model{}, session.RunningSession{State: "key"}),
			want: map[string]bool{
				"e": false, "T": true, "r": true, "c": true,
				"C": true, "s": true, "x": true, "X": true,
				"o": false, "O": false,
				"f": true, "l": true, ":": true, "S": true, "P": true, "R": true,
				"v": true,
			},
		},
		{
			name: "active row — e active, x unavailable",
			m:    withListRows(Model{}, session.RunningSession{State: "bolt"}),
			want: map[string]bool{
				"e": true, "T": true, "r": true, "c": true,
				"C": true, "s": true, "x": false, "X": true,
				"o": false, "O": false,
				"f": true, "l": true, ":": true, "S": true, "P": true, "R": true,
				"v": true,
			},
		},
		{
			name: "idle row — neither e nor x available",
			m:    withListRows(Model{}, session.RunningSession{State: ""}),
			want: map[string]bool{
				"e": false, "T": true, "r": true, "c": true,
				"C": true, "s": true, "x": false, "X": true,
				"o": false, "O": false,
				"f": true, "l": true, ":": true, "S": true, "P": true, "R": true,
				"v": true,
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
