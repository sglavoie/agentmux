package daemon

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	agentSessionLoopStarted               = "AgentSessionLoopStarted"
	agentSessionLoopTurnStarted           = "AgentSessionLoopTurnStarted"
	agentSessionLoopTurnHeartbeat         = "AgentSessionLoopTurnHeartbeat"
	agentSessionLoopTurnHangSuspected     = "AgentSessionLoopTurnHangSuspected"
	agentSessionLoopTurnHangCleared       = "AgentSessionLoopTurnHangCleared"
	agentSessionLoopTurnFinished          = "AgentSessionLoopTurnFinished"
	agentSessionLoopVerificationStarted   = "AgentSessionLoopVerificationStarted"
	agentSessionLoopVerificationHeartbeat = "AgentSessionLoopVerificationHeartbeat"
	agentSessionLoopVerificationFinished  = "AgentSessionLoopVerificationFinished"
	agentSessionLoopWaitStarted           = "AgentSessionLoopWaitStarted"
	agentSessionLoopWaitEnded             = "AgentSessionLoopWaitEnded"
	agentSessionLoopPaused                = "AgentSessionLoopPaused"
	agentSessionLoopFinished              = "AgentSessionLoopFinished"
)

var daemonLoopDiscover = agentloop.Discover

type loopLifecycleTarget struct {
	windowID  int
	tab       *kitty.Tab
	win       *kitty.Window
	workspace agentloop.WorkspaceTarget
}

func (h *Handler) handleLoopLifecycleHookEvent(
	eventOrAction string,
	action string,
	payload hookevent.HookPayload,
	baseReq Request,
	windows []kitty.OSWindow,
) bool {
	targets, openTargets := loopLifecycleTargets(windows, payload)
	if len(targets) == 0 {
		return false
	}

	for _, target := range targets {
		h.handleLoopLifecycleTarget(eventOrAction, action, payload, baseReq, target, openTargets)
	}
	// A finish that carries no run identity cannot name which per-run lease it
	// releases. Instead of clearing every run, reconcile the window's leases
	// against on-disk loop state so only genuinely finished runs lose 🔁.
	if eventOrAction == agentSessionLoopFinished && payload.EffectiveRunID() == "" {
		h.ReconcileASLLeases(time.Now(), windows)
	}
	return true
}

func (h *Handler) handleLoopLifecycleTarget(
	eventOrAction string,
	action string,
	payload hookevent.HookPayload,
	baseReq Request,
	target loopLifecycleTarget,
	openTargets []agentloop.WorkspaceTarget,
) {
	parts := palette.ParseTitleParts(target.tab.Title)
	profile := ""
	harnessName := ""
	muted := parts.Muted
	if target.win != nil {
		profile = target.win.UserVars.EffectiveProfile()
		harnessName = target.win.UserVars.EffectiveHarness()
		muted = muted || target.win.UserVars.Mute != ""
	}
	h.setMutedFromSnapshot(target.windowID, muted)

	req := buildHookStateUpdateRequest(
		target.windowID,
		action,
		eventOrAction,
		payload,
		parts.Base,
		profile,
		baseReq.Harness,
		harnessName,
		parts.State,
		baseReq.PipeMode,
		false,
		false,
		false,
		baseReq.KittyListenOn,
	)
	propagateIngressTrace(&req, baseReq)
	if req.routeEvidence == nil {
		req.routeEvidence = newRoutingEvidence(baseReq.WindowID)
	}
	req.routeEvidence.ConsideredWindows = nil
	req.routeEvidence.RejectedAlternatives = nil
	req.routeEvidence.ASLMatches = 1
	populateRoutingCandidates(req.routeEvidence, target.tab, routingWindowByID(target.tab, baseReq.WindowID), target.win)
	chooseRoute(req.routeEvidence, routeDecisionOwner, "asl_workspace", target.win)
	req.routeDecision = routeDecisionOwner
	// The fact lands on the managed target but retains the pane identified by
	// the request/payload as its origin for route-aware probes and diagnostics.
	originWindowID := baseReq.WindowID
	if payloadWindowID := payload.EffectiveKittyWindowID(); payloadWindowID > 0 {
		originWindowID = payloadWindowID
	}
	normalized := normalizedFromHookRequest(eventOrAction, baseReq, originWindowID)
	req.normalized = &normalized
	req.normalizationOutcome = normalizationOutcomeFor(normalized)
	resp := h.handleStateUpdate(req)
	if !resp.OK {
		slog.Debug("loop lifecycle target state update failed",
			"window_id", target.windowID,
			"event", eventOrAction,
			"action", action,
			"error", resp.Error,
		)
	}
	runID := payload.EffectiveRunID()
	if eventOrAction == agentSessionLoopFinished {
		h.finishASLSupervisorQuestion(target.windowID, runID, time.Now())
	} else {
		h.registerASLSupervisorLifecycleTarget(
			target.windowID,
			originWindowID,
			runID,
			payload.StatePath,
		)
	}

	if marker := loopMarkerForLifecycleTarget(eventOrAction, target.workspace, openTargets); marker != nil {
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(target.windowID)
		// The reducer's per-run leases are authoritative once any ASL fact has
		// reached it (reduceStateUpdate already published the reduced verdict
		// above). The discovery-derived marker only stands in for windows whose
		// reducer holds no ASL knowledge — a quarantined stamp, or pre-cutover
		// state.
		loopingBefore, loopingSetBefore := st.Looping, st.LoopingSet
		if st.Reducer == nil || !st.Reducer.LoopStateKnown() {
			st.Looping = *marker
			st.LoopingSet = true
		}
		if st.Looping != loopingBefore || st.LoopingSet != loopingSetBefore {
			h.markWindowDirtyLocked(target.windowID)
		} else {
			h.markWindowActivityDirtyLocked(target.windowID)
		}
		h.stateMu.Unlock()
	}
}

