package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
)

const (
	lifecycleParentChildWindow   = 42
	lifecycleCodexPipeWindow     = 43
	lifecycleCopilotPipeWindow   = 44
	lifecycleConcurrentWindow    = 45
	lifecycleProtectedWindow     = 46
	lifecycleNativeBeforeWindow  = 47
	lifecycleNativeAfterWindow   = 48
	lifecycleClaudeInteractive   = 49
	lifecycleCodexInteractive    = 50
	lifecycleCopilotInteractive  = 51
	lifecycleRestartWindow       = 52
	lifecycleASLWindow           = 53
	lifecycleResetWindow         = 54
	lifecycleStopHoldWindow      = 55
	lifecycleAcceptanceKittyAddr = "unix:/tmp/agent-mux-process-lifecycle-acceptance-kitty"
)

// The confirmed-idle gate is configured far longer than this fixture runs, so a
// held stop can only leave the gate by being bypassed, never by timing out.
const (
	lifecycleAcceptanceStopConfirmDelay   = 300
	lifecycleAcceptanceStopConfirmTimeout = 600
)

// TestAcceptanceHarnessProcessLifecyclePipeline crosses the production Unix
// socket, persistence, process-inspection, reducer, diagnostic, and render
// boundaries with deterministic local substitutes. A PATH-local ps supplies
// controllable PID/lstart snapshots and a fake kitten records real amx_state
// and title writes. No harness provider, model, notification backend, Git
// repository, or live Kitty process is contacted.
func TestAcceptanceHarnessProcessLifecyclePipeline(t *testing.T) {
	fixture := newProcessLifecycleAcceptanceFixture(t)

	parent := lifecycleAcceptanceIdentity("claude", 4201)
	claudePipe := lifecycleAcceptanceIdentity("claude", 4202)
	codexPipe := lifecycleAcceptanceIdentity("codex", 4301)
	copilotPipe := lifecycleAcceptanceIdentity("copilot", 4401)
	concurrentA := lifecycleAcceptanceIdentity("codex", 4501)
	concurrentB := lifecycleAcceptanceIdentity("codex", 4502)
	protected := lifecycleAcceptanceIdentity("claude", 4601)
	nativeBefore := lifecycleAcceptanceIdentity("codex", 4701)
	nativeAfter := lifecycleAcceptanceIdentity("claude", 4801)
	interactiveClaude := lifecycleAcceptanceIdentity("claude", 4901)
	interactiveCodex := lifecycleAcceptanceIdentity("codex", 5001)
	interactiveCopilot := lifecycleAcceptanceIdentity("copilot", 5101)
	restoredPipe := lifecycleAcceptanceIdentity("claude", 5201)
	aslPipe := lifecycleAcceptanceIdentity("claude", 5301)
	resetProcess := lifecycleAcceptanceIdentity("copilot", 5401)
	stopHoldProcess := lifecycleAcceptanceIdentity("claude", 5501)

	for _, identity := range []daemon.HarnessProcessIdentity{
		parent, claudePipe, codexPipe, copilotPipe, concurrentA, concurrentB,
		protected, nativeBefore, interactiveClaude, interactiveCodex,
		interactiveCopilot, restoredPipe, aslPipe, resetProcess, stopHoldProcess,
	} {
		fixture.setProcess(identity.PID, identity.StartToken)
	}
	fixture.startDaemon()

	// Parent plus pipe child: the child must never displace the interactive
	// conversation owner, and its exit must reveal the still-live parent.
	fixture.sendHook(lifecycleParentChildWindow, "claude", "PreToolUse", "parent-42", 1, false, &parent, nil)
	fixture.sendHook(lifecycleParentChildWindow, "claude", "PreToolUse", "pipe-42", 1, true, &claudePipe, nil)

	// The three supported pipe shapes. The executable/flag discovery itself is
	// covered in internal/indicator; this fixture starts at the validated IPC
	// identity boundary so liveness is completely controllable.
	fixture.sendHook(lifecycleCodexPipeWindow, "codex", "PreToolUse", "pipe-43", 1, true, &codexPipe, nil)
	fixture.sendHook(lifecycleCopilotPipeWindow, "copilot", "userPromptSubmitted", "pipe-44", 1, true, &copilotPipe, nil)

	// Simultaneous children share a window but retain exact fingerprints.
	fixture.sendHook(lifecycleConcurrentWindow, "codex", "PreToolUse", "pipe-45-a", 1, true, &concurrentA, nil)
	fixture.sendHook(lifecycleConcurrentWindow, "codex", "PreToolUse", "pipe-45-b", 1, true, &concurrentB, nil)

	// One disposable active source makes the group pollable while terminal
	// failure and question sources prove acknowledgement protection.
	fixture.sendHook(lifecycleProtectedWindow, "claude", "PreToolUse", "active-46", 1, true, &protected, nil)
	fixture.sendHook(lifecycleProtectedWindow, "claude", "StopFailure", "failure-46", 1, true, &protected, nil)
	fixture.sendHook(lifecycleProtectedWindow, "claude", "Elicitation", "question-46", 1, true, &protected, nil)

	fixture.sendHook(lifecycleNativeBeforeWindow, "codex", "PreToolUse", "native-47", 1, true, &nativeBefore, nil)
	fixture.sendHook(lifecycleClaudeInteractive, "claude", "PreToolUse", "interactive-49", 1, false, &interactiveClaude, nil)
	fixture.sendHook(lifecycleCodexInteractive, "codex", "PreToolUse", "interactive-50", 1, false, &interactiveCodex, nil)
	fixture.sendHook(lifecycleCopilotInteractive, "copilot", "userPromptSubmitted", "interactive-51", 1, false, &interactiveCopilot, nil)
	fixture.sendHook(lifecycleRestartWindow, "claude", "PreToolUse", "restart-52", 1, true, &restoredPipe, nil)
	fixture.sendHook(lifecycleStopHoldWindow, "claude", "PreToolUse", "hold-55", 1, false, &stopHoldProcess, nil)

	fixture.sendHook(lifecycleASLWindow, "claude", "AgentSessionLoopTurnStarted", "", 1, false, nil, map[string]any{
		"source":          "agent_session_loop",
		"run_id":          "acceptance-run-53",
		"cwd":             fixture.workspace(lifecycleASLWindow),
		"harness":         "claude",
		"kitty_window_id": lifecycleASLWindow,
	})
	fixture.sendHook(lifecycleASLWindow, "claude", "PreToolUse", "pipe-53", 1, true, &aslPipe, nil)

	// A reset-marked prompt leaves a live process fact under reset ownership;
	// process exit must clear it without synthesizing a stop.
	fixture.sendHook(lifecycleResetWindow, "copilot", "userPromptSubmitted", "reset-54", 1, false, &resetProcess, map[string]any{
		"prompt": "/clear",
	})

	initial := fixture.waitForExport("initial process facts persisted and rendered", func(export daemon.EventsExport) bool {
		meta := export.WindowMetadata[lifecycleRestartWindow]
		return lifecycleAcceptanceFact(meta, daemon.SourceHarness, "claude:restart-52") != nil &&
			meta.Renderer.PersistedGen > 0 && meta.Renderer.RenderedGen >= meta.Renderer.PersistedGen &&
			export.WindowMetadata[lifecycleASLWindow].Shadow.EffectiveLooping &&
			lifecycleAcceptanceFact(export.WindowMetadata[lifecycleASLWindow], daemon.SourceASL, "acceptance-run-53") != nil
	})
	if got := initial.WindowMetadata[lifecycleParentChildWindow].Shadow.EffectiveIcon; got != "bolt" {
		t.Fatalf("parent/child initial icon = %q, want bolt", got)
	}

	// A native terminal that lands before the confirming snapshot remains
	// authoritative evidence. Stop is reset-sensitive and deferred briefly, so
	// wait for the clean terminal fact before exposing process absence.
	fixture.sendHook(lifecycleNativeBeforeWindow, "codex", "Stop", "native-47", 2, true, &nativeBefore, map[string]any{
		"stop_reason": "end_turn",
	})
	fixture.waitForExport("native terminal applied before inspection", func(export daemon.EventsExport) bool {
		fact := lifecycleAcceptanceFact(export.WindowMetadata[lifecycleNativeBeforeWindow], daemon.SourceHarness, "codex:native-47")
		return fact != nil && fact.Action == "stop" && fact.Terminated
	})

	// An interactive native terminal enters the confirmed-idle gate instead of
	// painting. Nothing here can confirm it and the configured deadline outlives
	// the fixture, so the only way it can leave the gate is the process exit.
	fixture.sendHook(lifecycleStopHoldWindow, "claude", "Stop", "hold-55", 2, false, &stopHoldProcess, map[string]any{
		"stop_reason": "end_turn",
	})
	fixture.waitForExport("interactive native terminal held by the confirmed-idle gate", func(export daemon.EventsExport) bool {
		return lifecycleAcceptanceHoldOutcomes(export.EventsByWindow[lifecycleStopHoldWindow]) != nil &&
			export.WindowMetadata[lifecycleStopHoldWindow].Shadow.EffectiveIcon == "bolt"
	})

	fixture.removeProcess(claudePipe.PID)
	fixture.setProcess(codexPipe.PID, "Thu Jul 16 11:59:59 2026") // PID reuse.
	fixture.removeProcess(copilotPipe.PID)
	fixture.removeProcess(concurrentA.PID)
	fixture.removeProcess(protected.PID)
	fixture.removeProcess(nativeBefore.PID)
	fixture.removeProcess(interactiveClaude.PID)
	fixture.removeProcess(interactiveCodex.PID)
	fixture.removeProcess(interactiveCopilot.PID)
	fixture.removeProcess(aslPipe.PID)
	fixture.removeProcess(resetProcess.PID)
	fixture.removeProcess(stopHoldProcess.PID)

	settled := fixture.waitForExport("cross-harness lifecycle settlements", func(export daemon.EventsExport) bool {
		for _, expected := range []struct {
			windowID int
			pid      int
		}{
			{lifecycleParentChildWindow, claudePipe.PID},
			{lifecycleCodexPipeWindow, codexPipe.PID},
			{lifecycleCopilotPipeWindow, copilotPipe.PID},
			{lifecycleConcurrentWindow, concurrentA.PID},
			{lifecycleProtectedWindow, protected.PID},
			{lifecycleNativeBeforeWindow, nativeBefore.PID},
			{lifecycleClaudeInteractive, interactiveClaude.PID},
			{lifecycleCodexInteractive, interactiveCodex.PID},
			{lifecycleCopilotInteractive, interactiveCopilot.PID},
			{lifecycleASLWindow, aslPipe.PID},
			{lifecycleResetWindow, resetProcess.PID},
			{lifecycleStopHoldWindow, stopHoldProcess.PID},
		} {
			exit, ok := lifecycleAcceptanceExit(export.EventsByWindow[expected.windowID], expected.pid)
			if !ok || !lifecycleAcceptanceRenderComplete(export.WindowMetadata[expected.windowID], exit.RenderGeneration) {
				return false
			}
		}
		return true
	})

	assertLifecycleAcceptancePipeResult(t, settled, lifecycleParentChildWindow, claudePipe, "bolt", daemon.HarnessProcessMissing)
	assertLifecycleAcceptancePipeResult(t, settled, lifecycleCodexPipeWindow, codexPipe, "", daemon.HarnessProcessReplacedInstance)
	assertLifecycleAcceptancePipeResult(t, settled, lifecycleCopilotPipeWindow, copilotPipe, "", daemon.HarnessProcessMissing)

	concurrentMeta := settled.WindowMetadata[lifecycleConcurrentWindow]
	if concurrentMeta.Shadow.EffectiveIcon != "bolt" {
		t.Fatalf("one concurrent child exit produced icon %q, want surviving bolt", concurrentMeta.Shadow.EffectiveIcon)
	}
	if exited := lifecycleAcceptanceFact(concurrentMeta, daemon.SourceHarness, "codex:pipe-45-a"); exited == nil || !exited.Acked {
		t.Fatalf("exiting concurrent fact = %+v, want acknowledged", exited)
	}
	if survivor := lifecycleAcceptanceFact(concurrentMeta, daemon.SourceHarness, "codex:pipe-45-b"); survivor == nil || survivor.Acked {
		t.Fatalf("surviving concurrent fact = %+v, want live", survivor)
	}
	if _, ok := lifecycleAcceptanceExit(settled.EventsByWindow[lifecycleConcurrentWindow], concurrentB.PID); ok {
		t.Fatal("alive concurrent child received a process-exit event")
	}

	protectedMeta := settled.WindowMetadata[lifecycleProtectedWindow]
	if protectedMeta.Shadow.EffectiveIcon != "error" {
		t.Fatalf("protected pipe outcome icon = %q, want error", protectedMeta.Shadow.EffectiveIcon)
	}
	for _, key := range []string{"claude:failure-46", "claude:question-46"} {
		if fact := lifecycleAcceptanceFact(protectedMeta, daemon.SourceHarness, key); fact == nil || fact.Acked {
			t.Fatalf("protected fact %s = %+v, want unacknowledged", key, fact)
		}
	}
	if fact := lifecycleAcceptanceFact(protectedMeta, daemon.SourceHarness, "claude:active-46"); fact == nil || !fact.Acked {
		t.Fatalf("disposable protected-group fact = %+v, want acknowledged", fact)
	}

	nativeBeforeExit, _ := lifecycleAcceptanceExit(settled.EventsByWindow[lifecycleNativeBeforeWindow], nativeBefore.PID)
	if nativeBeforeExit.TerminalHookEvidence != "observed" || nativeBeforeExit.SynthesizedStop == nil || *nativeBeforeExit.SynthesizedStop {
		t.Fatalf("native-before exit evidence = %+v", nativeBeforeExit)
	}

	for _, expected := range []struct {
		windowID int
		identity daemon.HarnessProcessIdentity
	}{
		{lifecycleClaudeInteractive, interactiveClaude},
		{lifecycleCodexInteractive, interactiveCodex},
		{lifecycleCopilotInteractive, interactiveCopilot},
	} {
		// No native terminal ever reached these windows, held or applied, so a
		// synthesized stop must keep reporting the hook as absent.
		exit, _ := lifecycleAcceptanceExit(settled.EventsByWindow[expected.windowID], expected.identity.PID)
		if exit.Reason != daemon.HarnessProcessExitReasonInteractive || exit.ProcessMode != "interactive" ||
			exit.SynthesizedStop == nil || !*exit.SynthesizedStop || exit.TerminalHookEvidence != "absent" ||
			settled.WindowMetadata[expected.windowID].Shadow.EffectiveIcon != "stop" {
			t.Fatalf("interactive exit for %s = %+v meta=%+v", expected.identity.Harness, exit, settled.WindowMetadata[expected.windowID].Shadow)
		}
	}

	aslMeta := settled.WindowMetadata[lifecycleASLWindow]
	if !aslMeta.Shadow.EffectiveLooping || aslMeta.Shadow.EffectiveIcon != "bolt" {
		t.Fatalf("ASL coexistence after pipe exit = %+v", aslMeta.Shadow)
	}
	if asl := lifecycleAcceptanceFact(aslMeta, daemon.SourceASL, "acceptance-run-53"); asl == nil || !asl.LeaseLive || asl.Acked {
		t.Fatalf("ASL lease changed during process settlement: %+v", asl)
	}

	// The exit takes both the commit mutex and the state lock, so a hold callback
	// that already claimed its generation cannot re-apply its stop after the exit
	// withdrew this window's facts: exactly one stop settles. The retired hold is
	// the proof that the native terminal hook was received, so the exit reports it
	// observed even though the exit synthesized the stop itself.
	stopHoldExit, _ := lifecycleAcceptanceExit(settled.EventsByWindow[lifecycleStopHoldWindow], stopHoldProcess.PID)
	if stopHoldExit.Reason != daemon.HarnessProcessExitReasonInteractive || stopHoldExit.SynthesizedStop == nil ||
		!*stopHoldExit.SynthesizedStop || stopHoldExit.TerminalHookEvidence != "observed" ||
		settled.WindowMetadata[lifecycleStopHoldWindow].Shadow.EffectiveIcon != "stop" {
		t.Fatalf("held-stop process exit = %+v shadow=%+v", stopHoldExit, settled.WindowMetadata[lifecycleStopHoldWindow].Shadow)
	}
	holdOutcomes := lifecycleAcceptanceHoldOutcomes(settled.EventsByWindow[lifecycleStopHoldWindow])
	if len(holdOutcomes) != 2 || holdOutcomes[0] != daemon.StopHoldOutcomeHeld ||
		holdOutcomes[1] != daemon.StopHoldOutcomeBypassed {
		t.Fatalf("stop-hold rows = %v, want [held bypassed]", holdOutcomes)
	}
	if got := lifecycleAcceptanceSettledStops(settled.EventsByWindow[lifecycleStopHoldWindow]); got != 1 {
		t.Fatalf("settled stop events for the bypassed hold = %d, want exactly 1 (%+v)",
			got, settled.EventsByWindow[lifecycleStopHoldWindow])
	}

	resetExit, _ := lifecycleAcceptanceExit(settled.EventsByWindow[lifecycleResetWindow], resetProcess.PID)
	if resetExit.Reason != daemon.HarnessProcessExitReasonReset || resetExit.SynthesizedStop == nil || *resetExit.SynthesizedStop ||
		settled.WindowMetadata[lifecycleResetWindow].Shadow.EffectiveIcon != "" {
		t.Fatalf("reset process exit = %+v shadow=%+v", resetExit, settled.WindowMetadata[lifecycleResetWindow].Shadow)
	}

	// Force a missing snapshot to pause outside the handler lock, then deliver
	// the native terminal while inspection is in flight. The stale receipt must
	// not settle; the following fresh pass may withdraw the clean pipe stop and
	// must report that terminal evidence was observed.
	fixture.enableProcessBlock(nativeAfter.PID)
	fixture.sendHook(lifecycleNativeAfterWindow, "claude", "PreToolUse", "native-48", 1, true, &nativeAfter, nil)
	fixture.waitForProcessBlock()
	// sendHook uses the production client's bounded IPC deadline, so accepting
	// this request also proves the blocked inspector does not hold daemon state.
	fixture.sendHook(lifecycleNativeAfterWindow, "claude", "Stop", "native-48", 2, true, &nativeAfter, map[string]any{
		"stop_reason": "end_turn",
	})
	fixture.releaseProcessBlock()
	time.Sleep(100 * time.Millisecond)
	beforeFreshPass, err := daemon.NewClient().ExportEvents(lifecycleNativeAfterWindow)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lifecycleAcceptanceExit(beforeFreshPass.EventsByWindow[lifecycleNativeAfterWindow], nativeAfter.PID); ok {
		t.Fatal("stale in-flight inspection settled a newer native terminal receipt")
	}
	nativeAfterExport := fixture.waitForExport("fresh inspection after native terminal", func(export daemon.EventsExport) bool {
		_, ok := lifecycleAcceptanceExit(export.EventsByWindow[lifecycleNativeAfterWindow], nativeAfter.PID)
		return ok
	})
	nativeAfterExit, _ := lifecycleAcceptanceExit(nativeAfterExport.EventsByWindow[lifecycleNativeAfterWindow], nativeAfter.PID)
	if nativeAfterExit.TerminalHookEvidence != "observed" || nativeAfterExit.SynthesizedStop == nil || *nativeAfterExit.SynthesizedStop {
		t.Fatalf("native-after exit evidence = %+v", nativeAfterExit)
	}

	// Capture the real renderer write, stop the daemon, inject only that Kitty
	// user var into the next ls snapshot, and prove the first post-restart
	// inspection can retire the restored exact fingerprint.
	persisted := fixture.persistedState(lifecycleRestartWindow)
	if !strings.Contains(persisted, `"v":4`) || !strings.Contains(persisted, `"pp":5201`) {
		t.Fatalf("restart state lacks schema-v4 process ownership: %s", persisted)
	}
	fixture.setPersistedState(lifecycleRestartWindow, persisted)
	fixture.stopDaemon()
	fixture.startDaemon()
	fixture.waitForExport("schema-v4 process fact restored", func(export daemon.EventsExport) bool {
		fact := lifecycleAcceptanceFact(export.WindowMetadata[lifecycleRestartWindow], daemon.SourceHarness, "claude:restart-52")
		return fact != nil && !fact.Acked && fact.HarnessProcessPID == restoredPipe.PID &&
			fact.HarnessProcessStartToken == restoredPipe.StartToken
	})
	fixture.removeProcess(restoredPipe.PID)
	restarted := fixture.waitForExport("restored pipe process settled after restart", func(export daemon.EventsExport) bool {
		exit, ok := lifecycleAcceptanceExit(export.EventsByWindow[lifecycleRestartWindow], restoredPipe.PID)
		return ok && lifecycleAcceptanceRenderComplete(export.WindowMetadata[lifecycleRestartWindow], exit.RenderGeneration)
	})
	restartExit, _ := lifecycleAcceptanceExit(restarted.EventsByWindow[lifecycleRestartWindow], restoredPipe.PID)
	if restartExit.Reason != daemon.HarnessProcessExitReasonPipe || restartExit.TerminalHookEvidence != "absent" ||
		restarted.WindowMetadata[lifecycleRestartWindow].Shadow.EffectiveIcon != "" ||
		!lifecycleAcceptanceRenderComplete(restarted.WindowMetadata[lifecycleRestartWindow], restartExit.RenderGeneration) {
		t.Fatalf("restart settlement = %+v metadata=%+v", restartExit, restarted.WindowMetadata[lifecycleRestartWindow])
	}

	// Synthetic process exits are silent. Notifications are disabled in the
	// fixture, and no process settlement may bypass that policy into dispatch.
	records := fixture.journalRecords()
	settlementRows := 0
	for _, record := range records {
		if record.Stage == "daemon.process_lifecycle" && record.Outcome == "settled" {
			settlementRows++
			for _, forbidden := range []string{"command", "prompt", "payload", "path", "arguments"} {
				if _, leaked := record.Evidence[forbidden]; leaked {
					t.Fatalf("lifecycle evidence leaked %q: %+v", forbidden, record)
				}
			}
		}
		if record.Stage == "daemon.notification.dispatch" && record.Outcome == "sent" {
			t.Fatalf("synthetic lifecycle stop dispatched a notification: %+v", record)
		}
	}
	if settlementRows < 13 {
		t.Fatalf("settled lifecycle journal rows = %d, want at least 13", settlementRows)
	}
}

