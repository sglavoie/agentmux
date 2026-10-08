package debug

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// A debug leaf's help names the record selectors it inherits from `amx debug`
// as Selectors, leaving Global Flags to the flags the root gives every command.
func TestDebugLeafUsageNamesSelectors(t *testing.T) {
	root := &cobra.Command{Use: "amx"}
	root.PersistentFlags().Bool("debug", false, "enable verbose debug output")
	root.AddCommand(debugCmd)
	t.Cleanup(func() { root.RemoveCommand(debugCmd) })

	var out bytes.Buffer
	debugTailCmd.SetOut(&out)
	t.Cleanup(func() { debugTailCmd.SetOut(nil) })
	if err := debugTailCmd.Usage(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	selectors, global, ok := strings.Cut(got, "\nGlobal Flags:\n")
	if !ok || !strings.Contains(selectors, "\nSelectors:\n") {
		t.Fatalf("usage has no Selectors section ahead of Global Flags:\n%s", got)
	}
	_, selectors, _ = strings.Cut(selectors, "\nSelectors:\n")
	for _, flag := range []string{"--stage", "--trace", "--window", "--after"} {
		if !strings.Contains(selectors, flag) || strings.Contains(global, flag) {
			t.Fatalf("%s is not listed under Selectors alone:\n%s", flag, got)
		}
	}
	if !strings.Contains(global, "--debug") || strings.Contains(selectors, "--debug") {
		t.Fatalf("--debug is not listed under Global Flags alone:\n%s", got)
	}
}
