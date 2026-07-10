package daemon

import (
	"encoding/json"
	"log/slog"
	"os"
	"strings"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

const (
	agentSessionLoopStarted     = "AgentSessionLoopStarted"
	agentSessionLoopTurnStarted = "AgentSessionLoopTurnStarted"
	agentSessionLoopFinished    = "AgentSessionLoopFinished"
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
		harnessName,
		parts.State,
		baseReq.PipeMode,
		false,
		false,
		false,
		baseReq.KittyListenOn,
	)
	resp := h.handleStateUpdate(req)
	if !resp.OK {
		slog.Debug("loop lifecycle target state update failed",
			"window_id", target.windowID,
			"event", eventOrAction,
			"action", action,
			"error", resp.Error,
		)
	}

	if marker := loopMarkerForLifecycleTarget(eventOrAction, target.workspace, openTargets); marker != nil {
		h.stateMu.Lock()
		st := h.ensureWindowStateLocked(target.windowID)
		st.Looping = *marker
		st.LoopingSet = true
		h.markWindowDirtyLocked(target.windowID)
		h.stateMu.Unlock()
	}
}

func loopLifecycleTargets(windows []kitty.OSWindow, payload hookevent.HookPayload) ([]loopLifecycleTarget, []agentloop.WorkspaceTarget) {
	allTargets, openTargets := loopLifecycleWindowTargets(windows)
	payloadTarget := loopPayloadWorkspaceTarget(payload)
	if payloadTarget.CWD == "" && payloadTarget.WindowID <= 0 {
		return nil, openTargets
	}

	candidates := []agentloop.Candidate{{
		CWD:      payloadTarget.CWD,
		Harness:  payloadTarget.Harness,
		WindowID: payloadTarget.WindowID,
	}}
	targets := make([]loopLifecycleTarget, 0, len(allTargets))
	seenTabs := make(map[int]struct{})
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
		if _, ok := seenTabs[target.tab.ID]; ok {
			continue
		}
		seenTabs[target.tab.ID] = struct{}{}
		targets = append(targets, target)
	}
	return targets, openTargets
}

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
				targets = append(targets, loopLifecycleTarget{
					windowID:  win.ID,
					tab:       tab,
					win:       win,
					workspace: workspace,
				})
				openTargets = append(openTargets, workspace)
			}
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
	case agentSessionLoopStarted, agentSessionLoopTurnStarted, agentSessionLoopFinished:
		return true
	default:
		return false
	}
}

func loopMarkerForLifecycleTarget(eventOrAction string, target agentloop.WorkspaceTarget, openTargets []agentloop.WorkspaceTarget) *bool {
	switch eventOrAction {
	case agentSessionLoopStarted, agentSessionLoopTurnStarted:
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