func TestProcessLifecycleAcceptanceDocumentation(t *testing.T) {
	data, err := os.ReadFile("../docs/acceptance-testing.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"Native terminal hooks are authoritative",
		"silent clean stop",
		"existing watchdog",
		"schema-v10 `HarnessProcessExited`",
		"schema 9 and schema 8",
		"does not claim that a live provider",
		"Do not modify the `spec`",
	} {
		if !strings.Contains(string(data), required) {
			t.Errorf("process lifecycle acceptance documentation does not contain %q", required)
		}
	}
}

type processLifecycleAcceptanceFixture struct {
	t            *testing.T
	home         string
	stateBase    string
	kittyFixture string
	kittyLog     string
	psState      string
	psBlock      string
	windows      []kitty.OSWindow
	live         map[int]string
	requestSeq   int
	cfg          *config.Config
	cancel       context.CancelFunc
	done         chan error
}

func newProcessLifecycleAcceptanceFixture(t *testing.T) *processLifecycleAcceptanceFixture {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "amx-process-lifecycle-acceptance-")
	if err != nil {
		t.Fatal(err)
	}
	stateBase := filepath.Join(home, "state")
	fixture := &processLifecycleAcceptanceFixture{
		t: t, home: home, stateBase: stateBase,
		kittyFixture: filepath.Join(home, "kitty.json"),
		kittyLog:     filepath.Join(home, "kitten-commands.log"),
		psState:      filepath.Join(home, "ps-state.txt"),
		psBlock:      filepath.Join(home, "ps-block"),
		live:         make(map[int]string),
	}
	disableContextProbe := false
	fixture.cfg = &config.Config{Defaults: config.Defaults{
		GuardDuration: 5,
		Diagnostics:   config.DiagnosticsConfig{JournalKeepDays: 14, JournalMaxMiB: 16},
		Watchdog: config.WatchdogConfig{
			Active: 600, Subagent: 1800, Compact: 180, HookSilence: 14400,
			ClaudeContextLimitProbe: &disableContextProbe,
		},
		StopConfirm: config.StopConfirmConfig{
			Delay:   lifecycleAcceptanceStopConfirmDelay,
			Timeout: lifecycleAcceptanceStopConfirmTimeout,
		},
	}}
	fixture.windows = lifecycleAcceptanceWindows(t, home)
	fixture.writeKittyFixture()
	fixture.writeProcessState()
	fixture.installExecutables()

	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", stateBase)
	t.Setenv("KITTY_LISTEN_ON", lifecycleAcceptanceKittyAddr)
	t.Setenv("KITTY_WINDOW_ID", strconv.Itoa(lifecycleParentChildWindow))
	t.Setenv("AMX_ACCEPTANCE_KITTY_FIXTURE", fixture.kittyFixture)
	t.Setenv("AMX_ACCEPTANCE_KITTY_LOG", fixture.kittyLog)
	t.Setenv("AMX_ACCEPTANCE_PS_STATE", fixture.psState)
	t.Setenv("AMX_ACCEPTANCE_PS_BLOCK", fixture.psBlock)
	t.Setenv("PATH", filepath.Join(home, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		fixture.stopDaemon()
		_ = os.RemoveAll(home)
	})
	return fixture
}

