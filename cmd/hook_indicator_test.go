package cmd

import "testing"

func TestHookIndicatorParsesProducerHarness(t *testing.T) {
	oldRun := runHookIndicator
	oldWindowID := hookWindowID
	oldHarness := hookHarness
	t.Cleanup(func() {
		runHookIndicator = oldRun
		hookWindowID = oldWindowID
		hookHarness = oldHarness
		_ = hookIndicatorCmd.Flags().Set("window-id", "0")
		_ = hookIndicatorCmd.Flags().Set("harness", "")
		hookIndicatorCmd.Flags().Lookup("window-id").Changed = false
		hookIndicatorCmd.Flags().Lookup("harness").Changed = false
	})

	var gotEvent, gotHarness string
	var gotWindowID int
	runHookIndicator = func(event string, windowID int, producerHarness, _ string) error {
		gotEvent = event
		gotWindowID = windowID
		gotHarness = producerHarness
		return nil
	}
	if err := hookIndicatorCmd.ParseFlags([]string{"Stop", "--window-id=42", "--harness=future-harness"}); err != nil {
		t.Fatalf("parse generated hook command flags: %v", err)
	}
	args := hookIndicatorCmd.Flags().Args()
	if err := hookIndicatorCmd.Args(hookIndicatorCmd, args); err != nil {
		t.Fatalf("validate parsed args: %v", err)
	}
	if err := hookIndicatorCmd.RunE(hookIndicatorCmd, args); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	if gotEvent != "Stop" || gotWindowID != 42 || gotHarness != "future-harness" {
		t.Fatalf("indicator args = event %q window %d harness %q", gotEvent, gotWindowID, gotHarness)
	}
}
