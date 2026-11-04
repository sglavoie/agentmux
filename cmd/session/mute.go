package session

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/spf13/cobra"
)

// MuteToggleCommand returns the root-level mute-toggle leaf so the root command
// can register it.
func MuteToggleCommand() *cobra.Command { return muteToggleCmd }

var (
	muteToggleRuntimeSession string
	muteToggleJSON           bool
)

var muteToggleCmd = &cobra.Command{
	Use:   "mute-toggle",
	Short: "Toggle mute on a tmux session",
	Long: `Toggles the mute state of a managed tmux session and updates its title.
--runtime-session names the target explicitly; otherwise AMX_RUNTIME_SESSION_ID
identifies the current session. A session that is not running is an error.

With --json it reports {window_id, runtime_session_id, muted, changed}.`,
	Example: `  amx mute-toggle
  amx mute-toggle --runtime-session rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f --json`,
	Args: cobra.NoArgs,
	RunE: runMuteToggle,
}

func init() {
	muteToggleCmd.Flags().StringVar(&muteToggleRuntimeSession, "runtime-session", "",
		"runtime session ID of the tmux session to mute")
	cli.AddJSONFlag(muteToggleCmd, &muteToggleJSON)
}

// muteToggleResult is the `mute-toggle --json` data. Muted is the state the
// window is left in; Changed is false when the command was a no-op because
// the window is not a managed session.
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
	if named == "" {
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
		return cli.MissingParameter(cmd, muteToggleJSON, "--runtime-session",
			"the runtime session ID of the tmux session to mute (default: $AMX_RUNTIME_SESSION_ID)")
	}
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
	if !result.Changed {
		// A window that is not a managed session: the documented silent no-op.
		return nil
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), muteToggleSummary(result))
	return err
}

// muteToggleSummary is the one line text mode prints for a mute that changed.
func muteToggleSummary(result muteToggleResult) string {
	target := result.RuntimeSessionID
	if result.Muted {
		return "muted " + target
	}
	return "unmuted " + target
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
