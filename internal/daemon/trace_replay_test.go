package daemon

// Sanitized trace replay. Each trace is a real-world hook sequence with all
// identifying content replaced by synthetic ids (sess-/turn-/tool-/agent-/run-
// prefixes; no prompt bodies), replayed through the FULL production path —
// Handler.Handle(TypeHookEvent) → routing → normalization → shadow reduce →
// publish — asserting the visible icon and loop marker at every step.
//
// Every trace additionally runs:
//   - a duplicate-delivery replay (each hook delivered twice) that must
//     produce the same waypoints, and
//   - restart equivalence after EVERY prefix: the window's amx_state user var
//     is captured, restored into a fresh handler through the production
//     restoreFromUserVars path, the remaining suffix is replayed, and the
//     final visible state and reducer facts must match the uninterrupted run.

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/aslcontract"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

type replayStep struct {
	event    string
	payload  string
	wantIcon string
	wantLoop bool
}

type replayTrace struct {
	name     string
	harness  string
	process  protocol.HarnessProcessIdentity
	pipeMode bool
	steps    []replayStep
}

// replayEmitBase anchors sanitized emitted_time values just behind the wall
// clock: the shadow path compacts tombstones by real age (time.Now), so a
// fixed historical epoch would silently erase terminal facts mid-trace.
var replayEmitBase = time.Now().Unix() - 60

// pl builds a sanitized payload with the shared identity fields appended.
// seq doubles as emitted time offset so recency follows the trace order.
func pl(seq int, extra string) string {
	body := fmt.Sprintf(`"source_sequence":"%d","emitted_time":"%d"`, seq, replayEmitBase+int64(seq))
	if extra != "" {
		body = extra + "," + body
	}
	return "{" + body + "}"
}

// plc is pl in the camelCase dialect used by Copilot payloads.
func plc(seq int, extra string) string {
	body := fmt.Sprintf(`"sourceSequence":"%d","emittedTime":"%d"`, seq, replayEmitBase+int64(seq))
	if extra != "" {
		body = extra + "," + body
	}
	return "{" + body + "}"
}

