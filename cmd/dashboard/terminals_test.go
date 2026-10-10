package dashboard

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

type stubTmux struct{ backend.TargetLauncher }

func (stubTmux) AttachCommand(pane backend.Ref) (*exec.Cmd, error) {
	return exec.Command("tmux", "attach-session", "-t", pane.ID), nil
}

type stubKitty struct{ kitty.KittyClient }

func TestSelectTerminals(t *testing.T) {
	unavailable := errors.New("tmux is not installed")
	cases := map[string]struct {
		requested     string
		hosted        bool
		serverPresent bool
		tmuxErr       error

		wantErr    bool
		wantKitty  bool
		wantTmux   bool
		wantLaunch runtimesession.BackendKind
	}{
		"inside Kitty stays Kitty-only":             {hosted: true, wantKitty: true, wantLaunch: runtimesession.BackendKitty},
		"inside Kitty lists a running tmux server":  {hosted: true, serverPresent: true, wantKitty: true, wantTmux: true, wantLaunch: runtimesession.BackendKitty},
		"inside Kitty with --backend tmux":          {requested: "tmux", hosted: true, wantKitty: true, wantTmux: true, wantLaunch: runtimesession.BackendTmux},
		"another terminal defaults to tmux":         {wantTmux: true, wantLaunch: runtimesession.BackendTmux},
		"another terminal without tmux is as ever":  {tmuxErr: unavailable, wantKitty: true, wantLaunch: runtimesession.BackendKitty},
		"another terminal asked for Kitty":          {requested: "kitty", wantKitty: true, wantLaunch: runtimesession.BackendKitty},
		"explicit tmux that cannot open is refused": {requested: "tmux", hosted: true, tmuxErr: unavailable, wantErr: true},
		"an unknown backend is refused":             {requested: "screen", hosted: true, wantErr: true},
		"a listing failure inside Kitty costs tmux": {hosted: true, serverPresent: true, tmuxErr: unavailable, wantKitty: true, wantLaunch: runtimesession.BackendKitty},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			previousHosted, previousOpen, previousPresent, previousPortable := insideKitty, openTmux, tmuxServerPresent, commands.Portable
			t.Cleanup(func() {
				insideKitty, openTmux, tmuxServerPresent, commands.Portable = previousHosted, previousOpen, previousPresent, previousPortable
			})
			insideKitty = func() bool { return tc.hosted }
			tmuxServerPresent = func() bool { return tc.serverPresent }
			openTmux = func() (backend.TargetLauncher, error) {
				if tc.tmuxErr != nil {
					return nil, tc.tmuxErr
				}
				return stubTmux{}, nil
			}

			client, err := selectTerminalsWith(tc.requested, func() kitty.KittyClient { return stubKitty{} })
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if (client != nil) != tc.wantKitty {
				t.Fatalf("Kitty client present = %v, want %v", client != nil, tc.wantKitty)
			}
			if (commands.Portable.Tmux != nil) != tc.wantTmux || commands.Portable.Launch != tc.wantLaunch {
				t.Fatalf("portable = tmux %v launch %q, want tmux %v launch %q",
					commands.Portable.Tmux != nil, commands.Portable.Launch, tc.wantTmux, tc.wantLaunch)
			}
			if (commands.Portable.Attach != nil) != tc.wantTmux {
				t.Fatalf("attach wired = %v, want %v", commands.Portable.Attach != nil, tc.wantTmux)
			}
		})
	}
}

// A registered session is attached through `amx attach`, which validates the
// target and records the attachment; an unregistered one directly.
func TestAttachCommandFor(t *testing.T) {
	previous := attachBinary
	attachBinary = func() (string, error) { return "/opt/amx", nil }
	t.Cleanup(func() { attachBinary = previous })
	attach := attachCommandFor(stubTmux{})

	registered, err := attach(session.RunningSession{RuntimeSessionID: "rs-0123456789abcdef0123456789abcdef", Pane: backend.PaneID("%3")})
	if err != nil || len(registered.Args) != 3 || registered.Args[0] != "/opt/amx" || registered.Args[1] != "attach" || registered.Args[2] != "rs-0123456789abcdef0123456789abcdef" {
		t.Fatalf("registered attach = %v, %v", registered, err)
	}
	direct, err := attach(session.RunningSession{Backend: runtimesession.BackendTmux, Pane: backend.PaneID("%3")})
	if err != nil || direct.Args[len(direct.Args)-1] != "%3" {
		t.Fatalf("direct attach = %v, %v", direct, err)
	}
	if attachCommandFor(nil) != nil {
		t.Fatal("a surface with no tmux backend must have no attach")
	}
}

// The pickers edit a Kitty tab's presentation. Outside Kitty they say that
// plainly rather than surfacing a failed Kitty call.
func TestPickersExplainThemselvesOutsideKitty(t *testing.T) {
	previous := insideKitty
	t.Cleanup(func() { insideKitty = previous })
	insideKitty = func() bool { return false }
	for _, command := range []*cobra.Command{ColorPickerCommand(), IconPickerCommand()} {
		err := command.RunE(command, nil)
		if err == nil || !strings.Contains(err.Error(), "this terminal is not Kitty") {
			t.Errorf("%s outside Kitty = %v, want it to say there is no Kitty tab", command.Name(), err)
		}
	}
	insideKitty = func() bool { return true }
	if err := requireKittyTab("color-picker", "color"); err != nil {
		t.Fatalf("inside Kitty the picker was refused: %v", err)
	}
}
