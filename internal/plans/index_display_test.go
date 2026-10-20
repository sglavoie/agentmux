package plans

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/textutil"
)

func TestDisplayName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		{"add-ability-to-search-drifting-beacon-4ca2183c5850", "add-ability-to-search-drifting-beacon"},
		{"agent-mux-memory-exhaustion-fix-9-sessions-8027329421d8-5d44126d1695", "agent-mux-memory-exhaustion-fix-9-sessions"},
		{"plan-deadbeef00ff", "plan"},
		{"plan-x-y-z", "plan-x-y-z"},
		{"plan", "plan"},
		{"", ""},
		{"plan-not12hex-aabbccddeeff", "plan-not12hex"},
		{"plan-zzzzzzzzzzzz", "plan-zzzzzzzzzzzz"},       // non-hex chars: not stripped
		{"plan-aabb1122ccdd-ee", "plan-aabb1122ccdd-ee"}, // trailing isn't 12 hex
	}
	for _, tc := range cases {
		got := DisplayName(tc.in)
		if got != tc.want {
			t.Errorf("DisplayName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTruncateSnippetBoundary(t *testing.T) {
	t.Parallel()

	exact := strings.Repeat("a", snippetMaxLen)
	if got := textutil.Snippet(exact, snippetMaxLen); got != exact {
		t.Errorf("Snippet(exact length) = %q, want %q", got, exact)
	}

	over := exact + "b"
	want := exact + "…"
	if got := textutil.Snippet(over, snippetMaxLen); got != want {
		t.Errorf("Snippet(over length) = %q, want %q", got, want)
	}
}
