package session

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// OpenDirectory opens a session's directory in a new Kitty tab as a plain
// shell (no Claude Code). The tab gets the correct title and account color
// but no user vars, so it won't appear in the dashboard's session list.
func OpenDirectory(client kitty.KittyClient, sess config.Session, acc config.Account, cfg *config.Config) error {
	formattedTitle := FormatTitle(sess, acc, cfg)

	osWindows, err := client.ListWindows()
	if err != nil {
		return fmt.Errorf("listing running sessions: %w", err)
	}
	// Include every tab (managed and unmanaged) so the color retry below
	// cannot repaint a pre-existing tab whose title coincides with
	// formattedTitle.
	beforeIDs := allTabIDs(osWindows)

	loc, prevTabID, err := ComputeInsertLocation(client, cfg.Defaults.Sort, effectiveSessionProfile(sess, acc), formattedTitle)
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

	colorName := acc.Color
	if color, err := palette.Lookup(colorName); err == nil {
		if colorErr := applyColorWithRetry(client, beforeIDs, formattedTitle, color); colorErr != nil {
			slog.Warn("applying tab color after open",
				"title", formattedTitle,
				"account", acc.Name,
				"harness", acc.EffectiveHarness(),
				"profile_color", acc.Color,
				"error", colorErr,
			)
		}
	}

	return nil
}

// resolveEditor picks an editor command. Order:
//
//	cfgEditor → $VISUAL → $EDITOR → "vi"
//
// Splits the chosen value as shell arguments so "code --wait" and quoted
// editor paths both work. Returns (name, args, nil) on success or ("", nil,
// error) if every source is empty or the selected command cannot be parsed.
func resolveEditor(cfgEditor string) (string, []string, error) {
	candidates := []string{
		cfgEditor,
		os.Getenv("VISUAL"),
		os.Getenv("EDITOR"),
		"vi",
	}
	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		parts, err := splitShellArgs(c)
		if err != nil {
			return "", nil, fmt.Errorf("parsing editor command %q: %w", c, err)
		}
		if len(parts) == 0 {
			continue
		}
		return parts[0], append([]string{}, parts[1:]...), nil
	}
	return "", nil, fmt.Errorf("no editor configured (cfg.Defaults.Editor, $VISUAL, $EDITOR all empty)")
}

// OpenInEditor opens a session's directory in a new Kitty tab running the
// user's configured editor. The tab gets the correct title and account color
// but no user vars, so it won't appear in the dashboard's session list
// (same contract as OpenDirectory).
func OpenInEditor(client kitty.KittyClient, sess config.Session, acc config.Account, cfg *config.Config) error {
	formattedTitle := FormatTitle(sess, acc, cfg)

	osWindows, err := client.ListWindows()
	if err != nil {
		return fmt.Errorf("listing running sessions: %w", err)
	}
	beforeIDs := allTabIDs(osWindows)

	loc, prevTabID, err := ComputeInsertLocation(client, cfg.Defaults.Sort, effectiveSessionProfile(sess, acc), formattedTitle)
	if err != nil {
		loc = ""
		prevTabID = 0
	}

	if (loc == "after" || loc == "before") && prevTabID != 0 {
		_ = client.FocusTab(kitty.MatchTabByID(prevTabID))
		time.Sleep(100 * time.Millisecond)
	}

	name, editorArgs, err := resolveEditor(cfg.Defaults.Editor)
	if err != nil {
		return fmt.Errorf("resolving editor for %q: %w", formattedTitle, err)
	}

	expandedDir := config.ExpandTilde(sess.Directory)
	launchOpts := kitty.LaunchOpts{
		Title:     formattedTitle,
		Directory: expandedDir,
		Location:  locationArg(loc),
		Cmdline:   append([]string{name}, append(editorArgs, expandedDir)...),
	}

	if err := client.LaunchTab(launchOpts); err != nil {
		return fmt.Errorf("opening editor for %q: %w", formattedTitle, err)
	}

	colorName := acc.Color
	if color, err := palette.Lookup(colorName); err == nil {
		if colorErr := applyColorWithRetry(client, beforeIDs, formattedTitle, color); colorErr != nil {
			slog.Warn("applying tab color after open-in-editor",
				"title", formattedTitle,
				"account", acc.Name,
				"harness", acc.EffectiveHarness(),
				"profile_color", acc.Color,
				"error", colorErr,
			)
		}
	}

	return nil
}

// OpenPathInEditor launches a Kitty tab running the user's editor against an
// arbitrary directory. Unlike OpenInEditor it has no profile/harness context,
// so it skips color application and account-derived tab styling. title becomes
// the new tab's title.
func OpenPathInEditor(client kitty.KittyClient, path, title string, cfg *config.Config) error {
	name, editorArgs, err := resolveEditor(cfg.Defaults.Editor)
	if err != nil {
		return fmt.Errorf("resolving editor for %q: %w", title, err)
	}

	expandedPath := config.ExpandTilde(path)
	launchOpts := kitty.LaunchOpts{
		Title:     title,
		Directory: expandedPath,
		Cmdline:   append([]string{name}, append(editorArgs, expandedPath)...),
	}

	if err := client.LaunchTab(launchOpts); err != nil {
		return fmt.Errorf("opening editor for %q: %w", title, err)
	}
	return nil
}
