package session

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// OpenDirectory opens a session's directory in a new Kitty tab as a plain
// shell (no Claude Code). The tab gets the correct title and account color
// but no user vars, so it won't appear in the dashboard's session list.
func OpenDirectory(client kitty.KittyClient, sess config.Session, acc config.Account, cfg *config.Config) error {
	formattedTitle := FormatTitle(sess)

	osWindows, err := client.ListWindows()
	if err != nil {
		return fmt.Errorf("listing running sessions: %w", err)
	}
	// Include every tab (managed and unmanaged) so the color retry below
	// cannot repaint a pre-existing tab whose title coincides with
	// formattedTitle.
	beforeIDs := allTabIDs(osWindows)

	loc, prevTabID, err := ComputeInsertLocation(client, cfg.Defaults.Sort, acc.Name, formattedTitle)
	if err != nil {
		loc = ""
		prevTabID = 0
	}

	if (loc == "after" || loc == "before") && prevTabID != 0 {
		_ = client.FocusTab(kitty.MatchTabByID(prevTabID))
		time.Sleep(100 * time.Millisecond)
	}

	launchOpts := kitty.LaunchOpts{
		Title:     formattedTitle,
		Directory: config.ExpandTilde(sess.Directory),
		Location:  locationArg(loc),
	}

	if err := client.LaunchTab(launchOpts); err != nil {
		return fmt.Errorf("opening directory for %q: %w", formattedTitle, err)
	}

	if color, err := palette.Lookup(acc.Color); err == nil {
		if colorErr := applyColorWithRetry(client, beforeIDs, formattedTitle, color); colorErr != nil {
			slog.Warn("applying tab color after open",
				"title", formattedTitle,
				"account", acc.Name,
				"error", colorErr,
			)
		}
	}

	return nil
}