func replayTraces(projectDir string) []replayTrace {
	return []replayTrace{
		{
			name:    "claude-single-turn",
			harness: "claude",
			steps: []replayStep{
				{"SessionStart", pl(1, `"session_id":"sess-0001","source":"startup"`), "", false},
				{"UserPromptSubmit", pl(2, `"session_id":"sess-0001","turn_id":"turn-1"`), "bolt", false},
				{"PreToolUse", pl(3, `"session_id":"sess-0001","turn_id":"turn-1","tool_use_id":"tool-1","tool_name":"Bash"`), "bolt", false},
				{"PostToolUse", pl(4, `"session_id":"sess-0001","turn_id":"turn-1","tool_use_id":"tool-1","tool_name":"Bash"`), "bolt", false},
				{"Stop", pl(5, `"session_id":"sess-0001","turn_id":"turn-1","stop_reason":"end_turn"`), "stop", false},
			},
		},
		{
			// Keep an unterminated pipe child in the trace corpus so the normal
			// production hook path and every-prefix restart oracle compare the
			// complete schema-v4 process fingerprint, not only icon/loop state.
			name:     "claude-pipe-process-owned-active",
			harness:  "claude",
			process:  protocol.HarnessProcessIdentity{Harness: "claude", PID: 4242, StartToken: "Thu Jul 16 10:20:30 2026"},
			pipeMode: true,
			steps: []replayStep{
				{"SessionStart", pl(1, `"session_id":"sess-pipe-0001","source":"startup"`), "", false},
				{"UserPromptSubmit", pl(2, `"session_id":"sess-pipe-0001","turn_id":"turn-1"`), "bolt", false},
			},
		},
		{
			name:    "claude-permission-then-new-turn",
			harness: "claude",
			steps: []replayStep{
				{"UserPromptSubmit", pl(1, `"session_id":"sess-0001","turn_id":"turn-1"`), "bolt", false},
				// A blocking permission fires before the gated tool's PreToolUse
				// (a permission_prompt directly after PreToolUse is the
				// auto-approved shape the daemon deliberately suppresses).
				{"Notification", pl(2, `"session_id":"sess-0001","turn_id":"turn-1","notification_type":"permission_prompt"`), "key", false},
				{"PreToolUse", pl(3, `"session_id":"sess-0001","turn_id":"turn-1","tool_use_id":"tool-1"`), "bolt", false},
				{"UserPromptSubmit", pl(4, `"session_id":"sess-0001","turn_id":"turn-2"`), "bolt", false},
				{"Stop", pl(5, `"session_id":"sess-0001","turn_id":"turn-2","stop_reason":"end_turn"`), "stop", false},
			},
		},
		{
			name:    "claude-terminal-failure-then-prompt-recovery",
			harness: "claude",
			steps: []replayStep{
				{"UserPromptSubmit", pl(1, `"session_id":"sess-0001","turn_id":"turn-1"`), "bolt", false},
				{"StopFailure", pl(2, `"session_id":"sess-0001","turn_id":"turn-1"`), "error", false},
				// Trailing same-turn completion must not dislodge the sticky ❗.
				{"PostToolUse", pl(3, `"session_id":"sess-0001","turn_id":"turn-1","tool_use_id":"tool-1"`), "error", false},
				// A new prompt is authoritative recovery.
				{"UserPromptSubmit", pl(4, `"session_id":"sess-0001","turn_id":"turn-2"`), "bolt", false},
			},
		},
		{
			// Cross-harness prompt-after-stop: a clean native completion reaches
			// 🛑, and a later prompt in the SAME session always restores ⚡.
			name:    "claude-stop-then-reprompt",
			harness: "claude",
			steps: []replayStep{
				{"UserPromptSubmit", pl(1, `"session_id":"sess-000c","turn_id":"turn-1"`), "bolt", false},
				{"Stop", pl(2, `"session_id":"sess-000c","turn_id":"turn-1","stop_reason":"end_turn"`), "stop", false},
				{"UserPromptSubmit", pl(3, `"session_id":"sess-000c","turn_id":"turn-2"`), "bolt", false},
			},
		},
		{
			name:    "codex-single-turn",
			harness: "codex",
			steps: []replayStep{
				{"SessionStart", pl(1, `"session_id":"sess-0002"`), "", false},
				{"UserPromptSubmit", pl(2, `"session_id":"sess-0002","turn_id":"turn-1"`), "bolt", false},
				{"PreToolUse", pl(3, `"session_id":"sess-0002","turn_id":"turn-1","tool_use_id":"tool-1"`), "bolt", false},
				{"PostToolUse", pl(4, `"session_id":"sess-0002","turn_id":"turn-1","tool_use_id":"tool-1"`), "bolt", false},
				{"Stop", pl(5, `"session_id":"sess-0002","turn_id":"turn-1","stop_reason":"end_turn"`), "stop", false},
			},
		},
		{
			name:    "codex-compaction-auto-and-manual",
			harness: "codex",
			steps: []replayStep{
				{"UserPromptSubmit", pl(1, `"session_id":"sess-0003","turn_id":"turn-1"`), "bolt", false},
				{"PreCompact", pl(2, `"session_id":"sess-0003","turn_id":"turn-1"`), "compact", false},
				// Codex stamps the trigger: auto-compaction resumes the turn.
				{"PostCompact", pl(3, `"session_id":"sess-0003","turn_id":"turn-1","trigger":"auto"`), "bolt", false},
				{"PreCompact", pl(4, `"session_id":"sess-0003","turn_id":"turn-1"`), "compact", false},
				// A manual /compact finishes at the prompt.
				{"PostCompact", pl(5, `"session_id":"sess-0003","turn_id":"turn-1","trigger":"manual"`), "stop", false},
			},
		},
		{
			// Stale interactive source replacement through the full hook path: a
			// brand-new interactive session's prompt retires the older session's
			// interactive active fact, so the visible icon stays ⚡ on the new
			// owner and the new session's terminal is the one that settles 🛑.
			name:    "codex-session-replacement",
			harness: "codex",
			steps: []replayStep{
				{"UserPromptSubmit", pl(1, `"session_id":"sess-000a","turn_id":"turn-1"`), "bolt", false},
				{"UserPromptSubmit", pl(2, `"session_id":"sess-000b","turn_id":"turn-1"`), "bolt", false},
				{"Stop", pl(3, `"session_id":"sess-000b","turn_id":"turn-1","stop_reason":"end_turn"`), "stop", false},
			},
		},
		{
			name:    "claude-compaction-matcher-split",
			harness: "claude",
			steps: []replayStep{
				{"UserPromptSubmit", pl(1, `"session_id":"sess-0004","turn_id":"turn-1"`), "bolt", false},
				{"PreCompact", pl(2, `"session_id":"sess-0004","turn_id":"turn-1"`), "compact", false},
				// Claude's payload has no trigger; hook matchers dispatch the
				// pseudo-events. Auto resumes the turn mid-flight.
				{"PostCompactAuto", pl(3, `"session_id":"sess-0004","turn_id":"turn-1"`), "bolt", false},
				{"PreCompact", pl(4, `"session_id":"sess-0004","turn_id":"turn-1"`), "compact", false},
				{"PostCompactManual", pl(5, `"session_id":"sess-0004","turn_id":"turn-1"`), "stop", false},
			},
		},
		{
			name:    "copilot-permission-and-stop",
			harness: "copilot",
			steps: []replayStep{
				{"sessionStart", plc(1, `"sessionId":"sess-0005","source":"resume"`), "", false},
				{"userPromptSubmitted", plc(2, `"sessionId":"sess-0005","turnId":"turn-1"`), "bolt", false},
				{"notification", plc(3, `"sessionId":"sess-0005","notificationType":"permission_prompt"`), "key", false},
				// Copilot pending prompts resolve on preToolUse (the gated tool
				// actually started); a trailing postToolUse would keep the 🔑.
				{"preToolUse", plc(4, `"sessionId":"sess-0005","turnId":"turn-1","toolUseId":"tool-1"`), "bolt", false},
				// agent_idle is a background-agent notification, not a main-session
				// stop: it is ignored and the interactive turn's bolt is preserved.
				{"notification", plc(5, `"sessionId":"sess-0005","notificationType":"agent_idle"`), "bolt", false},
				// The main turn's terminal state comes from the native agentStop hook.
				{"agentStop", plc(6, `"sessionId":"sess-0005","turnId":"turn-1","stopReason":"end_turn"`), "stop", false},
			},
		},
		{
			// Copilot native terminal + prompt-after-stop, mirroring the Claude
			// and Codex reprompt traces so all three harnesses share the same
			// stop→⚡ convergence contract via their native terminal hook.
			name:    "copilot-stop-then-reprompt",
			harness: "copilot",
			steps: []replayStep{
				{"userPromptSubmitted", plc(1, `"sessionId":"sess-000d","turnId":"turn-1"`), "bolt", false},
				{"agentStop", plc(2, `"sessionId":"sess-000d","turnId":"turn-1","stopReason":"end_turn"`), "stop", false},
				{"userPromptSubmitted", plc(3, `"sessionId":"sess-000d","turnId":"turn-2"`), "bolt", false},
			},
		},
		{
			name:    "multi-agent-clean-and-failing",
			harness: "claude",
			steps: []replayStep{
				{"UserPromptSubmit", pl(1, `"session_id":"sess-0006","turn_id":"turn-1"`), "bolt", false},
				{"SubagentStart", pl(2, `"session_id":"sess-0006","turn_id":"turn-1","agent_id":"agent-a","agent_type":"explore"`), "subagent", false},
				{"SubagentStart", pl(3, `"session_id":"sess-0006","turn_id":"turn-1","agent_id":"agent-b","agent_type":"plan"`), "subagent", false},
				// agent-a completes cleanly: 🤖 persists while agent-b lives.
				{"SubagentStop", pl(4, `"session_id":"sess-0006","turn_id":"turn-1","agent_id":"agent-a","stop_reason":"end_turn"`), "subagent", false},
				// agent-b fails: sticky ❗ outranks everything below.
				{"SubagentStop", pl(5, `"session_id":"sess-0006","turn_id":"turn-1","agent_id":"agent-b","stop_reason":"error"`), "error", false},
				// The parent's own stop does not clear the failure outcome.
				{"Stop", pl(6, `"session_id":"sess-0006","turn_id":"turn-1","stop_reason":"end_turn"`), "error", false},
				// A fresh prompt acknowledges the stuck terminal fact.
				{"UserPromptSubmit", pl(7, `"session_id":"sess-0006","turn_id":"turn-2"`), "bolt", false},
			},
		},
		{
			name:    "multi-agent-fallback-to-parent",
			harness: "claude",
			steps: []replayStep{
				{"UserPromptSubmit", pl(1, `"session_id":"sess-0007","turn_id":"turn-1"`), "bolt", false},
				{"SubagentStart", pl(2, `"session_id":"sess-0007","agent_id":"agent-a"`), "subagent", false},
				// Clean completion is a lifecycle tombstone: the icon falls back
				// to the parent session's own activity, never to ⏹.
				{"SubagentStop", pl(3, `"session_id":"sess-0007","agent_id":"agent-a","stop_reason":"end_turn"`), "bolt", false},
				{"Stop", pl(4, `"session_id":"sess-0007","turn_id":"turn-1","stop_reason":"end_turn"`), "stop", false},
			},
		},
		{
			name:    "asl-claude-finish-without-native-stop",
			harness: "claude",
			steps: []replayStep{
				{"UserPromptSubmit", pl(1, `"session_id":"sess-asl-claude","turn_id":"turn-1"`), "bolt", false},
				{"AgentSessionLoopTurnStarted", aslPlForHarness(projectDir, "claude", "run-claude", 1, ""), "bolt", true},
				// No native Stop arrives. The authoritative run finish retires the
				// completed turn's older interactive bolt and surfaces 🛑.
				{"AgentSessionLoopFinished", aslPlForHarness(projectDir, "claude", "run-claude", 2, ""), "stop", false},
				{"UserPromptSubmit", pl(3, `"session_id":"sess-asl-claude","turn_id":"turn-2"`), "bolt", false},
			},
		},
		{
			name:    "asl-codex-finish-without-native-stop",
			harness: "codex",
			steps: []replayStep{
				{"UserPromptSubmit", pl(1, `"session_id":"sess-asl-codex","turn_id":"turn-1"`), "bolt", false},
				{"AgentSessionLoopTurnStarted", aslPlForHarness(projectDir, "codex", "run-codex", 1, ""), "bolt", true},
				{"AgentSessionLoopFinished", aslPlForHarness(projectDir, "codex", "run-codex", 2, ""), "stop", false},
				{"UserPromptSubmit", pl(3, `"session_id":"sess-asl-codex","turn_id":"turn-2"`), "bolt", false},
			},
		},
		{
			name:    "asl-copilot-finish-without-native-stop",
			harness: "copilot",
			steps: []replayStep{
				{"userPromptSubmitted", plc(1, `"sessionId":"sess-asl-copilot","turnId":"turn-1"`), "bolt", false},
				{"AgentSessionLoopTurnStarted", aslPlForHarness(projectDir, "copilot", "run-copilot", 1, ""), "bolt", true},
				{"AgentSessionLoopFinished", aslPlForHarness(projectDir, "copilot", "run-copilot", 2, ""), "stop", false},
				{"userPromptSubmitted", plc(3, `"sessionId":"sess-asl-copilot","turnId":"turn-2"`), "bolt", false},
			},
		},
		{
			name:    "asl-run-lifecycle",
			harness: "claude",
			steps: []replayStep{
				{"AgentSessionLoopStarted", aslPl(projectDir, "run-1", 1, ""), "", true},
				{"AgentSessionLoopTurnStarted", aslPl(projectDir, "run-1", 2, ""), "bolt", true},
				// The harness turn underneath ends: ⏹ is masked while the loop lease lives.
				{"Stop", pl(3, `"session_id":"sess-0008","turn_id":"turn-1","stop_reason":"end_turn"`), "bolt", true},
				{"AgentSessionLoopTurnFinished", aslPl(projectDir, "run-1", 4, ""), "", true},
				{"AgentSessionLoopVerificationStarted", aslPl(projectDir, "run-1", 5, ""), "bolt", true},
				{"AgentSessionLoopVerificationFinished", aslPl(projectDir, "run-1", 6, ""), "", true},
				{"AgentSessionLoopPaused", aslPl(projectDir, "run-1", 7, `"pause_reason":"question"`), "question", true},
				{"AgentSessionLoopTurnStarted", aslPl(projectDir, "run-1", 8, ""), "bolt", true},
				{"AgentSessionLoopFinished", aslPl(projectDir, "run-1", 9, ""), "stop", false},
			},
		},
		{
			name:    "asl-overlapping-runs",
			harness: "claude",
			steps: []replayStep{
				{"AgentSessionLoopTurnStarted", aslPl(projectDir, "run-1", 1, ""), "bolt", true},
				{"AgentSessionLoopStarted", aslPl(projectDir, "run-2", 1, ""), "bolt", true},
				// run-1 finishing never changes run-2's fact: 🔁 survives and
				// run-1's 🛑 is masked by run-2's live lease.
				{"AgentSessionLoopFinished", aslPl(projectDir, "run-1", 2, ""), "", true},
				{"AgentSessionLoopFinished", aslPl(projectDir, "run-2", 2, ""), "stop", false},
			},
		},
	}
}

