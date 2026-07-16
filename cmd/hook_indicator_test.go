package cmd

import (
	"bytes"
	"errors"
	"testing"

	"github.com/sglavoie/agentmux/internal/indicator"
)

func TestHookIndicatorParsesProducerHarness(t *testing.T) {
	oldRun := runHookIndicator
	oldWindowID := hookWindowID
	oldHarness := hookHarness
	oldRequestID := hookRequestID
	oldJSON := hookJSON
	oldStrict := hookStrict
	t.Cleanup(func() {
		runHookIndicator = oldRun
		hookWindowID = oldWindowID
		hookHarness = oldHarness
		hookRequestID = oldRequestID
		hookJSON = oldJSON
		hookStrict = oldStrict
		_ = hookIndicatorCmd.Flags().Set("window-id", "0")
		_ = hookIndicatorCmd.Flags().Set("harness", "")
		_ = hookIndicatorCmd.Flags().Set("request-id", "")
		_ = hookIndicatorCmd.Flags().Set("json", "false")
		_ = hookIndicatorCmd.Flags().Set("strict", "false")
		for _, name := range []string{"window-id", "harness", "request-id", "json", "strict"} {
			hookIndicatorCmd.Flags().Lookup(name).Changed = false
		}
	})

	var gotEvent, gotHarness, gotRequestID string
	var gotWindowID int
	runHookIndicator = func(event string, windowID int, producerHarness, _ string, options indicator.DeliveryOptions) indicator.DeliveryResult {
		gotEvent = event
		gotWindowID = windowID
		gotHarness = producerHarness
		gotRequestID = options.RequestID
		return indicator.DeliveryResult{Status: indicator.DeliveryAccepted}
	}
	if err := hookIndicatorCmd.ParseFlags([]string{"Stop", "--window-id=42", "--harness=future-harness", "--request-id=req-42"}); err != nil {
		t.Fatalf("parse generated hook command flags: %v", err)
	}
	args := hookIndicatorCmd.Flags().Args()
	if err := hookIndicatorCmd.Args(hookIndicatorCmd, args); err != nil {
		t.Fatalf("validate parsed args: %v", err)
	}
	if err := hookIndicatorCmd.RunE(hookIndicatorCmd, args); err != nil {
		t.Fatalf("RunE: %v", err)
	}

	if gotEvent != "Stop" || gotWindowID != 42 || gotHarness != "future-harness" || gotRequestID != "req-42" {
		t.Fatalf("indicator args = event %q window %d harness %q request %q", gotEvent, gotWindowID, gotHarness, gotRequestID)
	}
}

func TestHookIndicatorStrictJSONMapsDeliveryExitCodes(t *testing.T) {
	oldRun := runHookIndicator
	oldJSON, oldStrict := hookJSON, hookStrict
	t.Cleanup(func() {
		runHookIndicator = oldRun
		hookJSON, hookStrict = oldJSON, oldStrict
	})

	for _, tc := range []struct {
		name   string
		result indicator.DeliveryResult
		code   int
	}{
		{name: "temporary", result: indicator.DeliveryResult{SchemaVersion: 1, Status: indicator.DeliveryRetryableFailure, Retryable: true, Reason: "daemon_unavailable"}, code: 75},
		{name: "permanent", result: indicator.DeliveryResult{SchemaVersion: 1, Status: indicator.DeliveryPermanentFailure, Reason: "invalid_window"}, code: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runHookIndicator = func(string, int, string, string, indicator.DeliveryOptions) indicator.DeliveryResult {
				return tc.result
			}
			hookJSON, hookStrict = true, true
			var output bytes.Buffer
			hookIndicatorCmd.SetOut(&output)
			err := hookIndicatorCmd.RunE(hookIndicatorCmd, []string{"Stop"})
			var exitErr *ExitError
			if !errors.As(err, &exitErr) || exitErr.Code != tc.code || !exitErr.Quiet {
				t.Fatalf("RunE error = %#v; want quiet ExitError code %d", err, tc.code)
			}
			if output.Len() == 0 || !bytes.Contains(output.Bytes(), []byte(`"status"`)) {
				t.Fatalf("JSON output = %q", output.String())
			}
		})
	}
}

func TestHookIndicatorLegacyModeRemainsFailOpen(t *testing.T) {
	oldRun := runHookIndicator
	oldJSON, oldStrict := hookJSON, hookStrict
	t.Cleanup(func() {
		runHookIndicator = oldRun
		hookJSON, hookStrict = oldJSON, oldStrict
	})
	runHookIndicator = func(string, int, string, string, indicator.DeliveryOptions) indicator.DeliveryResult {
		return indicator.DeliveryResult{Status: indicator.DeliveryPermanentFailure, Reason: "unknown_event"}
	}
	hookJSON, hookStrict = false, false
	if err := hookIndicatorCmd.RunE(hookIndicatorCmd, []string{"FutureEvent"}); err != nil {
		t.Fatalf("legacy hook returned %v, want fail-open nil", err)
	}
}
