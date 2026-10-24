package plans

import (
	"errors"
	"strings"
	"testing"

	planindex "github.com/sglavoie/agentmux/internal/plans"
)

func TestEmptyPlanResultsNameTheWayOut(t *testing.T) {
	cases := []struct {
		name string
		in   SearchInput
		want string
	}{
		{"nothing to undo", SearchInput{}, "(no saved plans found — plans_directory in config.toml says where to look)"},
		{"query only", SearchInput{Query: "x"}, "(no plans match — esc clears the search)"},
		{"project filter", SearchInput{FilterProject: planindex.ProjectFilter{Kind: planindex.ProjectUnknown}}, "(no plans match — r changes the project)"},
		{"first load failed", SearchInput{RefreshErr: errors.New("permission denied")}, "(could not load plans — esc, then reopen Plans to retry)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.RenderList(10); !strings.Contains(got, tc.want) {
				t.Fatalf("RenderList = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}
