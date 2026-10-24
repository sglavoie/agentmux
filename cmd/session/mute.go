package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/spf13/cobra"
)

// MuteToggleCommand returns the root-level mute-toggle leaf so the root command
// can register it.
func MuteToggleCommand() *cobra.Command { return muteToggleCmd }

var (
	muteToggleWindowID       int
	muteToggleRuntimeSession string
	muteToggleJSON           bool
	muteToggleClient         = cli.NewKittyClient
)

var muteToggleCmd = &cobra.Command{
	Use:   "mute-toggle",
	Short: "Toggle mute on the current Agent Mux session (Kitty tab or tmux session)",
	Long: `Flips the cc_mute user var on the window identified by --window-id
(or $KITTY_WINDOW_ID when the flag is zero) and rewrites the tab title so
the 🔇 glyph appears or disappears immediately. Muted sessions suppress all
macOS notifications originating from that window. If the active window is
not a managed Agent Mux session, the command is a silent no-op.

With --runtime-session the session is named by its runtime session instead,
which is how one on the tmux backend is muted: it has no Kitty window, and
no Kitty is consulted. A runtime session that is not running there is an
error rather than a no-op, because it was named exactly. With neither flag
and outside Kitty, a command that runs in a managed session
(AMX_RUNTIME_SESSION_ID) mutes that session the same way.

With --json it reports {window_id, muted, changed}: muted is the window's
mute state after the command, and changed is false for the no-op. A session
named by --runtime-session reports runtime_session_id as well, and a
window_id of zero.`,
	Example: `  amx mute-toggle
  amx mute-toggle --runtime-session rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f --json`,
	Args: cobra.NoArgs,
	RunE: runMuteToggle,
}

func init() {
	muteToggleCmd.Flags().IntVar(&muteToggleWindowID, "window-id", 0,
		"Kitty window ID of the target tab (defaults to $KITTY_WINDOW_ID)")
	muteToggleCmd.Flags().StringVar(&muteToggleRuntimeSession, "runtime-session", "",
		"runtime session ID of a tmux session to mute instead of a Kitty tab")
	cli.AddJSONFlag(muteToggleCmd, &muteToggleJSON)
}

// muteToggleResult is the `mute-toggle --json` data. Muted is the state the
// window is left in; Changed is false when the command was a no-op because no
// window was named or the window is not a managed session.
type muteToggleResult struct {
	WindowID         int    `json:"window_id"`
	RuntimeSessionID string `json:"runtime_session_id,omitempty"`
	Muted            bool   `json:"muted"`
	Changed          bool   `json:"changed"`
}

// muteWriter writes a mute and the title glyph that mirrors it.
type muteWriter interface {
	backend.MetadataWriter
	SetTitle(ref backend.Ref, title string) error
}

// muteTerminal is what muting a tmux session needs from its backend.
type muteTerminal interface {
	backend.Discoverer
	muteWriter
}

func runMuteToggle(cmd *cobra.Command, _ []string) error {
	var result muteToggleResult
	var err error
	named := strings.TrimSpace(muteToggleRuntimeSession)
	if named == "" && muteToggleWindowID == 0 {
		// With nothing named and no Kitty window around it, the session
		// this command runs in is the one meant.
		named = currentRuntimeSession()
	}
	if named != "" {
		id, parseErr := cli.ParseRuntimeSessionID(named)
		if parseErr != nil {
			return cli.CommandFailure(cmd, muteToggleJSON, "invalid_arguments", parseErr)
		}
		opened, openErr := attachNewTmux()
		if openErr != nil {
			return cli.CommandFailure(cmd, muteToggleJSON, "backend_unavailable", fmt.Errorf("tmux backend unavailable: %w", openErr))
		}
		term, ok := opened.(muteTerminal)
		if !ok {
			return cli.CommandFailure(cmd, muteToggleJSON, "backend_unavailable", errors.New("tmux backend cannot mute sessions"))
		}
		result, err = muteToggleRuntime(term, id)
		if errors.Is(err, errNoRuntimeSession) {
			return cli.CommandFailure(cmd, muteToggleJSON, "not_found", err)
		}
	} else {
		result, err = muteToggle(muteToggleClient(), cli.WindowIDOrEnv(muteToggleWindowID))
	}
	if err != nil && result.Changed && muteToggleJSON {
		// The mute itself landed; only the title glyph did not. Report the
		// durable change beside the nonzero exit rather than hiding it.
		return cli.WriteJSONPartial(cmd, result, fmt.Errorf("toggle mute: update tab title: %w", err))
	}
	if err != nil {
		return cli.CommandFailure(cmd, muteToggleJSON, "mute_toggle_failed", fmt.Errorf("toggle mute: %w", cli.PlainKittyError(err)))
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
	return applyMuteToggle(kitty.NewBackend(client), rs, result)
}

// muteToggleRuntime toggles the mute of the tmux session a runtime session
// ID names. Only a pane that still carries that runtime session is touched.
func muteToggleRuntime(term muteTerminal, id runtimesession.ID) (muteToggleResult, error) {
	result := muteToggleResult{RuntimeSessionID: string(id)}
	panes, err := runtimeSessionPanes(term, id)
	if err != nil {
		return result, err
	}
	// Mute reads no configuration: the session is found by its tag alone.
	for _, rs := range session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, nil) {
		if rs.RuntimeSessionID == id {
			return applyMuteToggle(term, rs, result)
		}
	}
	return result, noRuntimeSession(id)
}

// applyMuteToggle flips rs's mute on term and fills in result.
func applyMuteToggle(term muteWriter, rs session.RunningSession, result muteToggleResult) (muteToggleResult, error) {
	var err error
	result.Muted = rs.Muted
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
