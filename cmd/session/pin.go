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

// PinSessionCommand returns the root-level pin-session leaf so the root
// command can register it.
func PinSessionCommand() *cobra.Command { return pinSessionCmd }

var (
	pinSessionWindowID       int
	pinSessionRuntimeSession string
	pinSessionUnpin          bool
	pinSessionJSON           bool
	pinSessionClient         = cli.NewKittyClient
)

var pinSessionCmd = &cobra.Command{
	Use:   "pin-session",
	Short: "Pin an Agent Mux session above every other, or unpin it",
	Long: `Pins the session in the Kitty window identified by --window-id (or
$KITTY_WINDOW_ID when the flag is zero), or with --unpin unpins it. The pin
is the ★ in front of the session's title, so the dashboard, the workspace
sidebar, and the session order all read the same pins: pinned sessions are
listed above every other, Needs you included. It sets the pin rather than
flipping it, so asking twice changes nothing the second time. If the window
is not a managed Agent Mux session, the command is a silent no-op.

With --runtime-session the session is named by its runtime session instead,
which is how one on the tmux backend is pinned: the star is on its group's
title, and no Kitty is consulted. A runtime session that is not running
there is an error rather than a no-op, because it was named exactly. With
neither flag and outside Kitty, a command that runs in a managed session
(AMX_RUNTIME_SESSION_ID) pins that session the same way.

With --json it reports {window_id, pinned, changed}: pinned is the session's
pin after the command, and changed is false when it already had it or the
window is not a managed session. A session named by --runtime-session reports
runtime_session_id as well, and a window_id of zero.`,
	Example: `  amx pin-session
  amx pin-session --unpin
  amx pin-session --runtime-session rs-4f1c0b9e2a7d4c3b8e6f1a2b3c4d5e6f --json`,
	Args: cobra.NoArgs,
	RunE: runPinSession,
}

func init() {
	pinSessionCmd.Flags().IntVar(&pinSessionWindowID, "window-id", 0,
		"Kitty window ID of the target tab (defaults to $KITTY_WINDOW_ID)")
	pinSessionCmd.Flags().StringVar(&pinSessionRuntimeSession, "runtime-session", "",
		"runtime session ID of a tmux session to pin instead of a Kitty tab")
	pinSessionCmd.Flags().BoolVar(&pinSessionUnpin, "unpin", false, "unpin the session instead of pinning it")
	cli.AddJSONFlag(pinSessionCmd, &pinSessionJSON)
}

// pinSessionResult is the `pin-session --json` data. Pinned is the pin the
// session is left with; Changed is false when it already had it, or when the
// window is not a managed session.
type pinSessionResult struct {
	WindowID         int    `json:"window_id"`
	RuntimeSessionID string `json:"runtime_session_id,omitempty"`
	Pinned           bool   `json:"pinned"`
	Changed          bool   `json:"changed"`
	// managed is false for a Kitty window that is not a managed session.
	managed bool
}

// pinWriter writes the title that carries the pin.
type pinWriter interface {
	SetTitle(ref backend.Ref, title string) error
}

// pinTerminal is what pinning a tmux session needs from its backend.
type pinTerminal interface {
	backend.Discoverer
	pinWriter
}

func runPinSession(cmd *cobra.Command, _ []string) error {
	want := !pinSessionUnpin
	var result pinSessionResult
	var err error
	named := strings.TrimSpace(pinSessionRuntimeSession)
	if named == "" && pinSessionWindowID == 0 {
		// With nothing named and no Kitty window around it, the session
		// this command runs in is the one meant, as with mute-toggle.
		named = currentRuntimeSession()
	}
	if named != "" {
		id, parseErr := cli.ParseRuntimeSessionID(named)
		if parseErr != nil {
			return cli.CommandFailure(cmd, pinSessionJSON, "invalid_arguments", parseErr)
		}
		opened, openErr := attachNewTmux()
		if openErr != nil {
			return cli.CommandFailure(cmd, pinSessionJSON, "backend_unavailable", fmt.Errorf("tmux backend unavailable: %w", openErr))
		}
		term, ok := opened.(pinTerminal)
		if !ok {
			return cli.CommandFailure(cmd, pinSessionJSON, "backend_unavailable", errors.New("tmux backend cannot pin sessions"))
		}
		result, err = pinRuntimeSession(term, id, want)
		if errors.Is(err, errNoRuntimeSession) {
			return cli.CommandFailure(cmd, pinSessionJSON, "not_found", err)
		}
	} else {
		windowID := cli.WindowIDOrEnv(pinSessionWindowID)
		if windowID == 0 {
			return cli.MissingParameter(cmd, pinSessionJSON, "--window-id",
				"the Kitty window of the session to pin (default: $KITTY_WINDOW_ID), or --runtime-session for a tmux session")
		}
		result, err = pinKittySession(pinSessionClient(), windowID, want)
	}
	if err != nil {
		return cli.CommandFailure(cmd, pinSessionJSON, "pin_session_failed", fmt.Errorf("pin session: %w", cli.PlainKittyError(err)))
	}
	if pinSessionJSON {
		return cli.WriteJSON(cmd, result)
	}
	if !result.managed {
		// A window that is not a managed session: the documented silent no-op.
		return nil
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), pinSessionSummary(result))
	return err
}

// pinSessionSummary is the one line text mode prints.
func pinSessionSummary(result pinSessionResult) string {
	target := fmt.Sprintf("Kitty window %d", result.WindowID)
	if result.RuntimeSessionID != "" {
		target = result.RuntimeSessionID
	}
	switch {
	case result.Pinned && result.Changed:
		return "pinned " + target
	case result.Pinned:
		return target + " is already pinned"
	case result.Changed:
		return "unpinned " + target
	default:
		return target + " is not pinned"
	}
}

func pinKittySession(client kitty.KittyClient, windowID int, want bool) (pinSessionResult, error) {
	result := pinSessionResult{WindowID: windowID}
	sessions, err := session.List(client)
	if err != nil {
		return result, err
	}
	rs, ok := session.FindByWindowID(sessions, windowID)
	if !ok {
		return result, nil
	}
	return applyPin(kitty.NewBackend(client), rs, want, result)
}

// pinRuntimeSession pins or unpins the tmux session a runtime session ID
// names. Only a pane that still carries that runtime session is touched.
func pinRuntimeSession(term pinTerminal, id runtimesession.ID, want bool) (pinSessionResult, error) {
	result := pinSessionResult{RuntimeSessionID: string(id)}
	panes, err := runtimeSessionPanes(term, id)
	if err != nil {
		return result, err
	}
	for _, rs := range session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, nil) {
		if rs.RuntimeSessionID == id {
			return applyPin(term, rs, want, result)
		}
	}
	return result, noRuntimeSession(id)
}

// applyPin leaves rs pinned or not on term and fills in result. A session
// that already has the pin asked for is not written to.
func applyPin(term pinWriter, rs session.RunningSession, want bool, result pinSessionResult) (pinSessionResult, error) {
	result.managed, result.Pinned = true, rs.Starred
	if rs.Starred == want {
		return result, nil
	}
	parts := rs.TitleParts()
	parts.Starred = want
	if err := term.SetTitle(rs.PaneRef(), palette.BuildTitleParts(parts)); err != nil {
		return result, err
	}
	result.Pinned, result.Changed = want, true
	return result, nil
}
