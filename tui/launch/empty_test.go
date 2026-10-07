package launch

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

// An empty launcher says what to do next: configure a project, or clear the
// filter that hid every one.
func TestEmptyLauncherNamesTheWayOut(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   PickerInput
		want string
	}{
		{"no projects", PickerInput{}, "add [[projects]] with `amx config edit`"},
		{"filter hid them", PickerInput{Sessions: []config.Session{{Title: "api"}}, Filter: "zzz"}, "esc clears the filter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.RenderList(10); !strings.Contains(got, tc.want) {
				t.Fatalf("RenderList = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}
