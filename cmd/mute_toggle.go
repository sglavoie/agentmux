package cmd

import (
	"os"
	"strconv"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/spf13/cobra"
)

var muteToggleWindowID int

var muteToggleCmd = &cobra.Command{
	Use:   "mute-toggle",
	Short: "Toggle mute on the focused Agent Mux tab",
	Long: `Flips the cc_mute user var on the window identified by --window-id
(or $KITTY_WINDOW_ID when the flag is zero) and rewrites the tab title so
the 🔇 glyph appears or disappears immediately. Muted sessions suppress all
macOS notifications originating from that window. If the active window is
not a managed Agent Mux session, the command is a silent no-op.`,
	RunE: runMuteToggle,
}

func init() {
	rootCmd.AddCommand(muteToggleCmd)
	muteToggleCmd.Flags().IntVar(&muteToggleWindowID, "window-id", 0,
		"Kitty window ID of the target tab (defaults to $KITTY_WINDOW_ID)")
}

func runMuteToggle(_ *cobra.Command, _ []string) error {
	return muteToggle(kitty.NewClient(kitty.NewRealRunner()),
		resolveWindowID(muteToggleWindowID))
}

func resolveWindowID(flag int) int {
	if flag != 0 {
		return flag
	}
	if s := os.Getenv("KITTY_WINDOW_ID"); s != "" {
		id, _ := strconv.Atoi(s)
		return id
	}
	return 0
}

func muteToggle(client kitty.KittyClient, windowID int) error {
	if windowID == 0 {
		return nil
	}
	sessions, err := session.List(client)
	if err != nil {
		return err
	}
	rs, ok := session.FindByWindowID(sessions, windowID)
	if !ok {
		return nil
	}
	if rs.Muted {
		err = session.Unmute(client, rs.WindowID)
	} else {
		err = session.Mute(client, rs.WindowID)
	}
	if err != nil {
		return err
	}
	parts := rs.TitleParts()
	parts.Muted = !rs.Muted
	newTitle := palette.BuildTitleParts(parts)
	return client.SetTabTitle(kitty.MatchTabByWindowID(rs.WindowID), newTitle)
}
