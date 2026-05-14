package tui

import (
	"testing"
	"time"

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
			name:     "unknown legacy state falls through to Idle",
			state:    "error",
			windowID: 8,
			want:     2,
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