func loopLifecycleTargets(windows []kitty.OSWindow, payload hookevent.HookPayload) ([]loopLifecycleTarget, []agentloop.WorkspaceTarget) {
	payloadTarget := loopPayloadWorkspaceTarget(payload)
	if payloadTarget.CWD == "" && payloadTarget.WindowID <= 0 {
		_, openTargets := loopLifecycleWindowTargets(windows)
		return nil, openTargets
	}

	candidates := []agentloop.Candidate{{
		CWD:      payloadTarget.CWD,
		Harness:  payloadTarget.Harness,
		WindowID: payloadTarget.WindowID,
	}}
	return matchManagedLoopTargets(windows, candidates)
}

// matchManagedLoopTargets selects the title-owner window of each tab whose
// workspace matches any of candidates under the shared MatchingWorkspace policy,
// and returns the open managed targets that policy considered. It is the single
// workspace-matching seam reused by both loop lifecycle fan-out and ASL
// sibling-pane state routing, so neither path grows a second ownership policy.
// loopLifecycleWindowTargets already resolves one target per tab, so no
// additional per-tab deduplication is required here.
func matchManagedLoopTargets(windows []kitty.OSWindow, candidates []agentloop.Candidate) ([]loopLifecycleTarget, []agentloop.WorkspaceTarget) {
	allTargets, openTargets := loopLifecycleWindowTargets(windows)
	targets := make([]loopLifecycleTarget, 0, len(allTargets))
	for _, target := range allTargets {
		matches, err := agentloop.HasMatchingWorkspace(candidates, target.workspace, openTargets)
		if err != nil {
			slog.Debug("loop lifecycle target match failed",
				"window_id", target.windowID,
				"directory", target.workspace.CWD,
				"harness", target.workspace.Harness,
				"err", err,
			)
			continue
		}
		if !matches {
			continue
		}
		targets = append(targets, target)
	}
	return targets, openTargets
}

// loopLifecycleWindowTargets resolves one fan-out target per tab — the window
// that actually owns the tab title (kitty.FindTabTitleOwner: the active managed
// window, else the first managed one) — so loop state lands on the same pane the
// renderer writes to rather than on the first managed window in Kitty order.
// openTargets still enumerates every managed window's workspace so
// HasMatchingWorkspace can weigh sibling harnesses in the same workspace.
func loopLifecycleWindowTargets(windows []kitty.OSWindow) ([]loopLifecycleTarget, []agentloop.WorkspaceTarget) {
	targets := make([]loopLifecycleTarget, 0)
	openTargets := make([]agentloop.WorkspaceTarget, 0)
	for wi := range windows {
		for ti := range windows[wi].Tabs {
			tab := &windows[wi].Tabs[ti]
			for wini := range tab.Windows {
				win := &tab.Windows[wini]
				workspace, ok := managedWindowWorkspaceTarget(*win)
				if !ok {
					continue
				}
				openTargets = append(openTargets, workspace)
			}
			owner, ok := kitty.FindTabTitleOwner(tab)
			if !ok {
				continue
			}
			workspace, ok := managedWindowWorkspaceTarget(*owner)
			if !ok {
				continue
			}
			targets = append(targets, loopLifecycleTarget{
				windowID:  owner.ID,
				tab:       tab,
				win:       owner,
				workspace: workspace,
			})
		}
	}
	return targets, openTargets
}

func managedWindowWorkspaceTarget(win kitty.Window) (agentloop.WorkspaceTarget, bool) {
	if win.UserVars.EffectiveProfile() == "" && win.UserVars.EffectiveHarness() == "" {
		return agentloop.WorkspaceTarget{}, false
	}
	directory := windowDirectoryHint(win)
	if directory == "" {
		return agentloop.WorkspaceTarget{}, false
	}
	return agentloop.WorkspaceTarget{
		CWD:      directory,
		Harness:  win.UserVars.EffectiveHarness(),
		WindowID: win.ID,
	}, true
}

