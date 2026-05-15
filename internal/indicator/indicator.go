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

	runner := kitty.NewRealRunner()
	client := kitty.NewClient(runner)
	windows, err := client.ListWindows()

	var tab *kitty.Tab
	var win *kitty.Window
	var sessionTitle string
	if err == nil {
		t, w, found := kitty.FindTabByWindowID(windows, windowID)
		if found {
			tab = t
			win = w
			base, _, _ := palette.ParseTitle(tab.Title)
			sessionTitle = base
		}
	} else {
		slog.Debug("indicator: kitty lookup failed", "err", err)
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
		"pipe_mode", pipeMode,
	)
	log.Debug("indicator")

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
		Account:       profile,
	}, amxBin)
	if err != nil {
		log.Debug("indicator: daemon unavailable, updating title without guard", "err", err)
		if tab == nil {
			return fmt.Errorf("indicator: window %d not found and daemon unavailable", windowID)
		}
		return updateTitle(windowID, daemon.IconState(action), client, tab)
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
