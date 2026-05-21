package indicator

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// Run is the core hook-indicator entry point. It resolves the action, contacts
// the daemon for a guard decision, then updates the Kitty tab title if allowed.
//
//   - eventOrAction: harness event name (e.g. "Stop", "agentStop") or amx action (e.g. "stop")
//   - windowID: 0 means read from $KITTY_WINDOW_ID
//   - amxBin: absolute path to the amx binary, used for daemon auto-start
func Run(eventOrAction string, windowID int, amxBin string) error {
	payload, payloadErr := ReadHookPayload(os.Stdin)
	if payloadErr != nil {
		slog.Warn("indicator: ignoring unreadable hook payload", "event", eventOrAction, "err", payloadErr)
	}
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

	if windowID == 0 {
		windowID = windowIDFromEnv()
	}
	if windowID == 0 {
		return fmt.Errorf("indicator: KITTY_WINDOW_ID is not set")
	}

	pipeMode := IsPipeMode()
	preservesIcon := PreservesCurrent(eventOrAction, payload) && action == "active"

	runner := kitty.NewRealRunner()
	client := kitty.NewClient(runner)
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
		} else if screenShowsContextLimit(text) {
			action = "stop_failure"
			preservesIcon = false
			contextLimitDetected = true
		}
	}

	var profile, harness string
	if win != nil {
		profile = win.UserVars.EffectiveProfile()
		harness = win.UserVars.EffectiveHarness()
	}
	log := slog.With(
		"window_id", windowID,
		"profile", profile,
		"harness", harness,
		"event", eventOrAction,
		"action", action,
		"tool_name", payload.ToolName,
		"session_reason", payload.Reason,
		"notification_type", payload.EffectiveNotificationType(),
		"stop_reason", payload.EffectiveStopReason(),
		"context_limit", contextLimitDetected,
		"notification_title", payload.Title,
		"notification_message", payload.Message,
		"pipe_mode", pipeMode,
	)
	log.Debug("indicator")

	dc := daemon.NewClient()
	resp, err := dc.SendWithAutoStart(daemon.Request{
		Type:             daemon.TypeStateUpdate,
		WindowID:         windowID,
		Action:           action,
		Event:            eventOrAction,
		ToolName:         payload.ToolName,
		IsPrompt:         IsPromptEvent(eventOrAction),
		PreservesIcon:    preservesIcon,
		PipeMode:         pipeMode,
		SessionTitle:     sessionTitle,
		Account:          profile,
		CurrentIconState: currentIconState,
	}, amxBin)
	if err != nil {
		log.Debug("indicator: daemon unavailable, updating title without guard", "err", err)
		if tab == nil {
			return fmt.Errorf("indicator: window %d not found and daemon unavailable", windowID)
		}
		nextIcon, skip := daemonDownIconState(action, currentIconState, preservesIcon)
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

// finalizeTitleUpdate applies the daemon-resolved icon state to the Kitty tab,
// or no-ops if the tab is no longer present. The daemon has already recorded
// the event and resolved any guard transition by the time this runs, so a
// missing tab is not an error — returning one previously caused Copilot CLI to
// log every such call as `Hook execution failed: HookExitCodeError` even when
// the event was captured correctly (and, on permissionRequest hooks, could be
// treated as a deny).
func finalizeTitleUpdate(tab *kitty.Tab, windowID int, iconState string, client kitty.KittyClient) error {
	if tab == nil {
		slog.Debug("indicator: tab not found after daemon update; skipping title update", "window_id", windowID)
		return nil
	}
	return updateTitle(windowID, iconState, client, tab)
}

// daemonDownIconState mirrors the daemon's preserve-current and idle/clear
// handling for the daemon-unavailable fallback: child-complete events keep the
// current icon unchanged, and a bare idle/clear must not wipe a still-relevant
// stop/permission/error/elicitation icon the user has not yet acknowledged.
// Only icons that represent an in-flight agent (bolt/compact/subagent) are
// cleared by idle/clear; everything else is left untouched.
func daemonDownIconState(action, currentIconState string, preservesIcon bool) (next string, skip bool) {
	if preservesIcon {
		return "", true
	}
	if action == "idle" || action == "clear" {
		switch currentIconState {
		case "bolt", "compact", "subagent":
			return "", false
		default:
			return "", true
		}
	}
	return daemon.IconState(action), false
}

func updateTitle(windowID int, iconState string, client kitty.KittyClient, tab *kitty.Tab) error {
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

func windowIDFromEnv() int {
	s := os.Getenv("KITTY_WINDOW_ID")
	if s == "" {
		return 0
	}
	id, _ := strconv.Atoi(s)
	return id
}