// aslPl builds a sanitized agent_session_loop lifecycle payload rooted at the
// fixture project directory.
func aslPl(projectDir, runID string, seq int, extra string) string {
	return aslPlForHarness(projectDir, "claude", runID, seq, extra)
}

// aslPlForHarness stamps the schema-2 compatibility block every lifecycle
// payload carries. A trace without it would be quarantined at the gate, so this
// is not decoration: it is the difference between replaying ASL's wire and
// replaying a producer this build refuses.
func aslPlForHarness(projectDir, harnessName, runID string, seq int, extra string) string {
	body := fmt.Sprintf(
		`"schema_version":%d,"contract_set_id":%q,"source":"agent_session_loop","cwd":%q,"harness":%q,"run_id":%q`,
		aslHookSchemaVersion, aslcontract.ContractSetID, projectDir, harnessName, runID)
	if extra != "" {
		body += "," + extra
	}
	return pl(seq, body)
}

// newReplayHandler builds a handler with one managed window (42) owning
// projectDir, a published snapshot for hook routing, synchronous probes, and
// the reset-defer debounce disabled so replay is deterministic.
func newReplayHandler(t *testing.T, harnessName, projectDir string) (*Handler, []kitty.OSWindow) {
	t.Helper()
	oldDelay := resetFlowStopDelay
	resetFlowStopDelay = 0 // deliver Stop synchronously instead of via the debounce timer
	t.Cleanup(func() { resetFlowStopDelay = oldDelay })
	stubDaemonLoopDiscover(t, nil, nil)

	h := newTestHandler()
	h.SetStopConfirm(0, 0, false) // terminal stops paint immediately, no confirmed-idle hold
	h.SetReducerConfig(state.ReducerConfig{GuardDuration: 1})
	h.probeRunner = func(fn func()) { fn() } // context-limit probes run inline
	windows := hookEventWindows(
		palette.BuildTitleParts(palette.TitleParts{Base: "api"}),
		[]kitty.Window{{
			ID:       42,
			UserVars: kitty.UserVars{Profile: "work", Harness: harnessName, AMXDirectory: projectDir},
		}},
	)
	fake := &fakeKittyClient{windows: windows}
	h.SetKittyClient(fake)
	h.publishSnapshot(windows, time.Now())
	return h, windows
}

