package session

import (
	"fmt"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/spf13/cobra"
)

// MuteToggleCommand returns the root-level mute-toggle leaf so the root command
// can register it.
func MuteToggleCommand() *cobra.Command { return muteToggleCmd }

var (
	muteToggleWindowID int
	muteToggleJSON     bool
	muteToggleClient   = cli.NewKittyClient
)

var muteToggleCmd = &cobra.Command{
	Use:   "mute-toggle",
	Short: "Toggle mute on the focused Agent Mux tab",
	Long: `Flips the cc_mute user var on the window identified by --window-id
(or $KITTY_WINDOW_ID when the flag is zero) and rewrites the tab title so
the 🔇 glyph appears or disappears immediately. Muted sessions suppress all
macOS notifications originating from that window. If the active window is
not a managed Agent Mux session, the command is a silent no-op.

With --json it reports {window_id, muted, changed}: muted is the window's
mute state after the command, and changed is false for the no-op.`,
	RunE: runMuteToggle,
}

func init() {
	muteToggleCmd.Flags().IntVar(&muteToggleWindowID, "window-id", 0,
		"Kitty window ID of the target tab (defaults to $KITTY_WINDOW_ID)")
	muteToggleCmd.Flags().BoolVar(&muteToggleJSON, "json", false, "emit schema-v1 JSON")
}

// muteToggleResult is the `mute-toggle --json` data. Muted is the state the
// window is left in; Changed is false when the command was a no-op because no
// window was named or the window is not a managed session.
type muteToggleResult struct {
	WindowID int  `json:"window_id"`
	Muted    bool `json:"muted"`
	Changed  bool `json:"changed"`
}

func runMuteToggle(cmd *cobra.Command, _ []string) error {
	result, err := muteToggle(muteToggleClient(), cli.WindowIDOrEnv(muteToggleWindowID))
	if err != nil && result.Changed && muteToggleJSON {
		// The mute itself landed; only the title glyph did not. Report the
		// durable change beside the nonzero exit rather than hiding it.
		return cli.WriteJSONPartial(cmd, result, fmt.Errorf("toggle mute: update tab title: %w", err))
	}
	if err != nil {
		return cli.CommandFailure(cmd, muteToggleJSON, "mute_toggle_failed", fmt.Errorf("toggle mute: %w", err))
	}
	if muteToggleJSON {
		return cli.WriteJSON(cmd, result)
	}
	return nil
}

func muteToggle(client kitty.KittyClient, windowID int) (muteToggleResult, error) {
	result := muteToggleResult{WindowID: windowID}
	if windowID == 0 {
		return result, nil
	}
	sessions, err := session.List(client)
	if err != nil {
		return result, err
	}
	rs, ok := session.FindByWindowID(sessions, windowID)
	if !ok {
		return result, nil
	}
	result.Muted = rs.Muted
	term := kitty.NewBackend(client)
	if rs.Muted {
		err = session.Unmute(term, rs.PaneRef())
	} else {
		err = session.Mute(term, rs.PaneRef())
	}
	if err != nil {
		return result, err
	}
	// The user var is the mute; the title glyph only mirrors it. Once the var
	// is written the window is muted (or not) whatever the title write does.
	result.Muted = !rs.Muted
	result.Changed = true
	parts := rs.TitleParts()
	parts.Muted = !rs.Muted
	newTitle := palette.BuildTitleParts(parts)
	return result, term.SetTitle(rs.PaneRef(), newTitle)
}
