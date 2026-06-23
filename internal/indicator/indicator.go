package indicator

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/contextlimit"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// resetFlowStopDelay gives late reset markers — especially Claude's
// SessionEnd.reason=clear lifecycle event — a brief chance to reach the daemon
// before an ordinary terminal stop hook turns into a visible stop icon or
// notification.
// Exposed as a var so tests can shrink it.
var resetFlowStopDelay = 500 * time.Millisecond

var indicatorLoopDiscover = agentloop.Discover

const (
	agentSessionLoopStarted     = "AgentSessionLoopStarted"
	agentSessionLoopTurnStarted = "AgentSessionLoopTurnStarted"
	agentSessionLoopFinished    = "AgentSessionLoopFinished"
)

type indicatorKittyClient interface {
	ListWindows() ([]kitty.OSWindow, error)
	GetText(matchExpr, extent string) (string, error)
	SetTabTitle(matchExpr, title string) error
}

type indicatorDaemonClient interface {
	SendWithAutoStart(req daemon.Request, amxBin string) (daemon.Response, error)
}

type kittyTitleSetter interface {
	SetTabTitle(matchExpr, title string) error
}

// Run is the core hook-indicator entry point. It resolves the action, contacts
// the daemon for a guard decision, then updates the Kitty tab title if allowed.
//
//   - eventOrAction: harness event name (e.g. "Stop", "agentStop") or amx action (e.g. "stop")
//   - windowID: 0 means resolve the Kitty window ID from the current hook env
//   - amxBin: absolute path to the amx binary, used for daemon auto-start
func Run(eventOrAction string, windowID int, amxBin string) error {
	payload, payloadErr := ReadHookPayload(os.Stdin)
	if payloadErr != nil {
		slog.Warn("indicator: ignoring unreadable hook payload", "event", eventOrAction, "err", payloadErr)
	}
	runner := kitty.NewRealRunner()
	client := kitty.NewClient(runner)
	dc := daemon.NewClient()
	return runWithDeps(eventOrAction, windowID, amxBin, payload, IsPipeMode(), client, dc)
}

