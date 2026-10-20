package overview

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

func assertFooterKey(t *testing.T, in Input, key string, available bool) {
	t.Helper()
	rows := renderLines(t, in)
	style := styleFooterUnavailableKey
	if available {
		style = styleFooterKey
	}
	if !strings.Contains(rows[len(rows)-1], style.Render(key)) {
		t.Fatalf("key %s available=%v not reflected in footer: %q", key, available, rows[len(rows)-1])
	}
}

// footerRow renders the row for a width, stripped, which is the form every
// claim below about what the row says is made in.
func footerRow(width int, sessions []session.RunningSession) string {
	in := viewInput(width, 30, sessions)
	return ansi.Strip(renderFooter(in, MeasureMode(in.Width, in.Height, false), 0))
}

// TestFooterGivesUpWholeEntriesAsTheTerminalNarrows is the width ladder.
//
// Three things are asserted at every width, and they are the whole contract the
// single row makes. It FITS, because the chrome reserves one line and a row that
// overflowed would be clipped mid-binding. What it drops is dropped WHOLE — a
// truncated entry is not a shorter hint, it is a key that does not exist — which
// is why the ellipsis is a failure here rather than a fallback. And it is
// MONOTONE: a narrower terminal says a subset of what a wider one said, so an
// operator who resizes never sees an entry appear, and never has to re-read the
// row to find out which half moved.
func TestFooterGivesUpWholeEntriesAsTheTerminalNarrows(t *testing.T) {
	labels := []string{"focus", "actions", "help", "quit", "jump", "needs you", "compact", "move", "next", "page", "ends"}
	essential := map[string]bool{"focus": true, "actions": true, "help": true, "quit": true}

	sessions := viewSessions(40)
	var previous map[string]bool
	for _, width := range []int{200, 160, 120, 100, 80, 70, 60, 40} {
		row := footerRow(width, sessions)
		if got := ansi.StringWidth(row); got > width {
			t.Fatalf("at %d columns the row is %d cells wide: %q", width, got, row)
		}
		if strings.Contains(row, "…") {
			t.Fatalf("at %d columns the row truncated an entry: %q", width, row)
		}

		present := map[string]bool{}
		for _, label := range labels {
			if strings.Contains(row, label) {
				present[label] = true
			}
		}
		for label := range essential {
			if !present[label] && !strings.Contains(row, bareFooterKeyFor(label)) {
				t.Fatalf("at %d columns the row lost the essential %q: %q", width, label, row)
			}
		}
		for label := range present {
			if previous != nil && !previous[label] {
				t.Fatalf("at %d columns %q appeared after a wider row dropped it: %q", width, label, row)
			}
		}
		previous = present
	}

	// The widest row says everything the table has for it, so the ladder above
	// starts from the full set rather than from whatever happened to fit.
	full := footerRow(200, sessions)
	for _, label := range labels {
		if !strings.Contains(full, label) {
			t.Fatalf("the unconstrained row omits %q: %q", label, full)
		}
	}
}

// bareFooterKeyFor is the last-ditch spelling of an essential entry: the row
// that has given up its labels still names these four keys.
func bareFooterKeyFor(label string) string {
	switch label {
	case "focus":
		return "↵"
	case "actions":
		return "a"
	case "help":
		return "?"
	default:
		return "q"
	}
}

// TestFooterFallsBackToBareKeysBeforeItLosesAKey is the bottom of the ladder. At
// a width that cannot hold the labelled essentials, the row respells all four of
// them as keys rather than dropping any: `↵ · a · ? · q` claims nothing about
// what the keys do, and that is the point — it is the last spelling that is
// still true.
func TestFooterFallsBackToBareKeysBeforeItLosesAKey(t *testing.T) {
	row := footerRow(20, viewSessions(4))
	for _, key := range []string{"↵", "a", "?", "q"} {
		if !strings.Contains(row, key) {
			t.Fatalf("the bare row lost %q: %q", key, row)
		}
	}
	for _, label := range []string{"focus", "actions", "help", "quit"} {
		if strings.Contains(row, label) {
			t.Fatalf("the bare row still carries the label %q: %q", label, row)
		}
	}
}

