package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/probes"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/screenstate"
)

// codexIdleProbeTerminalRun is how many consecutive agreeing screen samples a
// terminal Codex screen owes before a synthetic stop may settle. It is READ OFF
// the policy table rather than written as a literal, so the run length has one
// source of truth and a future change to the Codex row moves every probe suite
// with it. The literal itself is pinned once, in TestCodexIdleProbeSampleTiers.
//
// TierTerminalExplicit is the only tier the Codex adapter can produce (see
// codexAdapter.Classify), and no Codex evidence buys a title bump, so this one
// number covers every Codex settle in the package.
var codexIdleProbeTerminalRun = activity.RequiredIdleSamples(harness.Codex, screenstate.TierTerminalExplicit, 0)

// probeCodexUntilSettled drives ProbeCodexScreenPrompts for one whole
// stabilization run — one tick per simulated second from base — and returns the
// timestamp of the final tick.
//
// Codex used to settle from a single probe pass, so every Codex probe test in
// this package called the probe exactly once. Funnelling those calls through
// here is what keeps a change to the sample count from being an edit at each of
// them, and it is why the run length above is derived rather than typed out.
//
// It deliberately asserts nothing on its own. The callers disagree about which
// window is meant to settle — a routed binding reads one pane and settles
// another — and about whether settling is even the expected outcome, so
// per-tick assertions stay with the test that knows the answer.
func probeCodexUntilSettled(t *testing.T, h *Handler, base time.Time) time.Time {
	t.Helper()
	if codexIdleProbeTerminalRun < 1 {
		t.Fatalf("codex terminal run = %d, want at least one probe pass", codexIdleProbeTerminalRun)
	}
	last := base
	for sample := 0; sample < codexIdleProbeTerminalRun; sample++ {
		last = base.Add(time.Duration(sample) * time.Second)
		h.ProbeCodexScreenPrompts(last)
	}
	return last
}

// codexInjectSessionEvent lands one session-scoped normalized event on the
// window mid-test, modelling a hook that arrives while a probe's screen read
// is in flight.
func codexInjectSessionEvent(t *testing.T, h *Handler, windowID int, session, action, event string, at int64) {
	t.Helper()
	ev := protocol.NormalizedEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:" + session},
		Action:    action,
		SessionID: session,
		At:        at,
		Event:     event,
	}
	resp := h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: windowID, Action: action, Event: event, Normalized: &ev})
	if !resp.OK {
		t.Fatalf("inject codex %s rejected: %+v", action, resp)
	}
}

// codexSeedActiveBolt folds a real session-scoped UserPromptSubmit fact into a
// window so its stuck ⚡ is backed by a live interactive reducer fact — the
// reducer precedence a synthetic screen stop must defeat. It clears the dirty
// mark left by seeding so the probe's own render is the only title write the
// fake observes, and returns the resulting receipt sequence.
func codexSeedActiveBolt(t *testing.T, h *Handler, windowID int, session string, at int64) uint64 {
	t.Helper()
	ev := protocol.NormalizedEvent{
		Source:    protocol.SourceID{Kind: protocol.SourceHarness, Key: "codex:" + session},
		Action:    "active",
		SessionID: session,
		At:        at,
		Event:     "UserPromptSubmit",
	}
	resp := h.handleStateUpdate(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: windowID, Action: "active", Event: "UserPromptSubmit", Normalized: &ev})
	if !resp.OK {
		t.Fatalf("seed codex active %q rejected: %+v", session, resp)
	}
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	st := h.store[windowID]
	st.Dirty = false
	st.DirtySince = time.Time{}
	return st.ShadowSeq
}

func directCodexProbeBinding(windowID int, receipt, snapshotGen uint64) probes.Binding {
	binding := probes.Binding{
		SourceWindowID:      windowID,
		TargetWindowID:      windowID,
		SourceReceipt:       receipt,
		TargetReceipt:       receipt,
		SnapshotTabID:       100,
		SnapshotTabEpoch:    snapshotGen,
		SnapshotSourceEpoch: snapshotGen,
		SnapshotTargetEpoch: snapshotGen,
		LegacyDirectManaged: true,
	}
	binding.Producer.Harness = harness.Codex
	return binding
}

// lastEventNamed returns the most recent event with the given name.
func lastEventNamed(events []protocol.SessionEvent, name string) (protocol.SessionEvent, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Event == name {
			return events[i], true
		}
	}
	return protocol.SessionEvent{}, false
}

