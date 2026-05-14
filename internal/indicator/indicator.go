package indicator

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/sglavoie/cc-manager-kitty/internal/daemon"
	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
	"github.com/sglavoie/cc-manager-kitty/internal/palette"
)

// Run is the core hook-indicator entry point. It resolves the action, contacts
// the daemon for a guard decision, then updates the Kitty tab title if allowed.
//
//   - eventOrAction: Claude event name (e.g. "Stop") or ccm action (e.g. "stop")
//   - windowID: 0 means read from $KITTY_WINDOW_ID
//   - ccmBin: absolute path to the ccm binary, used for daemon auto-start
func Run(eventOrAction string, windowID int, ccmBin string) error {
	action, ok := ResolveAction(eventOrAction)
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
	slog.Debug("indicator", "action", action, "window_id", windowID, "pipe_mode", pipeMode)

	runner := kitty.NewRealRunner()
	client := kitty.NewClient(runner)
	windows, err := client.ListWindows()

	var tab *kitty.Tab
	var sessionTitle string
	if err == nil {
		t, _, found := kitty.FindTabByWindowID(windows, windowID)
		if found {
			tab = t
			base, _, _ := palette.ParseTitle(tab.Title)
			sessionTitle = base
		}
	} else {
		slog.Debug("indicator: kitty lookup failed", "err", err)
	}

	dc := daemon.NewClient()
	resp, err := dc.SendWithAutoStart(daemon.Request{
		Type:          daemon.TypeStateUpdate,
		WindowID:      windowID,
		Action:        action,
		Event:         eventOrAction,
		IsPrompt:      IsPromptEvent(eventOrAction),
		PreservesIcon: PreservesCurrent(eventOrAction),
		PipeMode:      pipeMode,
		SessionTitle:  sessionTitle,
	}, ccmBin)
	if err != nil {
		slog.Debug("indicator: daemon unavailable, updating title without guard", "err", err)
		if tab == nil {
			return fmt.Errorf("indicator: window %d not found and daemon unavailable", windowID)
		}
		return updateTitle(windowID, daemon.IconState(action), client, tab)
	}
	if !resp.OK {
		return fmt.Errorf("indicator: daemon error: %s", resp.Error)
	}
	if !resp.Allowed {
		slog.Debug("indicator: guard blocked transition", "action", action, "window_id", windowID)
		return nil
	}
	if resp.IconState == daemon.IconStatePreserve {
		slog.Debug("indicator: preserve current icon", "action", action, "window_id", windowID)
		return nil
	}

	if tab == nil {
		return fmt.Errorf("indicator: window %d not found in Kitty tab list", windowID)
	}
	return updateTitle(windowID, resp.IconState, client, tab)
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