// runWithDeps contains the indicator transition logic with injectable Kitty and
// daemon clients so tests can exercise full screen-probe flows without reaching
// real subprocesses or sockets.
func runWithDeps(
	eventOrAction string,
	windowID int,
	amxBin string,
	payload HookPayload,
	pipeMode bool,
	client indicatorKittyClient,
	dc indicatorDaemonClient,
) error {
	if IsIgnoredEvent(eventOrAction, payload) {
		slog.Debug("indicator: event intentionally ignored",
			"event", eventOrAction,
			"notification_type", payload.EffectiveNotificationType(),
		)
		return nil
	}

	action, ok := ResolveAction(eventOrAction, payload)
	if !ok {
		slog.Debug("indicator: unknown event/action, ignoring", "input", eventOrAction)
		return nil
	}

	resolvedWindowID, insideKitty, err := resolveHookWindowID(windowID)
	if err != nil {
		return err
	}
	if !insideKitty {
		slog.Debug("indicator: hook invoked outside Kitty, ignoring", "event", eventOrAction)
		return nil
	}
	windowID = resolvedWindowID

	preservesIcon := PreservesCurrent(eventOrAction) && action == "active"

	windows, err := client.ListWindows()

	var tab *kitty.Tab
	var win *kitty.Window
	var sessionTitle string
	var currentIconState string
	var titleMuted bool
	if err == nil {
		t, w, found := kitty.FindTabByWindowID(windows, windowID)
		if found {
			tab = t
			win = w
			parts := palette.ParseTitleParts(tab.Title)
			sessionTitle = parts.Base
			currentIconState = parts.State
			titleMuted = parts.Muted
		}
	} else {
		slog.Debug("indicator: kitty lookup failed", "err", err)
	}

	if isAgentSessionLoopEvent(eventOrAction) {
		handled, err := runLoopLifecycleEventWithTargets(
			eventOrAction,
			action,
			amxBin,
			payload,
			pipeMode,
			client,
			dc,
			windows,
		)
		if handled || err != nil {
			return err
		}
	}

	contextLimitDetected := payloadHasContextLimit(payload)
	if !contextLimitDetected && shouldProbeWindowForContextLimit(eventOrAction, action) {
		text, textErr := client.GetText(kitty.MatchWindowByID(windowID), "screen")
		if textErr != nil {
			slog.Debug("indicator: context-limit probe failed", "window_id", windowID, "event", eventOrAction, "err", textErr)
		} else if contextlimit.ScreenShowsMarker(text) {
			action = "stop_failure"
			preservesIcon = false
			contextLimitDetected = true
		}
	}

	var profile, harnessName string
	var muted bool
	if win != nil {
		profile = win.UserVars.EffectiveProfile()
		harnessName = win.UserVars.EffectiveHarness()
		muted = titleMuted || win.UserVars.Mute != ""
	}
	resetFlowDetected := payloadHasResetFlow(harnessName, eventOrAction, payload)
	toolName := payload.EffectiveToolName()
	maybeDelayResetSensitiveStop(eventOrAction, action, payload, contextLimitDetected, resetFlowDetected)
	log := slog.With(
		"window_id", windowID,
		"profile", profile,
		"harness", harnessName,
		"event", eventOrAction,
		"action", action,
		"tool_name", toolName,
		"source", payload.Source,
		"session_reason", payload.Reason,
		"notification_type", payload.EffectiveNotificationType(),
		"stop_reason", payload.EffectiveStopReason(),
		"context_limit", contextLimitDetected,
		"reset_flow", resetFlowDetected,
		"notification_title", payload.Title,
		"notification_message", payload.Message,
		"pipe_mode", pipeMode,
	)
	log.Debug("indicator")

	resp, err := dc.SendWithAutoStart(buildStateUpdateRequest(
		windowID,
		action,
		eventOrAction,
		payload,
		sessionTitle,
		profile,
		harnessName,
		currentIconState,
		pipeMode,
		preservesIcon,
		contextLimitDetected,
		resetFlowDetected,
		muted,
	), amxBin)
	if err != nil {
		log.Debug("indicator: daemon unavailable, updating title without guard", "err", err)
		if tab == nil {
			return fmt.Errorf("indicator: window %d not found and daemon unavailable", windowID)
		}
		nextIcon, skip := daemonDownIconState(action, currentIconState, preservesIcon, harnessName, resetFlowDetected)
		if skip {
			return nil
		}
		return finalizeTitleUpdate(tab, win, windowID, nextIcon, eventOrAction, payload, client, windows)
	}
	if !resp.OK {
		return fmt.Errorf("indicator: daemon error: %s", resp.Error)
	}
	if !resp.Allowed {
		log.Debug("indicator: guard blocked transition")
		return nil
	}
	if resp.IconState == daemon.IconStatePreserve {
		log.Debug("indicator: preserve current icon")
		if eventOrAction != "AgentSessionLoopFinished" {
			return nil
		}
		return finalizeTitleUpdate(tab, win, windowID, currentIconState, eventOrAction, payload, client, windows)
	}

	return finalizeTitleUpdate(tab, win, windowID, resp.IconState, eventOrAction, payload, client, windows)
}

type loopLifecycleTarget struct {
	windowID  int
	tab       *kitty.Tab
	win       *kitty.Window
	workspace agentloop.WorkspaceTarget
}

func runLoopLifecycleEventWithTargets(
	eventOrAction string,
	action string,
	amxBin string,
	payload HookPayload,
	pipeMode bool,
	client kittyTitleSetter,
	dc indicatorDaemonClient,
	windows []kitty.OSWindow,
) (bool, error) {
	targets, openTargets := loopLifecycleTargets(windows, payload)
	if len(targets) == 0 {
		return false, nil
	}

	var errs []error
	for _, target := range targets {
		if err := runLoopLifecycleTarget(
			eventOrAction,
			action,
			amxBin,
			payload,
			pipeMode,
			client,
			dc,
			target,
			openTargets,
		); err != nil {
			errs = append(errs, err)
		}
	}
	return true, errors.Join(errs...)
}