// TestFooterYieldsToAModalThatSpeaksForItself is the modal-composition rule: the
// row belongs to the base view, and under a modal none of its keys mean what it
// says they mean.
//
// Each of the four modals is checked for what it is: the three that print their
// own key line leave the row empty rather than repeating it, and the
// confirmation — which prints a question, a consequence, and nothing else — is
// given the keys that answer it. Dismissing the modal brings the base row back.
func TestFooterYieldsToAModalThatSpeaksForItself(t *testing.T) {
	for _, tc := range []struct {
		name string
		open []string
		want string
	}{
		{name: "keymap", open: []string{"?"}},
		{name: "menu", open: []string{"a"}},
		{name: "profile picker", open: []string{"a", "C"}},
		{name: "confirmation", open: []string{"a", "X"}, want: "y confirm · n/esc/q cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := actionModel(t, viewSessions(4), 0, hostTabElsewhere)
			base := hintRowOf(m)
			if base == "" {
				t.Fatal("the base view drew no hint row; the assertions below would be vacuous")
			}
			for _, name := range tc.open {
				m = press(t, m, name)
			}
			if got := hintRowOf(m); got != tc.want {
				t.Fatalf("under the %s the hint row is %q, want %q", tc.name, got, tc.want)
			}
			// Backing out is the reverse of opening: a cancelled profile picker
			// and a cancelled confirmation land back on the menu they were chosen
			// from, which is still a modal and still owns the row.
			for range tc.open {
				m = press(t, m, "esc")
			}
			if got := hintRowOf(m); got != base {
				t.Fatalf("leaving the %s left the row %q, want the base row %q", tc.name, got, base)
			}
		})
	}
}

func TestFooterEmptyAndSingleSessionAvailability(t *testing.T) {
	in := viewInput(160, 45, nil)
	for _, key := range []string{"enter", "1–0", "n", "N", "←↑↓→/hjkl", "tab", "pgup", "pgdn", "home", "end"} {
		assertFooterKey(t, in, key, false)
	}
	for _, key := range []string{"q", "c"} {
		assertFooterKey(t, in, key, true)
	}
	in.Sessions = viewSessions(1)
	assertFooterKey(t, in, "enter", true)
	assertFooterKey(t, in, "1–0", true)
	assertFooterKey(t, in, "←↑↓→/hjkl", false)
	assertFooterKey(t, in, "tab", false)
}

func TestFooterNeedsYouUsesDashboardSemantics(t *testing.T) {
	for _, compact := range []bool{false, true} {
		in := viewInput(160, 45, viewSessions(1))
		in.Compact = compact
		for _, tc := range []struct {
			state             string
			unseen, available bool
		}{
			{"bolt", false, false}, {"warning", false, false}, {"", false, false},
			{"stop", false, false}, {"stop", true, true},
			{"key", false, true}, {"question", false, true}, {"error", false, true},
		} {
			in.Sessions[0].State, in.Sessions[0].Unseen = tc.state, tc.unseen
			assertFooterKey(t, in, "n", tc.available)
			assertFooterKey(t, in, "N", tc.available)
		}
		rows := plain(renderLines(t, in))
		label := "c compact"
		if compact {
			label = "c normal"
		}
		if !strings.Contains(rows[len(rows)-1], label) {
			t.Fatalf("footer did not name the toggle's destination: %q", rows[len(rows)-1])
		}
	}
}

func TestFooterNavigationTracksBothEndsAndGeometry(t *testing.T) {
	for _, compact := range []bool{false, true} {
		in := viewInput(160, 45, viewSessions(40))
		in.Compact = compact
		for _, index := range []int{0, 39} {
			in.Selected = index
			assertFooterKey(t, in, "home", index != 0)
			assertFooterKey(t, in, "pgup", index != 0)
			assertFooterKey(t, in, "end", index != 39)
			assertFooterKey(t, in, "pgdn", index != 39)
			assertFooterKey(t, in, "tab", index != 39)
		}
	}
	// A one-tile normal page has no spatial neighbor. Compact mode makes room
	// for a second visible tile, so the same movement hint becomes available.
	in := viewInput(160, 15, viewSessions(4))
	in.Selected = 3
	assertFooterKey(t, in, "←↑↓→/hjkl", false)
	in.Compact = true
	assertFooterKey(t, in, "←↑↓→/hjkl", true)
}

func TestFooterDisablesFocusDuringRequestAndRestoresAfterFailure(t *testing.T) {
	m, _ := atSize(t, viewSessions(2), 0, 160, 45)
	assertFooterKey(t, overviewInput(m), "enter", true)
	assertFooterKey(t, overviewInput(m), "1–0", true)
	m = only(t, m, key(t, "enter"))
	assertFooterKey(t, overviewInput(m), "enter", false)
	assertFooterKey(t, overviewInput(m), "1–0", false)
	assertFooterKey(t, overviewInput(m), "q", true)
	m = only(t, m, commands.FocusDoneMsg{Err: errors.New("focus unavailable")})
	assertFooterKey(t, overviewInput(m), "enter", true)
	assertFooterKey(t, overviewInput(m), "1–0", true)
}
