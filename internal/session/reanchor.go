package session

import (
	"log/slog"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// ReanchorTab writes the standard amx user-var set onto the ghost tab's
// surviving window. After a successful call, the next poll of `List` sees the
// tab via the normal anchor-window path and any harness relaunched in that
// window has hooks resolve EffectiveProfile/EffectiveHarness correctly.
func ReanchorTab(client kitty.KittyClient, ghost GhostTab, defaults config.Defaults) error {
	vars := BuildUserVars(ghost.Session, ghost.Account, defaults)
	return client.SetUserVars(kitty.MatchWindowByID(ghost.WindowID), vars)
}

// ReanchorAll attempts to re-stamp every ghost in the slice. Per-ghost
// failures are logged at debug and otherwise ignored: a failed re-stamp is
// self-healing on the next tick because the title fallback re-detects the
// same tab.
func ReanchorAll(client kitty.KittyClient, ghosts []GhostTab, defaults config.Defaults) {
	for _, g := range ghosts {
		if err := ReanchorTab(client, g, defaults); err != nil {
			slog.Debug("reanchor failed",
				"tab_id", g.TabID,
				"window_id", g.WindowID,
				"profile", g.Profile,
				"project", g.Project,
				"err", err,
			)
		}
	}
}