func runLoopLifecycleTarget(
	eventOrAction string,
	action string,
	amxBin string,
	payload HookPayload,
	pipeMode bool,
	client kittyTitleSetter,
	dc indicatorDaemonClient,
	target loopLifecycleTarget,
	openTargets []agentloop.WorkspaceTarget,
) error {
	parts := palette.ParseTitleParts(target.tab.Title)
	sessionTitle := parts.Base
	currentIconState := parts.State
	titleMuted := parts.Muted

	var profile, harnessName string
	muted := titleMuted
	if target.win != nil {
		profile = target.win.UserVars.EffectiveProfile()
		harnessName = target.win.UserVars.EffectiveHarness()
		muted = titleMuted || target.win.UserVars.Mute != ""
	}

	log := slog.With(
		"window_id", target.windowID,
		"profile", profile,
		"harness", harnessName,
		"event", eventOrAction,
		"action", action,
		"source", payload.Source,
		"payload_cwd", payload.CWD,
		"payload_harness", payload.Harness,
		"payload_window_id", payload.EffectiveKittyWindowID(),
		"payload_state_path", payload.StatePath,
		"pipe_mode", pipeMode,
	)
	log.Debug("indicator: routing loop lifecycle event to target window")

	resp, err := dc.SendWithAutoStart(buildStateUpdateRequest(
		target.windowID,
		action,
		eventOrAction,
		payload,
		sessionTitle,
		profile,
		harnessName,
		currentIconState,
		pipeMode,
		false,
		false,
		false,
		muted,
	), amxBin)
	if err != nil {
		log.Debug("indicator: daemon unavailable, updating target title without guard", "err", err)
		nextIcon, skip := daemonDownIconState(action, currentIconState, false, harnessName, false)
		if skip {
			nextIcon = currentIconState
		}
		return finalizeLoopLifecycleTargetTitleUpdate(target, nextIcon, eventOrAction, client, openTargets)
	}
	if !resp.OK {
		return fmt.Errorf("indicator: daemon error: %s", resp.Error)
	}

	iconState := resp.IconState
	if !resp.Allowed || iconState == daemon.IconStatePreserve {
		iconState = currentIconState
	}
	return finalizeLoopLifecycleTargetTitleUpdate(target, iconState, eventOrAction, client, openTargets)
}

func finalizeLoopLifecycleTargetTitleUpdate(
	target loopLifecycleTarget,
	iconState string,
	eventOrAction string,
	client kittyTitleSetter,
	openTargets []agentloop.WorkspaceTarget,
) error {
	return updateTitleWithLoopMarker(
		target.windowID,
		iconState,
		client,
		target.tab,
		loopMarkerForLifecycleTarget(eventOrAction, target.workspace, openTargets),
	)
}

