package rendering

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func TestDesiredIconPrefersTheRecordedDecision(t *testing.T) {
	tests := []struct {
		name string
		st   *state.WindowState
		want string
	}{
		{name: "no window", st: nil, want: ""},
		{
			name: "recorded decision wins over the legacy scalar",
			st:   &state.WindowState{StateSet: true, DesiredIcon: "bolt", LastAction: "stop"},
			want: "bolt",
		},
		{
			// The reducer may legitimately clear an icon while LastAction still
			// holds the stale value that produced the previous one.
			name: "a recorded clear is authoritative even when empty",
			st:   &state.WindowState{StateSet: true, DesiredIcon: "", LastAction: "stop"},
			want: "",
		},
		{
			name: "an unrecorded window falls back to the legacy scalar",
			st:   &state.WindowState{LastAction: "stop"},
			want: "stop",
		},
		{
			name: "an unrecorded window with no action has no icon",
			st:   &state.WindowState{},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := DesiredIcon(tc.st); got != tc.want {
				t.Fatalf("DesiredIcon = %q, want %q", got, tc.want)
			}
		})
	}
}
