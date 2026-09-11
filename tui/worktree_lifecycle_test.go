package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/launch"
)

func lifecycleDriver(t *testing.T) (*uitestDriver, *uitestKittyClient) {
	t.Helper()
	driver, client := newUITestFixtureDriver(worktreeLifecycleFixture(), UITestSize{})
	cleanupUITestDriver(t, driver)
	if err := driver.Command(commands.ManageWorktrees); err != nil {
		t.Fatalf("enter Worktrees: %v", err)
	}
	return driver, client
}

func filterLifecycleWorktree(t *testing.T, driver *uitestDriver, id string) {
	t.Helper()
	if err := driver.Key("/"); err != nil {
		t.Fatalf("open filter: %v", err)
	}
	if err := driver.Text(id); err != nil {
		t.Fatalf("type filter: %v", err)
	}
	if err := driver.Key("enter"); err != nil {
		t.Fatalf("close filter: %v", err)
	}
	if got := len(driver.Model().worktreeFiltered); got != 1 {
		t.Fatalf("filter %q matched %d rows", id, got)
	}
}

func TestWorktreeReopenFocusedResumeAndFreshOutcomes(t *testing.T) {
	for _, tc := range []struct {
		id      string
		message string
		call    string
	}{
		{"wt-live", "focused live worktree session", "FocusTab"},
		{"wt-resume", "resumed newest worktree conversation", "LaunchTab"},
		{"wt-fresh", "launched fresh worktree session", "LaunchTab"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			driver, client := lifecycleDriver(t)
			filterLifecycleWorktree(t, driver, tc.id)
			if err := driver.Key("enter"); err != nil {
				t.Fatalf("reopen: %v", err)
			}
			model := driver.Model()
			if model.mode != ModeWorktrees || model.worktreeActionInFlight {
				t.Fatalf("reopen left mode=%v inFlight=%v", model.mode, model.worktreeActionInFlight)
			}
			if len(model.toasts) == 0 {
				t.Fatal("reopen result did not publish a toast")
			}
			if status := currentStatusMessage(model, model.toasts[0].Expires.Add(-1)); !strings.Contains(status.Text, tc.message) {
				t.Fatalf("status %q does not contain %q", status.Text, tc.message)
			}
			if len(client.CallsFor(tc.call)) != 1 {
				t.Fatalf("%s calls = %+v", tc.call, client.Calls)
			}
		})
	}
}

func TestWorktreeAmbiguousLiveSessionPickerFocusAndCancel(t *testing.T) {
	driver, client := lifecycleDriver(t)
	filterLifecycleWorktree(t, driver, "wt-multi")
	selected := driver.Model().worktreeSelectedID
	if err := driver.Key("enter"); err != nil {
		t.Fatalf("open live picker: %v", err)
	}
	model := driver.Model()
	if model.mode != ModeLiveSessionPicker || len(model.livePickerCandidates) != 2 {
		t.Fatalf("picker state = mode %v candidates %+v", model.mode, model.livePickerCandidates)
	}
	view := stripAnsi(renderLiveSessionPicker(model))
	for _, want := range []string{"candidate 81", "candidate 82", "native-multi-81", "work/claude", "personal/codex"} {
		if !strings.Contains(view, want) {
			t.Errorf("picker missing %q:\n%s", want, view)
		}
	}
	if err := driver.Key("esc"); err != nil {
		t.Fatalf("cancel picker: %v", err)
	}
	if driver.Model().mode != ModeWorktrees || driver.Model().worktreeSelectedID != selected || len(client.CallsFor("FocusTab")) != 0 {
		t.Fatalf("cancel changed selection or focused: model=%+v calls=%+v", driver.Model(), client.Calls)
	}
	if err := driver.Key("enter"); err != nil {
		t.Fatalf("reopen picker: %v", err)
	}
	if err := driver.Key("j"); err != nil {
		t.Fatalf("move picker: %v", err)
	}
	if err := driver.Key("enter"); err != nil {
		t.Fatalf("focus picker choice: %v", err)
	}
	if calls := client.CallsFor("FocusTab"); len(calls) != 1 || calls[0].Match != "id:82" {
		t.Fatalf("focus calls = %+v", calls)
	}
}