func loopPayloadWorkspaceTarget(payload hookevent.HookPayload) agentloop.WorkspaceTarget {
	cwd := strings.TrimSpace(payload.CWD)
	harnessName := normalizeKnownLoopHarness(payload.Harness)
	windowID := payload.EffectiveKittyWindowID()
	if (cwd == "" || harnessName == "" || windowID <= 0) && strings.TrimSpace(payload.StatePath) != "" {
		stateCWD, stateHarness, stateWindowID, ok := readLoopStateWorkspace(payload.StatePath)
		if ok {
			if cwd == "" {
				cwd = stateCWD
			}
			if harnessName == "" {
				harnessName = stateHarness
			}
			if windowID <= 0 {
				windowID = stateWindowID
			}
		}
	}
	return agentloop.WorkspaceTarget{CWD: cwd, Harness: harnessName, WindowID: windowID}
}

type loopStateWorkspace struct {
	CWD           string `json:"cwd"`
	Harness       string `json:"harness"`
	KittyWindowID int    `json:"kitty_window_id"`
	ActiveTurn    *struct {
		Harness string `json:"harness"`
	} `json:"active_turn"`
}

func readLoopStateWorkspace(statePath string) (cwd string, harnessName string, windowID int, ok bool) {
	data, err := os.ReadFile(strings.TrimSpace(statePath))
	if err != nil {
		slog.Debug("loop state payload read failed", "state_path", statePath, "err", err)
		return "", "", 0, false
	}
	var state loopStateWorkspace
	if err := json.Unmarshal(data, &state); err != nil {
		slog.Debug("loop state payload decode failed", "state_path", statePath, "err", err)
		return "", "", 0, false
	}
	harnessName = normalizeKnownLoopHarness(state.Harness)
	if state.ActiveTurn != nil {
		if activeHarness := normalizeKnownLoopHarness(state.ActiveTurn.Harness); activeHarness != "" {
			harnessName = activeHarness
		}
	}
	if state.KittyWindowID > 0 {
		windowID = state.KittyWindowID
	}
	return strings.TrimSpace(state.CWD), harnessName, windowID, true
}

func normalizeKnownLoopHarness(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case harness.Codex:
		return harness.Codex
	case harness.Copilot:
		return harness.Copilot
	case harness.Claude:
		return harness.Claude
	default:
		return ""
	}
}

func isAgentSessionLoopEvent(eventOrAction string) bool {
	switch eventOrAction {
	case agentSessionLoopStarted, agentSessionLoopTurnStarted,
		agentSessionLoopTurnHeartbeat, agentSessionLoopTurnHangSuspected,
		agentSessionLoopTurnHangCleared, agentSessionLoopTurnFinished,
		agentSessionLoopVerificationStarted, agentSessionLoopVerificationHeartbeat,
		agentSessionLoopVerificationFinished, agentSessionLoopWaitStarted,
		agentSessionLoopWaitEnded, agentSessionLoopPaused,
		agentSessionLoopFinished:
		return true
	default:
		return false
	}
}

func loopMarkerForLifecycleTarget(eventOrAction string, target agentloop.WorkspaceTarget, openTargets []agentloop.WorkspaceTarget) *bool {
	switch eventOrAction {
	case agentSessionLoopStarted, agentSessionLoopTurnStarted,
		agentSessionLoopTurnHeartbeat, agentSessionLoopTurnHangSuspected,
		agentSessionLoopTurnHangCleared, agentSessionLoopTurnFinished,
		agentSessionLoopVerificationStarted, agentSessionLoopVerificationHeartbeat,
		agentSessionLoopVerificationFinished, agentSessionLoopWaitStarted,
		agentSessionLoopWaitEnded, agentSessionLoopPaused:
		// Every non-final lifecycle event asserts the loop marker: turns and
		// verification are active work (a suspected or retracted worker hang is
		// still an executing turn), between-turn signals keep the lease, and
		// a paused or mailbox-waiting run still owns 🔁 while it is blocked.
		return boolPtr(true)
	case agentSessionLoopFinished:
		if strings.TrimSpace(target.CWD) == "" && target.WindowID <= 0 {
			slog.Debug("preserving loop marker after loop finish; target directory missing")
			return nil
		}
		candidates, err := daemonLoopDiscover(agentloop.DiscoverOptions{})
		if err != nil {
			slog.Debug("preserving loop marker after loop finish; discovery failed", "err", err)
			return nil
		}
		looping, err := agentloop.HasMatchingWorkspace(candidates, target, openTargets)
		if err != nil {
			slog.Debug("preserving loop marker after loop finish; workspace match failed",
				"directory", target.CWD,
				"harness", target.Harness,
				"window_id", target.WindowID,
				"err", err,
			)
			return nil
		}
		return boolPtr(looping)
	default:
		return nil
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func windowDirectoryHint(win kitty.Window) string {
	if directory := strings.TrimSpace(win.UserVars.EffectiveDirectory()); directory != "" {
		return directory
	}
	if directory := firstForegroundCWD(win); directory != "" {
		return directory
	}
	return strings.TrimSpace(win.CWD)
}

func firstForegroundCWD(win kitty.Window) string {
	for _, proc := range win.ForegroundProcesses {
		if cwd := strings.TrimSpace(proc.CWD); cwd != "" {
			return cwd
		}
	}
	return ""
}