func loopLifecycleTargets(windows []kitty.OSWindow, payload HookPayload) ([]loopLifecycleTarget, []agentloop.WorkspaceTarget) {
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
			slog.Debug("indicator: loop lifecycle target match failed",
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

func loopPayloadWorkspaceTarget(payload HookPayload) agentloop.WorkspaceTarget {
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
		slog.Debug("indicator: loop state payload read failed", "state_path", statePath, "err", err)
		return "", "", 0, false
	}
	var state loopStateWorkspace
	if err := json.Unmarshal(data, &state); err != nil {
		slog.Debug("indicator: loop state payload decode failed", "state_path", statePath, "err", err)
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

func buildStateUpdateRequest(
	windowID int,
	action string,
	eventOrAction string,
	payload HookPayload,
	sessionTitle string,
	profile string,
	harness string,
	currentIconState string,
	pipeMode bool,
	preservesIcon bool,
	contextLimitDetected bool,
	resetFlowDetected bool,
	muted bool,
) daemon.Request {
	return daemon.Request{
		Type:             daemon.TypeStateUpdate,
		WindowID:         windowID,
		Action:           action,
		Event:            eventOrAction,
		ToolName:         payload.EffectiveToolName(),
		Source:           payload.Source,
		IsPrompt:         IsPromptEvent(eventOrAction),
		PreservesIcon:    preservesIcon,
		PipeMode:         pipeMode,
		SessionTitle:     sessionTitle,
		Account:          profile,
		Harness:          harness,
		CurrentIconState: currentIconState,
		NotificationType: payload.EffectiveNotificationType(),
		StopReason:       payload.EffectiveStopReason(),
		Reason:           payload.Reason,
		ContextLimit:     contextLimitDetected,
		ResetFlow:        resetFlowDetected,
		Muted:            muted,
	}
}

func maybeDelayResetSensitiveStop(eventOrAction, action string, payload HookPayload, contextLimitDetected, resetFlowDetected bool) {
	if !shouldDelayResetSensitiveStop(eventOrAction, action, payload, contextLimitDetected, resetFlowDetected) {
		return
	}
	time.Sleep(resetFlowStopDelay)
}

func shouldDelayResetSensitiveStop(eventOrAction, action string, payload HookPayload, contextLimitDetected, resetFlowDetected bool) bool {
	if resetFlowStopDelay <= 0 || action != "stop" || contextLimitDetected || resetFlowDetected {
		return false
	}
	switch eventOrAction {
	case "Stop", "SessionEnd", "agentStop", "sessionEnd":
		return true
	case "Notification", "notification":
		return normalizedNotificationType(payload) == "agent_idle"
	default:
		return false
	}
}

// finalizeTitleUpdate applies the resolved icon state to the Kitty tab, or
// no-ops if the tab is no longer present. After a successful daemon response,
// the daemon has already recorded the event and resolved any guard transition by
// the time this runs, so a missing tab is not an error — returning one
// previously caused Copilot CLI to log every such call as
// `Hook execution failed: HookExitCodeError` even when the event was captured
// correctly (and, on permissionRequest hooks, could be treated as a deny).
func finalizeTitleUpdate(tab *kitty.Tab, win *kitty.Window, windowID int, iconState string, eventOrAction string, payload HookPayload, client kittyTitleSetter, windows []kitty.OSWindow) error {
	if tab == nil {
		slog.Debug("indicator: tab not found after daemon update; skipping title update", "window_id", windowID)
		return nil
	}

	return updateTitleWithLoopMarker(windowID, iconState, client, tab, loopMarkerForLifecycleRun(eventOrAction, tab, win, payload, windows))
}

func loopMarkerForLifecycleRun(eventOrAction string, tab *kitty.Tab, win *kitty.Window, payload HookPayload, windows []kitty.OSWindow) *bool {
	if !isAgentSessionLoopEvent(eventOrAction) || tab == nil {
		return nil
	}

	payloadTarget := loopPayloadWorkspaceTarget(payload)
	if eventOrAction == agentSessionLoopStarted || eventOrAction == agentSessionLoopTurnStarted {
		if payloadTarget.WindowID > 0 && win != nil && payloadTarget.WindowID == win.ID {
			return boolPtr(true)
		}
		if payloadTarget.CWD != "" {
			match, known, err := payloadCWDMatchesKnownTabDirectory(payloadTarget.CWD, tab)
			if err != nil {
				slog.Debug("indicator: preserving loop marker after loop start; payload directory match failed", "cwd", payloadTarget.CWD, "err", err)
				return nil
			}
			if known && !match {
				slog.Debug("indicator: preserving loop marker after loop start; payload cwd differs from tab directory", "cwd", payloadTarget.CWD)
				return nil
			}
		}
		return boolPtr(true)
	}

	directory := payloadTarget.CWD
	payloadMatchesWindow := payloadTarget.WindowID > 0 && win != nil && payloadTarget.WindowID == win.ID
	if directory != "" && !payloadMatchesWindow {
		match, known, err := payloadCWDMatchesKnownTabDirectory(directory, tab)
		if err != nil {
			slog.Debug("indicator: preserving loop marker after loop finish; payload directory match failed", "cwd", directory, "err", err)
			return nil
		}
		if known && !match {
			slog.Debug("indicator: preserving loop marker after loop finish; payload cwd differs from tab directory", "cwd", directory)
			return nil
		}
	}
	if directory == "" {
		directory = finishedLoopFallbackDirectory(tab, win)
	}
	if strings.TrimSpace(directory) == "" {
		slog.Debug("indicator: preserving loop marker after loop finish; window directory missing")
		return nil
	}
	harnessName := payloadTarget.Harness
	if win != nil {
		if h := win.UserVars.EffectiveHarness(); h != "" {
			harnessName = h
		}
	}
	_, openTargets := loopLifecycleWindowTargets(windows)
	if len(openTargets) == 0 {
		openTargets = workspaceTargetsForTab(tab)
	}
	return loopMarkerForLifecycleTarget(
		eventOrAction,
		agentloop.WorkspaceTarget{CWD: directory, Harness: harnessName, WindowID: payloadTarget.WindowID},
		openTargets,
	)
}

func loopMarkerForLifecycleTarget(eventOrAction string, target agentloop.WorkspaceTarget, openTargets []agentloop.WorkspaceTarget) *bool {
	switch eventOrAction {
	case agentSessionLoopStarted, agentSessionLoopTurnStarted:
		return boolPtr(true)
	case agentSessionLoopFinished:
		if strings.TrimSpace(target.CWD) == "" && target.WindowID <= 0 {
			slog.Debug("indicator: preserving loop marker after loop finish; target directory missing")
			return nil
		}
		candidates, err := indicatorLoopDiscover(agentloop.DiscoverOptions{})
		if err != nil {
			slog.Debug("indicator: preserving loop marker after loop finish; discovery failed", "err", err)
			return nil
		}
		looping, err := agentloop.HasMatchingWorkspace(candidates, target, openTargets)
		if err != nil {
			slog.Debug("indicator: preserving loop marker after loop finish; workspace match failed", "directory", target.CWD, "harness", target.Harness, "window_id", target.WindowID, "err", err)
			return nil
		}
		return boolPtr(looping)
	default:
		return nil
	}
}

func workspaceTargetsForTab(tab *kitty.Tab) []agentloop.WorkspaceTarget {
	if tab == nil {
		return nil
	}
	targets := make([]agentloop.WorkspaceTarget, 0, len(tab.Windows))
	for i := range tab.Windows {
		target, ok := managedWindowWorkspaceTarget(tab.Windows[i])
		if ok {
			targets = append(targets, target)
		}
	}
	return targets
}

func boolPtr(value bool) *bool {
	return &value
}

func payloadCWDMatchesKnownTabDirectory(cwd string, tab *kitty.Tab) (match bool, known bool, err error) {
	for i := range tab.Windows {
		directory := strings.TrimSpace(tab.Windows[i].UserVars.EffectiveDirectory())
		if directory == "" {
			continue
		}
		known = true
		same, cmpErr := sameCanonicalDirectory(cwd, directory)
		if cmpErr != nil {
			return false, true, cmpErr
		}
		if same {
			return true, true, nil
		}
	}
	return false, known, nil
}

func sameCanonicalDirectory(a, b string) (bool, error) {
	canonicalA, err := agentloop.CanonicalizePath(a)
	if err != nil {
		return false, err
	}
	canonicalB, err := agentloop.CanonicalizePath(b)
	if err != nil {
		return false, err
	}
	return canonicalA == canonicalB, nil
}

func finishedLoopFallbackDirectory(tab *kitty.Tab, win *kitty.Window) string {
	if win != nil {
		if directory := windowDirectoryHint(*win); directory != "" {
			return directory
		}
	}
	if tab == nil {
		return ""
	}
	hookWindowID := 0
	if win != nil {
		hookWindowID = win.ID
	}
	for i := range tab.Windows {
		if tab.Windows[i].ID == hookWindowID {
			continue
		}
		if directory := strings.TrimSpace(tab.Windows[i].UserVars.EffectiveDirectory()); directory != "" {
			return directory
		}
	}
	for i := range tab.Windows {
		if tab.Windows[i].ID == hookWindowID {
			continue
		}
		if directory := firstForegroundCWD(tab.Windows[i]); directory != "" {
			return directory
		}
	}
	for i := range tab.Windows {
		if tab.Windows[i].ID == hookWindowID {
			continue
		}
		if directory := strings.TrimSpace(tab.Windows[i].CWD); directory != "" {
			return directory
		}
	}
	return ""
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

// daemonDownIconState mirrors the daemon's preserve-current and idle/clear
// handling for the daemon-unavailable fallback: child-complete events keep the
// current icon unchanged, and a bare idle/clear must not wipe a still-relevant
// stop/permission/warning/error/elicitation icon the user has not yet
// acknowledged.
// Copilot also lacks a documented postCompact hook, so an idle/clear arriving
// while the compact icon is still visible must synthesize a stop icon instead of
// silently clearing it. Other active-like icons are still cleared normally.
func daemonDownIconState(action, currentIconState string, preservesIcon bool, harnessName string, resetFlow bool) (next string, skip bool) {
	if resetFlow {
		switch action {
		case "stop", "idle", "clear", "user_clear":
			action = "user_clear"
		}
	}
	if preservesIcon {
		return "", true
	}
	if action == "idle" || action == "clear" {
		if currentIconState == "compact" && harness.Normalize(harnessName) == "copilot" {
			return "stop", false
		}
		switch currentIconState {
		case "bolt", "compact", "subagent":
			return "", false
		default:
			return "", true
		}
	}
	next = daemon.IconState(action)
	if next == daemon.IconStatePreserve {
		return "", true
	}
	return next, false
}

func updateTitle(windowID int, iconState string, client kittyTitleSetter, tab *kitty.Tab) error {
	return updateTitleWithLoopMarker(windowID, iconState, client, tab, nil)
}

func updateTitleWithLoopMarker(windowID int, iconState string, client kittyTitleSetter, tab *kitty.Tab, loopMarker *bool) error {
	parts := palette.ParseTitleParts(tab.Title)
	parts.State = iconState
	if loopMarker != nil {
		parts.Looping = *loopMarker
	}
	newTitle := palette.BuildTitleParts(parts)

	if newTitle == tab.Title {
		return nil
	}

	matchExpr := kitty.MatchTabByWindowID(windowID)
	if err := client.SetTabTitle(matchExpr, newTitle); err != nil {
		return fmt.Errorf("indicator: setting tab title: %w", err)
	}
	slog.Debug("indicator: title updated", "window_id", windowID, "old", tab.Title, "new", newTitle)
	return nil
}

// resolveHookWindowID distinguishes a genuine Kitty hook invocation from a
// shared-home hook firing in a non-Kitty surface such as VS Code. Those
// outside-Kitty calls must succeed quietly, while a broken Kitty env should
// still fail loudly so regressions remain visible.
func resolveHookWindowID(windowID int) (int, bool, error) {
	switch {
	case windowID < 0:
		return 0, true, fmt.Errorf("indicator: invalid window ID %d", windowID)
	case windowID > 0:
		return windowID, true, nil
	case !runningInsideKitty():
		return 0, false, nil
	default:
		id, err := windowIDFromEnv()
		if err != nil {
			return 0, true, err
		}
		return id, true, nil
	}
}

func runningInsideKitty() bool {
	return strings.TrimSpace(os.Getenv("KITTY_LISTEN_ON")) != ""
}

func windowIDFromEnv() (int, error) {
	s := strings.TrimSpace(os.Getenv("KITTY_WINDOW_ID"))
	if s == "" {
		return 0, fmt.Errorf("indicator: KITTY_WINDOW_ID is not set")
	}
	id, err := strconv.Atoi(s)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("indicator: invalid KITTY_WINDOW_ID %q", s)
	}
	return id, nil
}
