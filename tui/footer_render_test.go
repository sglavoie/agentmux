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
	for _, want := range []string{"ctrl+j/k", "ctrl+n/p up/down", "enter run", "esc close", "ctrl+c quit"} {
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

// The dashboard pins its footer to the final row and rests the feedback slot
// directly on the list above it.
func TestRootRenderPinsTheDashboardFooterToTheFinalRow(t *testing.T) {
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
		t.Fatalf("footer row width = %d, want %d; row %q", got, m.width, last)
	}
	for _, want := range []string{"j/k up/down", "q quit", "i inspector"} {
		if !strings.Contains(last, want) {
			t.Fatalf("final row missing %q:\n%s", want, last)
		}
	}

	// One physical row, whatever the palette had to shed to get there.
	if got := len(rootFooterLines(m)); got != 1 {
		t.Fatalf("dashboard footer spent %d rows, want 1", got)
	}
	// And the reserved feedback slot above it is blank on an idle dashboard.
	feedback := lines[len(lines)-2]
	if got := strings.TrimSpace(feedback); got != "" {
		t.Fatalf("feedback slot = %q on an idle dashboard, want blank", got)
	}
	if got := runewidth.StringWidth(feedback); got != m.width {
		t.Fatalf("feedback row width = %d, want %d", got, m.width)
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
	// The dashboard's feedback slot is the row above the footer.
	if slot := strings.TrimSpace(lines[len(lines)-2]); !strings.Contains(slot, "launch finished") {
		t.Fatalf("feedback slot = %q, want latest toast status", slot)
	}
	rest := strings.Join(append(append([]string{}, lines[:len(lines)-2]...), lines[len(lines)-1]), "\n")
	if strings.Contains(rest, "older toast") {
		t.Fatalf("older toast rendered outside the feedback slot:\n%s", rest)
	}
}

// TestRootRenderListKeepsItsFooterBesideTheInspector reverses the rule the
// preview pane shipped with, and the reversal is required rather than cosmetic.
//
// The preview was opt-in, so an operator who turned it on had already read the
// footer; suppressing it bought the pane four rows. The inspector is on before
// the first keypress, so the same rule would give a first-run dashboard no key
// hints at all — there would be nothing on screen naming the key that turns the
// pane off. The footer also cannot be conditioned on the inspector for a
// mechanical reason: its own height is a term in the body-row floor the split is
// judged against, so a footer that asked whether the inspector fits would be
// asking itself.
func TestRootRenderListKeepsItsFooterBesideTheInspector(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.width = 140
	m.height = 30
	requireInspector(t, m)

	out := stripAnsi(m.render())
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("rendered line count = %d, want terminal height %d:\n%s", len(lines), m.height, out)
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if !strings.Contains(last, "j/k up/down") {
		t.Fatalf("final row = %q, want the dashboard footer", last)
	}
	if !strings.Contains(out, "q") || !strings.Contains(out, "quit") {
		t.Fatalf("the footer went missing beside the inspector:\n%s", out)
	}
	// The key that hides the pane is named by the footer now rather than by a
	// second copy in the header.
	if !strings.Contains(out, "i inspector") {
		t.Fatalf("nothing on screen names the key that hides the pane:\n%s", out)
	}
}

// TestListFooterFitsTerminalWidth asserts that no emitted row is wider than the
// terminal at any supported band, including the width the drop loop cannot
// reduce further because only essentials remain.
func TestListFooterFitsTerminalWidth(t *testing.T) {
	for _, width := range []int{40, 60, 79, 80, 119, 120, 160, 260} {
		m := withListRows(Model{width: width}, session.RunningSession{State: "bolt"})
		for i, line := range listFooterRows(m) {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("width %d: row %d width %d exceeds terminal: %q", width, i, w, line)
			}
		}
	}
}

// TestListFooterZeroWidthEmitsOneUnwrappedRow asserts that a zero-width model
// (before the first WindowSizeMsg) emits a single unwrapped row carrying the
// whole palette. There is no width to budget against yet, so dropping hints
// would hide keys for a terminal size nothing has reported.
func TestListFooterZeroWidthEmitsOneUnwrappedRow(t *testing.T) {
	rows := listFooterRows(withListRows(Model{}, session.RunningSession{State: "bolt"}))
	if len(rows) != 1 {
		t.Fatalf("expected one row at zero width, got %d:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	out := stripAnsi(rows[0])
	if strings.Contains(out, footerHintMore) {
		t.Errorf("zero width dropped hints and marked overflow: %q", out)
	}
	// One hint from each priority band, so a regression that sheds a whole band
	// at zero width is what this catches rather than one entry going missing.
	for _, want := range []string{"j/k up/down", "a actions", "n/N needs you", "l launch", "S past sessions", "s project's past sessions"} {
		if !strings.Contains(out, want) {
			t.Errorf("zero-width footer missing %q: %q", want, out)
		}
	}
}

// cursorIndependentFooterKeys are the palette bindings that do nothing to the
// cursor row and so are available in every state, including an empty list. They
// are listed once rather than repeated in every case below, which is what makes
// a key that quietly becomes cursor-dependent show up as a missing case entry.
//
// It is short now because the small keymap is: the per-row command keys the
// footer used to name one by one (`e`, `r`, `c`, `C`, `x`, `X`, `o`, `O`, …)
// are entries in the actions overlay behind `a`, and the overlay carries its own
// per-entry availability. What is left here is the app-level half of the row.
var cursorIndependentFooterKeys = map[string]bool{
	"j/k": true, "enter": true, ":": true, "/": true, "?": true,
	"esc": true, "tab": true,
	"i": true, "p": true, "l": true, "q": true, "S": true,
}

// TestListFooterAvailability verifies the per-entry availability rules for the
// candidates whose answer depends on the cursor row: `a` opens the actions
// overlay, `s` opens project history, and `n`/`N` dims when nothing on
// the dashboard is waiting on a human.
func TestListFooterAvailability(t *testing.T) {
	cases := []struct {
		name string
		m    Model
		want map[string]bool // expected availability per cursor-dependent key
	}{
		{
			name: "empty list — only global actions available",
			m:    Model{},
			want: map[string]bool{"t": false, "a": false, "s": false, "n/N": false, "u": false},
		},
		{
			name: "attention row — actions and needs-you both live",
			m:    withListRows(Model{}, session.RunningSession{State: "key"}),
			want: map[string]bool{"t": false, "a": true, "s": true, "n/N": true, "u": false},
		},
		{
			name: "active row — actions live, nothing waiting to jump to",
			m:    withListRows(Model{}, session.RunningSession{State: "bolt"}),
			want: map[string]bool{"t": false, "a": true, "s": true, "n/N": false, "u": false},
		},
		{
			name: "idle row — actions live, nothing waiting to jump to",
			m:    withListRows(Model{}, session.RunningSession{State: ""}),
			want: map[string]bool{"t": false, "a": true, "s": true, "n/N": false, "u": false},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, e := range listFooterBindings() {
				want, ok := tc.want[e.key]
				if !ok {
					if !cursorIndependentFooterKeys[e.key] {
						t.Fatalf("test case missing entry for key %q", e.key)
					}
					want = true
				}
				got := e.availFn(tc.m)
				if got != want {
					t.Errorf("availability for %q (%s): got %v, want %v", e.key, e.label, got, want)
				}
			}
		})
	}
}
