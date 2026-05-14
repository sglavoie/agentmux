package session

import (
	"fmt"
	"time"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
	"github.com/sglavoie/cc-manager-kitty/internal/palette"
)

// OpenDirectory opens a session's directory in a new Kitty tab as a plain
// shell (no Claude Code). The tab gets the correct title and account color
// but no user vars, so it won't appear in the dashboard's session list.
func OpenDirectory(client kitty.KittyClient, sess config.Session, acc config.Account, cfg *config.Config) error {
	formattedTitle := FormatTitle(sess)

	running, err := List(client)
	if err != nil {
		return fmt.Errorf("listing running sessions: %w", err)
	}
	beforeIDs := tabIDSet(running)

	loc, prevTabID, err := ComputeInsertLocation(client, cfg.Defaults.Sort, acc.Name, formattedTitle)
	if err != nil {
		loc = ""
		prevTabID = 0
	}

	if loc == "after" && prevTabID != 0 {
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
		applyColorWithRetry(client, beforeIDs, formattedTitle, color)
	}

	return nil
}
