package debug

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// A debug leaf that reads no selector lists none: its help would otherwise
// offer --stage and --trace to a command that ignores them.
func TestDebugLeavesWithoutSelectorsListNone(t *testing.T) {
	root := &cobra.Command{Use: "amx"}
	root.PersistentFlags().Bool("debug", false, "enable verbose debug output")
	root.AddCommand(debugCmd)
	t.Cleanup(func() { root.RemoveCommand(debugCmd) })
	for _, leaf := range []*cobra.Command{debugCaptureStopCmd, debugCaptureStatusCmd, debugSubscribeCmd} {
		var out bytes.Buffer
		leaf.SetOut(&out)
		if err := leaf.Usage(); err != nil {
			t.Fatal(err)
		}
		leaf.SetOut(nil)
		got := out.String()
		if strings.Contains(got, "Selectors:") || strings.Contains(got, "--stage") || !strings.Contains(got, "--debug") {
			t.Errorf("%s usage lists selectors or lost --debug:\n%s", leaf.CommandPath(), got)
		}
	}
}
