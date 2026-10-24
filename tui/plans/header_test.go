package plans

import (
	"strings"
	"testing"

	planindex "github.com/sglavoie/agentmux/internal/plans"
)

// TestHeaderKeepsTheCountWhileLoading pins that the Plans header keeps its
// count and sort while a query or a scan runs, and says "scanning…" only for
// a real scan of the plan files.
func TestHeaderKeepsTheCountWhileLoading(t *testing.T) {
	results := []planindex.Result{{}, {}}
	cases := []struct {
		name        string
		in          SearchInput
		want        []string
		wantMissing string
	}{
		{"at rest", SearchInput{Results: results}, []string{"[2 results · sort: "}, "…)"},
		{"query", SearchInput{Results: results, Loading: true}, []string{"[2 results · sort: ", "(searching…)"}, "scanning"},
		{"scan", SearchInput{Results: results, Loading: true, Scanning: true}, []string{"[2 results · sort: ", "(scanning…)"}, "searching"},
		{"first scan", SearchInput{Loading: true, Scanning: true}, []string{"(scanning…)"}, "results"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			header := strings.SplitN(tc.in.Render(), "\n", 2)[0]
			for _, want := range tc.want {
				if !strings.Contains(header, want) {
					t.Errorf("header %q lacks %q", header, want)
				}
			}
			if strings.Contains(header, tc.wantMissing) {
				t.Errorf("header %q should not contain %q", header, tc.wantMissing)
			}
		})
	}
}