func lifecycleAcceptanceWindows(t *testing.T, home string) []kitty.OSWindow {
	t.Helper()
	harnesses := map[int]string{
		lifecycleParentChildWindow: "claude", lifecycleCodexPipeWindow: "codex",
		lifecycleCopilotPipeWindow: "copilot", lifecycleConcurrentWindow: "codex",
		lifecycleProtectedWindow: "claude", lifecycleNativeBeforeWindow: "codex",
		lifecycleNativeAfterWindow: "claude", lifecycleClaudeInteractive: "claude",
		lifecycleCodexInteractive: "codex", lifecycleCopilotInteractive: "copilot",
		lifecycleRestartWindow: "claude", lifecycleASLWindow: "claude", lifecycleResetWindow: "copilot",
		lifecycleStopHoldWindow: "claude",
	}
	ids := make([]int, 0, len(harnesses))
	for id := range harnesses {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	tabs := make([]kitty.Tab, 0, len(ids))
	for _, id := range ids {
		workspace := filepath.Join(home, "workspace", strconv.Itoa(id))
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		harnessName := harnesses[id]
		tabs = append(tabs, kitty.Tab{
			ID: id + 1000, IsActive: true, Title: fmt.Sprintf("W:lifecycle-%d", id),
			Windows: []kitty.Window{{
				ID: id, IsActive: true, CWD: workspace, Cmdline: []string{harnessName},
				UserVars: kitty.UserVars{
					Profile: "acceptance", Harness: harnessName, Project: fmt.Sprintf("lifecycle-%d", id),
					AMXDirectory: workspace, HarnessSessionID: fmt.Sprintf("managed-%d", id),
				},
			}},
		})
	}
	return []kitty.OSWindow{{ID: 1, IsActive: true, IsFocused: true, Tabs: tabs}}
}

func (f *processLifecycleAcceptanceFixture) installExecutables() {
	f.t.Helper()
	binDir := filepath.Join(f.home, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	kittenScript := `#!/bin/sh
printf '%s\n' "$*" >> "$AMX_ACCEPTANCE_KITTY_LOG"
case " $* " in
  *" ls "*) exec /bin/cat "$AMX_ACCEPTANCE_KITTY_FIXTURE" ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "kitten"), []byte(kittenScript), 0o700); err != nil {
		f.t.Fatal(err)
	}
	psScript := `#!/bin/sh
pids=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-p" ] && [ "$#" -gt 1 ]; then
    pids="$2"
    shift 2
    continue
  fi
  shift
done
source_file="$AMX_ACCEPTANCE_PS_STATE"
block_pid=""
if [ -f "$AMX_ACCEPTANCE_PS_BLOCK.enabled" ]; then
  block_pid=$(/bin/cat "$AMX_ACCEPTANCE_PS_BLOCK.enabled")
fi
case ",$pids," in
  *",$block_pid,"*)
    if [ -n "$block_pid" ]; then
      source_file="$AMX_ACCEPTANCE_PS_BLOCK.snapshot.$$"
      /bin/cp "$AMX_ACCEPTANCE_PS_STATE" "$source_file"
      : > "$AMX_ACCEPTANCE_PS_BLOCK.entered"
      while [ ! -f "$AMX_ACCEPTANCE_PS_BLOCK.release" ]; do /bin/sleep 0.01; done
    fi
    ;;
esac
/usr/bin/awk -v wanted="$pids" '
BEGIN { n = split(wanted, values, ","); for (i = 1; i <= n; i++) keep[values[i]] = 1 }
$1 in keep { print; found = 1 }
END { if (!found) exit 1 }
' "$source_file"
status=$?
case "$source_file" in "$AMX_ACCEPTANCE_PS_BLOCK.snapshot."*) /bin/rm -f "$source_file" ;; esac
exit "$status"
`
	if err := os.WriteFile(filepath.Join(binDir, "ps"), []byte(psScript), 0o700); err != nil {
		f.t.Fatal(err)
	}
}

func (f *processLifecycleAcceptanceFixture) startDaemon() {
	f.t.Helper()
	if f.cancel != nil {
		f.t.Fatal("acceptance daemon already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = make(chan error, 1)
	go func() { f.done <- daemon.Run(ctx, f.cfg) }()
	waitForAcceptance(f.t, "process lifecycle daemon readiness", func() bool {
		health := daemon.NewClient().Health()
		return health.Alive && health.Ready
	})
}

func (f *processLifecycleAcceptanceFixture) stopDaemon() {
	if f.cancel == nil {
		return
	}
	f.cancel()
	select {
	case err := <-f.done:
		if err != nil {
			f.t.Fatalf("process lifecycle daemon shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		f.t.Fatal("process lifecycle daemon did not stop")
	}
	f.cancel = nil
	f.done = nil
}

func (f *processLifecycleAcceptanceFixture) workspace(windowID int) string {
	return filepath.Join(f.home, "workspace", strconv.Itoa(windowID))
}

func (f *processLifecycleAcceptanceFixture) sendHook(
	windowID int,
	harnessName string,
	event string,
	sessionID string,
	seq int,
	pipeMode bool,
	identity *daemon.HarnessProcessIdentity,
	extra map[string]any,
) {
	f.t.Helper()
	f.requestSeq++
	payload := map[string]any{
		"source_sequence": strconv.Itoa(seq),
		"source_event_id": fmt.Sprintf("lifecycle-%d:%d:%d", windowID, f.requestSeq, seq),
		"emitted_time":    strconv.FormatInt(time.Now().Unix(), 10),
	}
	if sessionID != "" {
		payload["session_id"] = sessionID
		payload["turn_id"] = "turn-1"
	}
	for key, value := range extra {
		payload[key] = value
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	var process *daemon.HarnessProcessIdentity
	if identity != nil {
		copy := *identity
		process = &copy
	}
	req := daemon.Request{
		Type: daemon.TypeHookEvent, WindowID: windowID, Event: event,
		Harness: harnessName, ProducerHarness: harnessName, HarnessProcess: process,
		PipeMode: pipeMode, PayloadJSON: string(encoded), Account: "acceptance",
		SessionTitle: fmt.Sprintf("W:lifecycle-%d", windowID), KittyListenOn: lifecycleAcceptanceKittyAddr,
		RequestID: fmt.Sprintf("lifecycle-request-%d", f.requestSeq),
		TraceID:   fmt.Sprintf("lifecycle-trace-%d", f.requestSeq),
		Attempt:   1, PayloadSize: int64(len(encoded)), PayloadEncoding: "json",
	}
	resp, err := daemon.NewClient().Send(req)
	if err != nil {
		f.t.Fatalf("send %s for window %d: %v", event, windowID, err)
	}
	if !resp.OK || resp.Delivery == nil || resp.Delivery.Status != daemon.DeliveryAcknowledgementAccepted {
		f.t.Fatalf("send %s for window %d response = %+v", event, windowID, resp)
	}
}

func (f *processLifecycleAcceptanceFixture) setProcess(pid int, token string) {
	f.live[pid] = token
	f.writeProcessState()
}

func (f *processLifecycleAcceptanceFixture) removeProcess(pid int) {
	delete(f.live, pid)
	f.writeProcessState()
}

func (f *processLifecycleAcceptanceFixture) writeProcessState() {
	f.t.Helper()
	pids := make([]int, 0, len(f.live))
	for pid := range f.live {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	var content strings.Builder
	for _, pid := range pids {
		fmt.Fprintf(&content, "%d %s\n", pid, f.live[pid])
	}
	tmp := f.psState + ".tmp"
	if err := os.WriteFile(tmp, []byte(content.String()), 0o600); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(tmp, f.psState); err != nil {
		f.t.Fatal(err)
	}
}

func (f *processLifecycleAcceptanceFixture) enableProcessBlock(pid int) {
	f.t.Helper()
	for _, suffix := range []string{".entered", ".release"} {
		_ = os.Remove(f.psBlock + suffix)
	}
	if err := os.WriteFile(f.psBlock+".enabled", []byte(strconv.Itoa(pid)), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *processLifecycleAcceptanceFixture) waitForProcessBlock() {
	f.t.Helper()
	waitForAcceptance(f.t, "process inspection block", func() bool {
		_, err := os.Stat(f.psBlock + ".entered")
		return err == nil
	})
}

func (f *processLifecycleAcceptanceFixture) releaseProcessBlock() {
	f.t.Helper()
	if err := os.WriteFile(f.psBlock+".release", nil, 0o600); err != nil {
		f.t.Fatal(err)
	}
	_ = os.Remove(f.psBlock + ".enabled")
}

func (f *processLifecycleAcceptanceFixture) waitForExport(
	description string,
	ready func(daemon.EventsExport) bool,
) daemon.EventsExport {
	f.t.Helper()
	var last daemon.EventsExport
	waitForAcceptance(f.t, description, func() bool {
		export, err := daemon.NewClient().ExportEvents(0)
		if err != nil {
			return false
		}
		last = export
		return ready(export)
	})
	return last
}

func (f *processLifecycleAcceptanceFixture) persistedState(windowID int) string {
	f.t.Helper()
	data, err := os.ReadFile(f.kittyLog)
	if err != nil {
		f.t.Fatal(err)
	}
	match := "--match=id:" + strconv.Itoa(windowID)
	state := ""
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, " set-user-vars ") || !strings.Contains(line, match) {
			continue
		}
		if _, value, ok := strings.Cut(line, "amx_state="); ok && json.Valid([]byte(value)) {
			state = value
		}
	}
	if state == "" {
		f.t.Fatalf("no persisted amx_state for window %d in:\n%s", windowID, data)
	}
	return state
}

func (f *processLifecycleAcceptanceFixture) setPersistedState(windowID int, state string) {
	f.t.Helper()
	found := false
	for oi := range f.windows {
		for ti := range f.windows[oi].Tabs {
			for wi := range f.windows[oi].Tabs[ti].Windows {
				win := &f.windows[oi].Tabs[ti].Windows[wi]
				if win.ID == windowID {
					win.UserVars.AMXState = state
					found = true
				}
			}
		}
	}
	if !found {
		f.t.Fatalf("window %d not found in Kitty fixture", windowID)
	}
	f.writeKittyFixture()
}

func (f *processLifecycleAcceptanceFixture) writeKittyFixture() {
	f.t.Helper()
	data, err := json.Marshal(f.windows)
	if err != nil {
		f.t.Fatal(err)
	}
	tmp := f.kittyFixture + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(tmp, f.kittyFixture); err != nil {
		f.t.Fatal(err)
	}
}

func (f *processLifecycleAcceptanceFixture) journalRecords() []diagnostics.TraceEnvelope {
	f.t.Helper()
	paths := diagnostics.PathsForRoot(filepath.Join(f.stateBase, "agent-mux"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		f.t.Fatal(err)
	}
	records, err := journal.Read(f.t.Context(), diagnostics.Selector{})
	if err != nil {
		f.t.Fatal(err)
	}
	return records
}

func lifecycleAcceptanceIdentity(harnessName string, pid int) daemon.HarnessProcessIdentity {
	return daemon.HarnessProcessIdentity{
		Harness: harnessName, PID: pid,
		StartToken: fmt.Sprintf("Thu Jul 16 10:%02d:%02d 2026", pid%60, (pid/60)%60),
	}
}

func lifecycleAcceptanceFact(
	meta daemon.WindowDebugMetadata,
	kind daemon.SourceKind,
	key string,
) *daemon.SourceFactSnapshot {
	for i := range meta.Shadow.Facts {
		fact := meta.Shadow.Facts[i]
		if fact.Kind == kind && fact.Key == key {
			return &fact
		}
	}
	return nil
}

func lifecycleAcceptanceExit(events []daemon.SessionEvent, pid int) (daemon.SessionEvent, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Event == daemon.EventHarnessProcessExited && events[i].ProcessPID == pid {
			return events[i], true
		}
	}
	return daemon.SessionEvent{}, false
}

// lifecycleAcceptanceHoldOutcomes returns the terminal-stop-hold outcomes a
// window recorded, in order, or nil while nothing has been held yet.
func lifecycleAcceptanceHoldOutcomes(events []daemon.SessionEvent) []daemon.StopHoldOutcome {
	var out []daemon.StopHoldOutcome
	for _, event := range events {
		if event.Event == daemon.EventTerminalStopHold {
			out = append(out, event.StopHoldOutcome)
		}
	}
	return out
}

// lifecycleAcceptanceSettledStops counts the rows that actually painted a stop.
// Synthetic hold rows carry action "stop" as well but apply nothing.
func lifecycleAcceptanceSettledStops(events []daemon.SessionEvent) int {
	settled := 0
	for _, event := range events {
		if event.Action == "stop" && event.Event != daemon.EventTerminalStopHold {
			settled++
		}
	}
	return settled
}

func assertLifecycleAcceptancePipeResult(
	t *testing.T,
	export daemon.EventsExport,
	windowID int,
	identity daemon.HarnessProcessIdentity,
	wantIcon string,
	wantObservation daemon.HarnessProcessInspection,
) {
	t.Helper()
	exit, ok := lifecycleAcceptanceExit(export.EventsByWindow[windowID], identity.PID)
	if !ok {
		t.Fatalf("window %d has no exit for %+v", windowID, identity)
	}
	meta := export.WindowMetadata[windowID]
	if exit.Reason != daemon.HarnessProcessExitReasonPipe || exit.ProcessMode != "pipe" ||
		exit.ProcessHarness != identity.Harness || exit.ProcessStartToken != identity.StartToken ||
		exit.ProcessObservation != string(wantObservation) || exit.WithdrawnFacts == nil || *exit.WithdrawnFacts < 1 ||
		exit.SynthesizedStop == nil || *exit.SynthesizedStop || meta.Shadow.EffectiveIcon != wantIcon ||
		!lifecycleAcceptanceRenderComplete(meta, exit.RenderGeneration) {
		t.Fatalf("window %d pipe exit = %+v metadata=%+v", windowID, exit, meta)
	}
}

func lifecycleAcceptanceRenderComplete(meta daemon.WindowDebugMetadata, generation uint64) bool {
	if generation == 0 || meta.Renderer.RenderedGen < generation {
		return false
	}
	for _, trace := range meta.Renderer.Generations {
		if trace.Generation == generation && trace.Stage == "rendered" && trace.Outcome == "success" {
			return true
		}
	}
	return false
}
