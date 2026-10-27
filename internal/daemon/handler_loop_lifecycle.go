package daemon

import (
	"log/slog"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/daemon/events"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// daemonLoopDiscoverPlans is the tri-state discovery seam reconciliation uses:
// it needs to tell "gone" from "could not tell". daemonLoopDiscover is the
// presence-only view for routing and marker consumers, which act on runs that
// exist and have nothing to do when evidence is missing. Both are variables so
// tests can inject state without a filesystem.
var (
	daemonLoopDiscoverPlans = agentloop.DiscoverPlans
	daemonLoopDiscover      = func(opts agentloop.DiscoverOptions) ([]agentloop.Candidate, error) {
		result, err := daemonLoopDiscoverPlans(opts)
		if err != nil {
			return nil, err
		}
		return result.Candidates(opts.IncludePaused), nil
	}
)

type loopLifecycleTarget struct {
	windowID int
	// tab and win are the Kitty tab and window of the target. Both are nil for
	// a runtime session on another backend, or one Kitty does not list.
	tab       *kitty.Tab
	win       *kitty.Window
	workspace agentloop.WorkspaceTarget
	// runtime names the runtime session whose managed hook selected this
	// target; it is empty for a target chosen by workspace matching.
	runtime string
}

func (h *Handler) handleLoopLifecycleHookEvent(
	eventOrAction string,
	action string,
	payload hookevent.HookPayload,
	baseReq protocol.Request,
	windows []kitty.OSWindow,
) bool {
	targets, openTargets := h.aslHookTargets(baseReq, windows, payload)
	if len(targets) == 0 {
		return false
	}

	for _, target := range targets {
		h.handleLoopLifecycleTarget(eventOrAction, action, payload, baseReq, target, openTargets)
	}
	// A finish that carries no run identity cannot name which per-run lease it
	// releases. Instead of clearing every run, reconcile the window's leases
	// against on-disk loop state so only genuinely finished runs lose 🔁.
	if eventOrAction == events.AgentSessionLoopFinished && payload.EffectiveRunID() == "" {
		h.ReconcileASLLeases(time.Now(), windows)
	}
	return true
}

func (h *Handler) handleLoopLifecycleTarget(
	eventOrAction string,
	action string,
	payload hookevent.HookPayload,
	baseReq protocol.Request,
	target loopLifecycleTarget,
	openTargets []agentloop.WorkspaceTarget,
) {
	targetCtx := h.loopTargetContextFor(target)

	req := events.BuildHookStateUpdateRequest(
		target.windowID,
		action,
		eventOrAction,
		payload,
		targetCtx.sessionTitle,
		targetCtx.profile,
		baseReq.Harness,
		targetCtx.harness,
		targetCtx.currentIcon,
		baseReq.PipeMode,
		false,
		false,
		false,
		baseReq.KittyListenOn,
	)
	events.PropagateIngressTrace(&req, baseReq)
	if req.RouteEvidence == nil {
		req.RouteEvidence = events.NewRoutingEvidence(baseReq.WindowID)
	}
	req.RouteEvidence.ConsideredWindows = nil
	req.RouteEvidence.RejectedAlternatives = nil
	req.RouteEvidence.ASLMatches = 1
	events.PopulateRoutingCandidates(req.RouteEvidence, target.tab, events.RoutingWindowByID(target.tab, baseReq.WindowID), target.win)
	if target.runtime != "" {
		// The session's own slot is both source and target; no workspace was
		// matched to find it.
		events.ChooseRoute(req.RouteEvidence, events.RouteDecisionSource, "asl_runtime_session", target.win)
		req.RouteDecision = events.RouteDecisionSource
	} else {
		events.ChooseRoute(req.RouteEvidence, events.RouteDecisionOwner, "asl_workspace", target.win)
		req.RouteDecision = events.RouteDecisionOwner
	}
	// The fact lands on the managed target but retains the pane identified by
	// the request/payload as its origin for route-aware probes and diagnostics.
	originWindowID := h.aslHookOriginWindow(baseReq, payload)
	normalized := events.NormalizedFromHookRequest(eventOrAction, baseReq, originWindowID)
	req.Normalized = &normalized
	req.NormalizationOutcome = events.NormalizationOutcomeFor(normalized)
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
	if target.runtime != "" {
		h.stateMu.Lock()
		if h.runtimeSlotClosedLocked(target.windowID) {
			// The session ended after its hook was translated. Its slot is
			// closed: no claim, supervision, or loop marker may reopen it.
			h.stateMu.Unlock()
			return
		}
		if eventOrAction == events.AgentSessionLoopFinished {
			h.releaseASLRunLocked(runID, target.windowID)
		} else {
			h.claimASLRunLocked(runID, target.windowID)
		}
		h.stateMu.Unlock()
	}
	if eventOrAction == events.AgentSessionLoopFinished {
		h.finishASLSupervisorQuestion(target.windowID, runID, time.Now())
	} else {
		h.registerASLSupervisorLifecycleTarget(
			target.windowID,
			originWindowID,
			runID,
			payload.StatePath,
		)
	}

	if marker := loopMarkerForLifecycleTarget(eventOrAction, target, openTargets, h.aslStateRoot()); marker != nil {
		h.stateMu.Lock()
		if h.runtimeSlotClosedLocked(target.windowID) {
			h.stateMu.Unlock()
			return
		}
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

func loopLifecycleTargets(windows []kitty.OSWindow, payloadTarget agentloop.WorkspaceTarget) ([]loopLifecycleTarget, []agentloop.WorkspaceTarget) {
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

// readLoopStateWorkspace fills gaps in a hook payload from the run's own
// durable state.
//
// It reads the atomic `agent_mux` projection and nothing else. The loop's
// top-level bookkeeping — cwd, active_turn, and the rest — is private to ASL,
// and this used to be a second, independently drifting parser of it. The
// projection's workspace root is the run's directory for a workspace plan; a
// plan without a workspace publishes none, and the caller then routes on window
// ID alone rather than borrowing some other plan's directory.
func readLoopStateWorkspace(statePath string) (cwd string, harnessName string, windowID int, ok bool) {
	projection, err := agentloop.ReadProjection(strings.TrimSpace(statePath))
	if err != nil {
		slog.Debug("loop state projection unavailable",
			"state_path", statePath,
			"reason", agentloop.ProjectionReason(err),
			"err", err,
		)
		return "", "", 0, false
	}
	if projection.KittyWindowID > 0 {
		windowID = projection.KittyWindowID
	}
	return projection.WorkspaceRoot, normalizeKnownLoopHarness(projection.Harness), windowID, true
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

// stateRoot is the configured ASL state root ("" for ASL's default), which
// the finish check's discovery must scan like every other discovery.
func loopMarkerForLifecycleTarget(eventOrAction string, lifecycleTarget loopLifecycleTarget, openTargets []agentloop.WorkspaceTarget, stateRoot string) *bool {
	target := lifecycleTarget.workspace
	switch eventOrAction {
	case events.AgentSessionLoopStarted, events.AgentSessionLoopTurnStarted,
		events.AgentSessionLoopTurnHeartbeat, events.AgentSessionLoopTurnHangSuspected,
		events.AgentSessionLoopTurnHangCleared, events.AgentSessionLoopTurnFinished,
		events.AgentSessionLoopVerificationStarted, events.AgentSessionLoopVerificationHeartbeat,
		events.AgentSessionLoopVerificationFinished, events.AgentSessionLoopWaitStarted,
		events.AgentSessionLoopWaitEnded:
		// Every non-final lifecycle event asserts the loop marker: turns and
		// verification are active work (a suspected or retracted worker hang is
		// still an executing turn), between-turn signals keep the lease, and
		// a mailbox-waiting run still owns 🔁 while it is blocked.
		return boolPtr(true)
	case events.AgentSessionLoopPaused:
		return boolPtr(false)
	case events.AgentSessionLoopFinished:
		if lifecycleTarget.runtime != "" {
			// The finish came from this session's own run. Whether another run
			// shares its directory says nothing about this session, so the
			// workspace comparison below does not apply.
			return boolPtr(false)
		}
		if strings.TrimSpace(target.CWD) == "" && target.WindowID <= 0 {
			slog.Debug("preserving loop marker after loop finish; target directory missing")
			return nil
		}
		candidates, err := daemonLoopDiscover(agentloop.DiscoverOptions{StateRoot: stateRoot})
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
