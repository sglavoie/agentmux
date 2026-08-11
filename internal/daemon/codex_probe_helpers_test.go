package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// codexInjectSessionEvent lands one session-scoped normalized event on the
// window mid-test, modelling a hook that arrives while a probe's screen read
// is in flight.
func codexInjectSessionEvent(t *testing.T, h *Handler, windowID int, session, action, event string, at int64) {
	t.Helper()
	ev := NormalizedEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "codex:" + session},
		Action:    action,
		SessionID: session,
		At:        at,
		Event:     event,
	}
	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: windowID, Action: action, Event: event, normalized: &ev})
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
	ev := NormalizedEvent{
		Source:    SourceID{Kind: SourceHarness, Key: "codex:" + session},
		Action:    "active",
		SessionID: session,
		At:        at,
		Event:     "UserPromptSubmit",
	}
	resp := h.handleStateUpdate(Request{Type: TypeStateUpdate, WindowID: windowID, Action: "active", Event: "UserPromptSubmit", normalized: &ev})
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

func directCodexProbeBinding(windowID int, receipt, snapshotGen uint64) screenProbeBinding {
	binding := screenProbeBinding{
		SourceWindowID:      windowID,
		TargetWindowID:      windowID,
		SourceReceipt:       receipt,
		TargetReceipt:       receipt,
		SnapshotTabID:       100,
		SnapshotTabEpoch:    snapshotGen,
		SnapshotSourceEpoch: snapshotGen,
		SnapshotTargetEpoch: snapshotGen,
		legacyDirectManaged: true,
	}
	binding.Producer.Harness = harness.Codex
	return binding
}

// lastEventNamed returns the most recent event with the given name.
func lastEventNamed(events []SessionEvent, name string) (SessionEvent, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Event == name {
			return events[i], true
		}
	}
	return SessionEvent{}, false
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