func TestWorktreeReplacementProfilePurposeReturnsToWorktrees(t *testing.T) {
	driver, client := lifecycleDriver(t)
	filterLifecycleWorktree(t, driver, "wt-profile")
	if err := driver.Key("enter"); err != nil {
		t.Fatalf("request reopen: %v", err)
	}
	model := driver.Model()
	if model.mode != ModeConfigPicker || model.configPickerPurpose != launch.ConfigPickerWorktreeReopen || model.configPickerReturnMode != ModeWorktrees || model.worktreeActionTarget == nil {
		t.Fatalf("profile detour state = mode %v purpose %v return %v target %+v", model.mode, model.configPickerPurpose, model.configPickerReturnMode, model.worktreeActionTarget)
	}
	if err := driver.Key("enter"); err != nil {
		t.Fatalf("select replacement profile: %v", err)
	}
	if driver.Model().mode != ModeWorktrees || driver.Model().worktreeActionTarget != nil || len(client.CallsFor("LaunchTab")) != 1 {
		t.Fatalf("replacement did not return cleanly: mode=%v target=%+v calls=%+v", driver.Model().mode, driver.Model().worktreeActionTarget, client.Calls)
	}
}

func TestWorktreeCleanupSnapshotRefusalsAndConfirmationText(t *testing.T) {
	for _, tc := range []struct {
		id, key, reason string
	}{
		{"wt-dirty", "d", "worktree is dirty"},
		{"wt-looping", "d", "worktree is active"},
		{"wt-unmerged", "D", "not merged"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			driver, _ := lifecycleDriver(t)
			filterLifecycleWorktree(t, driver, tc.id)
			if err := driver.Key(tc.key); err != nil {
				t.Fatalf("cleanup key: %v", err)
			}
			model := driver.Model()
			if model.mode != ModeWorktrees || len(model.toasts) == 0 || !strings.Contains(model.toasts[0].Text, tc.reason) {
				t.Fatalf("refusal = mode %v toasts %+v", model.mode, model.toasts)
			}
		})
	}

	driver, _ := lifecycleDriver(t)
	filterLifecycleWorktree(t, driver, "wt-delete")
	if err := driver.Key("D"); err != nil {
		t.Fatalf("open delete confirmation: %v", err)
	}
	confirmation := stripAnsi(renderConfirm(driver.Model()))
	for _, want := range []string{"delete branch", "amx/delete", "recorded target", "main"} {
		if !strings.Contains(confirmation, want) {
			t.Errorf("confirmation missing %q:\n%s", want, confirmation)
		}
	}
}

func TestWorktreeRemoveForgetAndPartialRecoveryRefreshSelection(t *testing.T) {
	for _, tc := range []struct {
		id, key string
	}{
		{"wt-cleanup", "d"},
		{"wt-delete", "D"},
		{"wt-missing", "F"},
		{"wt-invalid", "F"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			driver, _ := lifecycleDriver(t)
			filterLifecycleWorktree(t, driver, tc.id)
			if err := driver.Key(tc.key); err != nil {
				t.Fatalf("open confirmation: %v", err)
			}
			if driver.Model().mode != ModeConfirm {
				t.Fatalf("mode = %v, want confirm", driver.Model().mode)
			}
			if err := driver.Key("y"); err != nil {
				t.Fatalf("confirm: %v", err)
			}
			model := driver.Model()
			if model.mode != ModeWorktrees || len(model.worktreeFiltered) != 0 || model.worktreeActionInFlight {
				t.Fatalf("completion state = mode %v filtered=%d inFlight=%v", model.mode, len(model.worktreeFiltered), model.worktreeActionInFlight)
			}
		})
	}

	driver, _ := lifecycleDriver(t)
	filterLifecycleWorktree(t, driver, "wt-partial")
	if err := driver.Key("d"); err != nil {
		t.Fatalf("partial confirm: %v", err)
	}
	if err := driver.Key("y"); err != nil {
		t.Fatalf("partial execute: %v", err)
	}
	model := driver.Model()
	if model.mode != ModeWorktrees || len(model.toasts) == 0 || !strings.Contains(model.toasts[0].Text, "checkout removed") || !strings.Contains(model.toasts[0].Text, "recovery required") {
		t.Fatalf("partial result not surfaced: mode=%v toasts=%+v", model.mode, model.toasts)
	}
	if len(model.worktreeFiltered) != 1 || string(model.worktreeFiltered[0].Classification) != "missing" {
		t.Fatalf("partial refresh did not preserve retained row: %+v", model.worktreeFiltered)
	}
}

func TestWorktreeSessionPickerResponsiveLayouts(t *testing.T) {
	driver, _ := lifecycleDriver(t)
	filterLifecycleWorktree(t, driver, "wt-multi")
	if err := driver.Key("enter"); err != nil {
		t.Fatalf("open picker: %v", err)
	}
	for _, size := range []UITestSize{{Width: 48, Height: 12}, {Width: 132, Height: 34}} {
		model := driver.Model()
		model.width, model.height = size.Width, size.Height
		issues := detectModalFitIssues(model)
		if len(issues) != 0 {
			t.Errorf("%+v issues: %+v", size, issues)
		}
	}
}