func replayHookStep(t *testing.T, h *Handler, trace replayTrace, step replayStep) {
	t.Helper()
	req := protocol.Request{Type: protocol.TypeHookEvent, WindowID: 42, Event: step.event, PayloadJSON: step.payload}
	if trace.process != (protocol.HarnessProcessIdentity{}) {
		process := trace.process
		req.Harness = trace.harness
		req.ProducerHarness = trace.harness
		req.HarnessProcess = &process
		req.PipeMode = trace.pipeMode
	}
	resp := h.Handle(req)
	if !resp.OK {
		t.Fatalf("%s: response = %+v", step.event, resp)
	}
}

// replayVisibleState reads the daemon-owned visible outcome for window 42.
func replayVisibleState(t *testing.T, h *Handler) (icon string, looping bool, facts []protocol.SourceFactSnapshot) {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[42]
	if st == nil {
		return "", false, nil
	}
	if st.Reducer != nil {
		facts = st.Reducer.Facts()
	}
	return st.DesiredIcon, st.Looping, facts
}

// TestTraceReplaySanitizedWaypoints replays every sanitized trace through the
// full hook path and asserts the visible icon and loop marker after each step.
func TestTraceReplaySanitizedWaypoints(t *testing.T) {
	projectDir := t.TempDir()
	for _, trace := range replayTraces(projectDir) {
		t.Run(trace.name, func(t *testing.T) {
			h, _ := newReplayHandler(t, trace.harness, projectDir)
			for i, step := range trace.steps {
				replayHookStep(t, h, trace, step)
				icon, looping, _ := replayVisibleState(t, h)
				if icon != step.wantIcon || looping != step.wantLoop {
					t.Fatalf("step %d (%s): icon=%q looping=%v; want %q/%v",
						i, step.event, icon, looping, step.wantIcon, step.wantLoop)
				}
			}
		})
	}
}

