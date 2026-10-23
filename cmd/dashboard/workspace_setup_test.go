package dashboard

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestWorkspaceSetupScrubsIdentityAndKeepsSummaryUntilReturn(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "configured", true: "partial failure"}[failure], func(t *testing.T) {
			borrowedIdentity(t)
			original := workspaceViewSetup
			t.Cleanup(func() { workspaceViewSetup = original })
			calls := 0
			problem := errors.New("hook sync failed; configuration saved")
			workspaceViewSetup = func(cmd *cobra.Command) error {
				calls++
				requireNoBorrowedIdentity(t, os.Environ())
				cmd.Println("Project configured. CLI: tmux")
				if failure {
					return problem
				}
				return nil
			}
			cmd := &cobra.Command{}
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetIn(strings.NewReader("\n"))
			err := runWorkspaceSetup(cmd, nil)
			if failure != errors.Is(err, problem) {
				t.Fatalf("failure=%v: %v", failure, err)
			}
			if calls != 1 || !strings.Contains(output.String(), "Press Enter to return") || !strings.Contains(output.String(), "Project configured") {
				t.Fatalf("setup lost its summary or was repeated: %s", output.String())
			}
			if failure && !strings.Contains(output.String(), problem.Error()) {
				t.Fatal("failure hidden")
			}
		})
	}
}
