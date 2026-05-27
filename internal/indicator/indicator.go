package indicator

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

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
	if err == nil {
		t, w, found := kitty.FindTabByWindowID(windows, windowID)
		if found {
			tab = t
			win = w
			base, state, _ := palette.ParseTitle(tab.Title)
			sessionTitle = base
			currentIconState = state
		}
	} else {
		slog.Debug("indicator: kitty lookup failed", "err", err)
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
	if win != nil {
		profile = win.UserVars.EffectiveProfile()
		harnessName = win.UserVars.EffectiveHarness()
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
		return updateTitle(windowID, nextIcon, client, tab)
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
		return nil
	}

	return finalizeTitleUpdate(tab, windowID, resp.IconState, client)
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
		return payload.EffectiveNotificationType() == "agent_idle"
	default:
		return false
	}
}

// finalizeTitleUpdate applies the daemon-resolved icon state to the Kitty tab,
// or no-ops if the tab is no longer present. The daemon has already recorded
// the event and resolved any guard transition by the time this runs, so a
// missing tab is not an error — returning one previously caused Copilot CLI to
// log every such call as `Hook execution failed: HookExitCodeError` even when
// the event was captured correctly (and, on permissionRequest hooks, could be
// treated as a deny).
func finalizeTitleUpdate(tab *kitty.Tab, windowID int, iconState string, client kittyTitleSetter) error {
	if tab == nil {
		slog.Debug("indicator: tab not found after daemon update; skipping title update", "window_id", windowID)
		return nil
	}
	return updateTitle(windowID, iconState, client, tab)
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
	return daemon.IconState(action), false
}

func updateTitle(windowID int, iconState string, client kittyTitleSetter, tab *kitty.Tab) error {
	base, _, starred := palette.ParseTitle(tab.Title)
	newTitle := palette.BuildTitle(base, iconState, starred)

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
