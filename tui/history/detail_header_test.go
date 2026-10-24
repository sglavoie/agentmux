package history

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestHeaderNamesTheSessionWhereTheOperatorFindsIt pins that a Kitty session
// is named by its window, a tmux session by its runtime session rather than
// the process-local ID it is listed under, and the state in the dashboard
// row's words rather than its icon name.
func TestHeaderNamesTheSessionWhereTheOperatorFindsIt(t *testing.T) {
	kittyRow := session.RunningSession{WindowID: 42, Base: "api", State: "key"}
	tmuxRow := session.RunningSession{
		WindowID: 1 << 41, Base: "web", State: "bolt",
		Backend: runtimesession.BackendTmux, RuntimeSessionID: "rs-1",
	}
	cases := []struct {
		name            string
		row             session.RunningSession
		want, wantState string
		reject          string
	}{
		{name: "kitty", row: kittyRow, want: "— window 42", wantState: "Needs permission"},
		{name: "tmux", row: tmuxRow, want: "— tmux session rs-1", wantState: "Working", reject: "2199023255552"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := DetailInput{WindowID: tc.row.WindowID, Sessions: []session.RunningSession{tc.row}}
			header := in.Header()
			if !strings.Contains(header, tc.want) {
				t.Errorf("header lacks %q:\n%s", tc.want, header)
			}
			if !strings.Contains(header, tc.wantState) || strings.Contains(header, "state=") {
				t.Errorf("header does not name the state as %q:\n%s", tc.wantState, header)
			}
			if tc.reject != "" && strings.Contains(header, tc.reject) {
				t.Errorf("header shows the portable ID %s:\n%s", tc.reject, header)
			}
		})
	}

	gone := DetailInput{WindowID: 1 << 41}
	if header := gone.Header(); strings.Contains(header, "window") {
		t.Errorf("an unlisted session with no location still names a window:\n%s", header)
	}
}
