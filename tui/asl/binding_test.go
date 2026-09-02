package asl

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/aslbridge"
)

func TestASLWorkbenchFocusMapping(t *testing.T) {
	cases := map[string]string{
		"paused-for-reply": aslbridge.FocusReply,
		"verifying":        aslbridge.FocusVerification,
		"failed":           aslbridge.FocusEvents,
		"stale":            aslbridge.FocusEvents,
		"invalid":          aslbridge.FocusEvents,
		"running":          aslbridge.FocusActive,
		"":                 aslbridge.FocusActive,
	}
	for state, want := range cases {
		if got := WorkbenchFocus(state); got != want {
			t.Errorf("WorkbenchFocus(%q) = %q, want %q", state, got, want)
		}
	}
}
