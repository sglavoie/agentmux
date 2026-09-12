package overview

import (
	"errors"
	"strings"
	"testing"

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

func TestFooterEmptyAndSingleSessionAvailability(t *testing.T) {
	in := viewInput(160, 45, nil)
	for _, key := range []string{"Enter", "n", "N", "←↑↓→/hjkl", "Tab", "PgUp", "PgDn", "Home", "End"} {
		assertFooterKey(t, in, key, false)
	}
	for _, key := range []string{"q", "c"} {
		assertFooterKey(t, in, key, true)
	}
	in.Sessions = viewSessions(1)
	assertFooterKey(t, in, "Enter", true)
	assertFooterKey(t, in, "←↑↓→/hjkl", false)
	assertFooterKey(t, in, "Tab", false)
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
			assertFooterKey(t, in, "Home", index != 0)
			assertFooterKey(t, in, "PgUp", index != 0)
			assertFooterKey(t, in, "End", index != 39)
			assertFooterKey(t, in, "PgDn", index != 39)
			assertFooterKey(t, in, "Tab", index != 39)
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
	assertFooterKey(t, overviewInput(m), "Enter", true)
	m = only(t, m, key(t, "enter"))
	assertFooterKey(t, overviewInput(m), "Enter", false)
	assertFooterKey(t, overviewInput(m), "q", true)
	m = only(t, m, commands.FocusDoneMsg{Err: errors.New("focus unavailable")})
	assertFooterKey(t, overviewInput(m), "Enter", true)
}