// TestTraceReplayDuplicateDeliveryIdempotent redelivers every hook of every
// trace twice (an at-least-once transport retry) and asserts the waypoints are
// unchanged: replay through the daemon's receipt sequencing is idempotent.
func TestTraceReplayDuplicateDeliveryIdempotent(t *testing.T) {
	projectDir := t.TempDir()
	for _, trace := range replayTraces(projectDir) {
		t.Run(trace.name, func(t *testing.T) {
			h, _ := newReplayHandler(t, trace.harness, projectDir)
			for i, step := range trace.steps {
				replayHookStep(t, h, trace, step)
				replayHookStep(t, h, trace, step) // duplicate delivery
				icon, looping, _ := replayVisibleState(t, h)
				if icon != step.wantIcon || looping != step.wantLoop {
					t.Fatalf("step %d (%s) after duplicate: icon=%q looping=%v; want %q/%v",
						i, step.event, icon, looping, step.wantIcon, step.wantLoop)
				}
			}
		})
	}
}

// TestTraceReplayRestartEquivalenceEveryPrefix restarts the daemon after every
// prefix of every trace: the window's amx_state user var is captured from the
// running handler, restored into a FRESH handler through the production
// restoreFromUserVars path, and the remaining steps are replayed. The final
// visible state and the reducer's facts must match the uninterrupted run.
func TestTraceReplayRestartEquivalenceEveryPrefix(t *testing.T) {
	projectDir := t.TempDir()
	for _, trace := range replayTraces(projectDir) {
		t.Run(trace.name, func(t *testing.T) {
			full, _ := newReplayHandler(t, trace.harness, projectDir)
			for _, step := range trace.steps {
				replayHookStep(t, full, trace, step)
			}
			wantIcon, wantLoop, wantFacts := replayVisibleState(t, full)

			for k := 0; k <= len(trace.steps); k++ {
				pre, _ := newReplayHandler(t, trace.harness, projectDir)
				for _, step := range trace.steps[:k] {
					replayHookStep(t, pre, trace, step)
				}

				post, _ := newReplayHandler(t, trace.harness, projectDir)
				pre.stateMu.Lock()
				st := pre.store[42]
				pre.stateMu.Unlock()
				if st != nil {
					encoded, err := st.MarshalUserVar()
					if err != nil {
						t.Fatalf("prefix %d: MarshalUserVar: %v", k, err)
					}
					post.restoreFromUserVars(hookEventWindows(
						palette.BuildTitleParts(palette.TitleParts{Base: "api"}),
						[]kitty.Window{{
							ID: 42,
							UserVars: kitty.UserVars{
								Profile:      "work",
								Harness:      trace.harness,
								AMXDirectory: projectDir,
								AMXState:     encoded,
							},
						}},
					), time.Now())
				}
				for _, step := range trace.steps[k:] {
					replayHookStep(t, post, trace, step)
				}

				gotIcon, gotLoop, gotFacts := replayVisibleState(t, post)
				if gotIcon != wantIcon || gotLoop != wantLoop {
					t.Fatalf("prefix %d: restarted run icon=%q looping=%v; uninterrupted %q/%v",
						k, gotIcon, gotLoop, wantIcon, wantLoop)
				}
				if (len(gotFacts) != 0 || len(wantFacts) != 0) && !reflect.DeepEqual(gotFacts, wantFacts) {
					t.Fatalf("prefix %d: facts diverged after restart\nrestarted    =%+v\nuninterrupted=%+v",
						k, gotFacts, wantFacts)
				}
			}
		})
	}
}