func codexProbeForegroundWindows(windowID int, tabTitle string, foregroundCmdline []string) []kitty.OSWindow {
	return []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:    100,
					Title: tabTitle,
					Windows: []kitty.Window{
						{
							ID: windowID,
							ForegroundProcesses: []kitty.ForegroundProcess{
								{Cmdline: foregroundCmdline},
							},
						},
					},
				},
			},
		},
	}
}

func codexProbeSplitWindows(tabTitle string, windows ...kitty.Window) []kitty.OSWindow {
	return []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:      100,
					Title:   tabTitle,
					Windows: windows,
				},
			},
		},
	}
}

func codexProbeWindows(windowID int, tabTitle string, userVars kitty.UserVars) []kitty.OSWindow {
	return []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID:    100,
					Title: tabTitle,
					Windows: []kitty.Window{
						{ID: windowID, UserVars: userVars},
					},
				},
			},
		},
	}
}

const codexEditApprovalScreen = `
▌ Would you like to make the following edits?

  src/app.go
  + return nil

› 1. Yes, apply these edits
  2. No, keep editing

Press enter to confirm
`

const codexRequestUserInputScreen = `
Pick the deployment target.

› Staging (Recommended)
  Production
  None of the above

↑/↓ to navigate questions
ctrl + p / ctrl + n change question
Type your answer (optional)
Add notes
Proceed
`

const codexCommandApprovalScreen = `
▌ Would you like to run the following command?

  go test ./...

› 1. Yes, run it
  2. No

Press enter to confirm
`

// The two screens below are synthesized rather than captured: unlike the three
// above, no testdata fixture exists for either kind. They are written to the
// clause lists in internal/screenstate/patterns_codex.toml and, just as
// importantly, AGAINST the arms that precede theirs in ClassifyCodexPrompt's
// fixed switch — the wording is chosen so each screen falls through every
// earlier arm and lands on its own. TestCodexPromptKindReachesWindowState is
// what proves that, since a screen that classified as the wrong kind would fail
// its row rather than pass quietly.

const codexUnansweredConfirmationScreen = `
▌ Unanswered confirmation

  Two questions were left blank.

› 1. Continue anyway
  2. Go back

Press enter to confirm
`

const codexPermissionGrantScreen = `
▌ Grant permission to read files outside the workspace?

  ~/notes

› 1. Yes
  2. No

Press enter to confirm
`

// The six screens below stayed behind when the Codex classifier tests moved to
// internal/screenstate/codex_test.go. They are not classifier fixtures here:
// each one is served through the Kitty fake by a probe test that asserts the
// EVENT path — which icon a stuck bolt settles to, which receipt wins a race —
// and needs a screen the classifier accepts in order to get there. The
// classifier assertions themselves live with the classifier.

const codexCompletedTurnScreen = `
• Ran tests
  go test ./internal/daemon

• Worked for 1m 30s

› Use /skills to list available skills

gpt-5.5 xhigh · ~/1_dev_projects/sglavoie_agentmux · Context 12% used
`

const codexWorkingTurnScreen = `
• Worked for 1m 30s

• Working (5s • Esc to interrupt)

›
`

const codexInterruptedTurnScreen = `
• Ran tests
  go test ./internal/daemon

⏹ Interrupted by user

› Use /skills to list available skills

gpt-5.5 xhigh · ~/1_dev_projects/sglavoie_agentmux · Context 12% used
`

const codexReportedClearedScreen = `
╭─────────────────────────────────────────────────╮
│ >_ OpenAI Codex (v0.137.0)                      │
│                                                 │
│ model:       gpt-5.5 xhigh   /model to change   │
│ directory:   ~/1_dev_projects/sglavoie_agentmux │
│ permissions: YOLO mode                          │
╰─────────────────────────────────────────────────╯

  Tip: New Use /fast to enable our fastest inference with increased plan usage.

Token usage: total=880,434 input=839,452 (+ 5,946,496 cached) output=40,982 (reasoning 21,671)
To continue this session, run codex resume 019e9855-77d8-7803-9a5a-fb2663a5940d

 
› Use /skills to list available skills
 
  gpt-5.5 xhigh · ~/1_dev_projects/sglavoie_agentmux · Context 0% used
`

const codexClearedScreen = `
╭────────────────────────────────────────────╮
│ >_ OpenAI Codex                            │
│ model: gpt-5-codex                         │
│ directory: /repo                           │
│ permissions: on-request                    │
╰────────────────────────────────────────────╯

Session summary is available with codex resume.
Token accounting changed wording in this build.

› Use /skills to list available skills

context 0% · gpt-5-codex · default · Plan mode
`

const codexClearedScreenWithTranscript = `
User: investigate why /clear leaves the stop icon.
Codex: I will inspect the hook flow.

` + codexClearedScreen
