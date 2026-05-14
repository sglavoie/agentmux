package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/cc-manager-kitty/internal/session"
)

func TestStateGroupClassification(t *testing.T) {
	now := time.Now()
	threshold := 5 * time.Minute

	cases := []struct {
		name     string
		state    string
		windowID int
		activity map[int]time.Time
		want     int
	}{
		{
			name:     "key state always attention regardless of activity",
			state:    "key",
			windowID: 1,
			activity: map[int]time.Time{1: now.Add(-time.Hour)}, // stale
			want:     0,
		},
		{
			name:     "stop state always attention",
			state:    "stop",
			windowID: 2,
			want:     0,
		},
		{
			name:     "bolt state always active",
			state:    "bolt",
			windowID: 3,
			activity: map[int]time.Time{3: now.Add(-time.Hour)}, // ignored
			want:     1,
		},
		{
			name:     "empty state with recent activity is Active",
			state:    "",
			windowID: 4,
			activity: map[int]time.Time{4: now.Add(-time.Minute)},
			want:     1,
		},
		{
			name:     "empty state with stale activity is Idle",
			state:    "",
			windowID: 5,
			activity: map[int]time.Time{5: now.Add(-time.Hour)},
			want:     2,
		},
		{
			name:     "empty state with no activity record is Active",
			state:    "",
			windowID: 6,
			activity: nil,
			want:     1,
		},
		{
			name:     "empty state, nil map, no record for this window is Active",
			state:    "",
			windowID: 7,
			activity: map[int]time.Time{99: now}, // record for different window
			want:     1,
		},
		{
			name:     "error state classified as Active",
			state:    "error",
			windowID: 8,
			want:     1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := session.RunningSession{State: tc.state, WindowID: tc.windowID}
			got := stateGroup(s, tc.activity, threshold)
			if got != tc.want {
				t.Errorf("stateGroup(%q, win=%d) = %d, want %d", tc.state, tc.windowID, got, tc.want)
			}
		})
	}
}

func TestStateGroupZeroThreshold(t *testing.T) {
	// A zero threshold disables the staleness check; empty-state sessions
	// with any activity record should still classify as Active.
	s := session.RunningSession{State: "", WindowID: 1}
	activity := map[int]time.Time{1: time.Now().Add(-time.Hour)}
	got := stateGroup(s, activity, 0)
	if got != 1 {
		t.Errorf("expected Active (1) with zero threshold, got %d", got)
	}
}

// ansiRE strips ANSI CSI escape sequences from a Lipgloss-rendered row so
// we can compare visual offsets independent of styling.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// terminalWidth approximates how many cells a string consumes when
// printed to a real terminal. go-runewidth treats U+FE0F as zero-width
// even though it forces emoji presentation (2 cells) on the preceding
// base character, so we add 1 per variation selector to compensate.
func terminalWidth(s string) int {
	w := runewidth.StringWidth(s)
	for _, r := range s {
		if r == '️' {
			w++
		}
	}
	return w
}

// idxBadge returns the visual cell offset of the first '[' character (the
// account badge opener) in a rendered row. Returns -1 if no '[' is found.
func idxBadge(row string) int {
	plain := ansiRE.ReplaceAllString(row, "")
	i := strings.IndexByte(plain, '[')
	if i < 0 {
		return -1
	}
	return terminalWidth(plain[:i])
}

// TestFormatRowIconColumnAlignment locks in that a row carrying the ⚠️
// failure icon aligns its account badge at the same visual column as a
// row carrying the ⚡ bolt icon. Without iconDisplayWidth's U+FE0F
// handling, ⚠️ would be measured as 1 cell and over-padded, shifting
// every following column right by one.
func TestFormatRowIconColumnAlignment(t *testing.T) {
	sBolt := session.RunningSession{State: "bolt", Account: "p", Base: "x"}
	sErr := session.RunningSession{State: "error", Account: "p", Base: "x"}
	rowBolt := formatRow(sBolt, nil, 0)
	rowErr := formatRow(sErr, nil, 0)

	iBolt := idxBadge(rowBolt)
	iErr := idxBadge(rowErr)
	if iBolt < 0 || iErr < 0 {
		t.Fatalf("badge '[' not found: bolt=%d err=%d (rows: %q / %q)", iBolt, iErr, rowBolt, rowErr)
	}
	if iBolt != iErr {
		t.Errorf("badge column misaligned: bolt=%d, error=%d", iBolt, iErr)
	}
}
