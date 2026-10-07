package history

import (
	"strings"
	"testing"
)

// TestEmptyResultsNameTheWayOut pins that an empty list says what to undo:
// esc clears only the text query, so a chip left from an earlier search must
// be named on its own or the operator has no reason to look for it.
func TestEmptyResultsNameTheWayOut(t *testing.T) {
	cases := []struct {
		name string
		in   SearchInput
		want string
	}{
		{"nothing to undo", SearchInput{}, "(no past sessions found — each profile's home_dir in config.toml says where to look)"},
		{"query only", SearchInput{Query: "x"}, "(no sessions match — esc clears the query)"},
		{"chip only", SearchInput{ProjectChip: "api"}, "(no sessions match — h/p/r/m change the filters)"},
		{"both", SearchInput{Query: "x", HarnessChip: "codex"}, "(no sessions match — esc clears the query · h/p/r/m change the filters)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.IndexReady = true
			if got := tc.in.RenderList(10); !strings.Contains(got, tc.want) {
				t.Fatalf("RenderList = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}
