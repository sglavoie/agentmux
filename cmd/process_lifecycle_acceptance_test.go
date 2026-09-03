package cmd

import (
	"os"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
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

	for _, identity := range []protocol.HarnessProcessIdentity{
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

	initial := fixture.waitForExport("initial process facts persisted and rendered", func(export protocol.EventsExport) bool {
		meta := export.WindowMetadata[lifecycleRestartWindow]
		return lifecycleAcceptanceFact(meta, protocol.SourceHarness, "claude:restart-52") != nil &&
			meta.Renderer.PersistedGen > 0 && meta.Renderer.RenderedGen >= meta.Renderer.PersistedGen &&
			export.WindowMetadata[lifecycleASLWindow].Shadow.EffectiveLooping &&
			lifecycleAcceptanceFact(export.WindowMetadata[lifecycleASLWindow], protocol.SourceASL, "acceptance-run-53") != nil
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
	fixture.waitForExport("native terminal applied before inspection", func(export protocol.EventsExport) bool {
		fact := lifecycleAcceptanceFact(export.WindowMetadata[lifecycleNativeBeforeWindow], protocol.SourceHarness, "codex:native-47")
		return fact != nil && fact.Action == "stop" && fact.Terminated
	})

	// An interactive native terminal enters the confirmed-idle gate instead of
	// painting. Nothing here can confirm it and the configured deadline outlives
	// the fixture, so the only way it can leave the gate is the process exit.
	fixture.sendHook(lifecycleStopHoldWindow, "claude", "Stop", "hold-55", 2, false, &stopHoldProcess, map[string]any{
		"stop_reason": "end_turn",
	})
	fixture.waitForExport("interactive native terminal held by the confirmed-idle gate", func(export protocol.EventsExport) bool {
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

	settled := fixture.waitForExport("cross-harness lifecycle settlements", func(export protocol.EventsExport) bool {
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

	assertLifecycleAcceptancePipeResult(t, settled, lifecycleParentChildWindow, claudePipe, "bolt", probes.HarnessProcessMissing)
	assertLifecycleAcceptancePipeResult(t, settled, lifecycleCodexPipeWindow, codexPipe, "", probes.HarnessProcessReplacedInstance)
	assertLifecycleAcceptancePipeResult(t, settled, lifecycleCopilotPipeWindow, copilotPipe, "", probes.HarnessProcessMissing)

	concurrentMeta := settled.WindowMetadata[lifecycleConcurrentWindow]
	if concurrentMeta.Shadow.EffectiveIcon != "bolt" {
		t.Fatalf("one concurrent child exit produced icon %q, want surviving bolt", concurrentMeta.Shadow.EffectiveIcon)
	}
	if exited := lifecycleAcceptanceFact(concurrentMeta, protocol.SourceHarness, "codex:pipe-45-a"); exited == nil || !exited.Acked {
		t.Fatalf("exiting concurrent fact = %+v, want acknowledged", exited)
	}
	if survivor := lifecycleAcceptanceFact(concurrentMeta, protocol.SourceHarness, "codex:pipe-45-b"); survivor == nil || survivor.Acked {
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
		if fact := lifecycleAcceptanceFact(protectedMeta, protocol.SourceHarness, key); fact == nil || fact.Acked {
			t.Fatalf("protected fact %s = %+v, want unacknowledged", key, fact)
		}
	}
	if fact := lifecycleAcceptanceFact(protectedMeta, protocol.SourceHarness, "claude:active-46"); fact == nil || !fact.Acked {
		t.Fatalf("disposable protected-group fact = %+v, want acknowledged", fact)
	}

	nativeBeforeExit, _ := lifecycleAcceptanceExit(settled.EventsByWindow[lifecycleNativeBeforeWindow], nativeBefore.PID)
	if nativeBeforeExit.TerminalHookEvidence != "observed" || nativeBeforeExit.SynthesizedStop == nil || *nativeBeforeExit.SynthesizedStop {
		t.Fatalf("native-before exit evidence = %+v", nativeBeforeExit)
	}

	for _, expected := range []struct {
		windowID int
		identity protocol.HarnessProcessIdentity
	}{
		{lifecycleClaudeInteractive, interactiveClaude},
		{lifecycleCodexInteractive, interactiveCodex},
		{lifecycleCopilotInteractive, interactiveCopilot},
	} {
		// No native terminal ever reached these windows, held or applied, so a
		// synthesized stop must keep reporting the hook as absent.
		exit, _ := lifecycleAcceptanceExit(settled.EventsByWindow[expected.windowID], expected.identity.PID)
		if exit.Reason != state.HarnessProcessExitReasonInteractive || exit.ProcessMode != "interactive" ||
			exit.SynthesizedStop == nil || !*exit.SynthesizedStop || exit.TerminalHookEvidence != "absent" ||
			settled.WindowMetadata[expected.windowID].Shadow.EffectiveIcon != "stop" {
			t.Fatalf("interactive exit for %s = %+v meta=%+v", expected.identity.Harness, exit, settled.WindowMetadata[expected.windowID].Shadow)
		}
	}

	aslMeta := settled.WindowMetadata[lifecycleASLWindow]
	if !aslMeta.Shadow.EffectiveLooping || aslMeta.Shadow.EffectiveIcon != "bolt" {
		t.Fatalf("ASL coexistence after pipe exit = %+v", aslMeta.Shadow)
	}
	if asl := lifecycleAcceptanceFact(aslMeta, protocol.SourceASL, "acceptance-run-53"); asl == nil || !asl.LeaseLive || asl.Acked {
		t.Fatalf("ASL lease changed during process settlement: %+v", asl)
	}

	// The exit takes both the commit mutex and the state lock, so a hold callback
	// that already claimed its generation cannot re-apply its stop after the exit
	// withdrew this window's facts: exactly one stop settles. The retired hold is
	// the proof that the native terminal hook was received, so the exit reports it
	// observed even though the exit synthesized the stop itself.
	stopHoldExit, _ := lifecycleAcceptanceExit(settled.EventsByWindow[lifecycleStopHoldWindow], stopHoldProcess.PID)
	if stopHoldExit.Reason != state.HarnessProcessExitReasonInteractive || stopHoldExit.SynthesizedStop == nil ||
		!*stopHoldExit.SynthesizedStop || stopHoldExit.TerminalHookEvidence != "observed" ||
		settled.WindowMetadata[lifecycleStopHoldWindow].Shadow.EffectiveIcon != "stop" {
		t.Fatalf("held-stop process exit = %+v shadow=%+v", stopHoldExit, settled.WindowMetadata[lifecycleStopHoldWindow].Shadow)
	}
	holdOutcomes := lifecycleAcceptanceHoldOutcomes(settled.EventsByWindow[lifecycleStopHoldWindow])
	if len(holdOutcomes) != 2 || holdOutcomes[0] != protocol.StopHoldOutcomeHeld ||
		holdOutcomes[1] != protocol.StopHoldOutcomeBypassed {
		t.Fatalf("stop-hold rows = %v, want [held bypassed]", holdOutcomes)
	}
	if got := lifecycleAcceptanceSettledStops(settled.EventsByWindow[lifecycleStopHoldWindow]); got != 1 {
		t.Fatalf("settled stop events for the bypassed hold = %d, want exactly 1 (%+v)",
			got, settled.EventsByWindow[lifecycleStopHoldWindow])
	}

	resetExit, _ := lifecycleAcceptanceExit(settled.EventsByWindow[lifecycleResetWindow], resetProcess.PID)
	if resetExit.Reason != state.HarnessProcessExitReasonReset || resetExit.SynthesizedStop == nil || *resetExit.SynthesizedStop ||
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
	// Stop is reset-sensitive and reaches the reducer only after its debounce, so
	// hold the inspection until the terminal fact has actually been applied.
	// Releasing on the acknowledgement alone would let the next inspection pass
	// collect a not-yet-terminal candidate and report the hook absent.
	fixture.waitForExport("native terminal applied before inspection release", func(export protocol.EventsExport) bool {
		fact := lifecycleAcceptanceFact(export.WindowMetadata[lifecycleNativeAfterWindow], protocol.SourceHarness, "claude:native-48")
		return fact != nil && fact.Action == "stop" && fact.Terminated
	})
	fixture.releaseProcessBlock()
	// The blocked pass captured its candidate before that terminal fact existed,
	// so its receipt is the evidence of which pass settled: a stale settlement can
	// only report the terminal hook absent, and only a pass that collected the
	// applied fact can report it observed.
	nativeAfterExport := fixture.waitForExport("fresh inspection after native terminal", func(export protocol.EventsExport) bool {
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
	fixture.waitForExport("schema-v4 process fact restored", func(export protocol.EventsExport) bool {
		fact := lifecycleAcceptanceFact(export.WindowMetadata[lifecycleRestartWindow], protocol.SourceHarness, "claude:restart-52")
		return fact != nil && !fact.Acked && fact.HarnessProcessPID == restoredPipe.PID &&
			fact.HarnessProcessStartToken == restoredPipe.StartToken
	})
	fixture.removeProcess(restoredPipe.PID)
	restarted := fixture.waitForExport("restored pipe process settled after restart", func(export protocol.EventsExport) bool {
		exit, ok := lifecycleAcceptanceExit(export.EventsByWindow[lifecycleRestartWindow], restoredPipe.PID)
		return ok && lifecycleAcceptanceRenderComplete(export.WindowMetadata[lifecycleRestartWindow], exit.RenderGeneration)
	})
	restartExit, _ := lifecycleAcceptanceExit(restarted.EventsByWindow[lifecycleRestartWindow], restoredPipe.PID)
	if restartExit.Reason != state.HarnessProcessExitReasonPipe || restartExit.TerminalHookEvidence != "absent" ||
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
