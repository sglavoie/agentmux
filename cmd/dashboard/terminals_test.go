package dashboard

import (
	"errors"
	"os/exec"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/tui/commands"
)

type stubTmux struct{ backend.TargetLauncher }

func (stubTmux) AttachCommand(pane backend.Ref, _ tmux.AttachView) (*exec.Cmd, error) {
	return exec.Command("tmux", "attach-session", "-t", pane.ID), nil
}

func TestSelectTerminals(t *testing.T) {
	unavailable := errors.New("tmux is not installed")
	cases := map[string]struct {
		requested string
		tmuxErr   error

		wantErr    bool
		wantTmux   bool
		wantLaunch runtimesession.BackendKind
	}{
		"defaults to tmux":           {wantTmux: true, wantLaunch: runtimesession.BackendTmux},
		"explicit tmux":              {requested: "tmux", wantTmux: true, wantLaunch: runtimesession.BackendTmux},
		"missing tmux is refused":    {tmuxErr: unavailable, wantErr: true},
		"unsupported backend":        {requested: "unsupported", wantErr: true},
		"explicit tmux unavailable":  {requested: "tmux", tmuxErr: unavailable, wantErr: true},
		"unknown backend is refused": {requested: "screen", wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			previousOpen, previousPortable := openTmux, commands.Portable
			t.Cleanup(func() {
				openTmux, commands.Portable = previousOpen, previousPortable
			})
			openTmux = func() (backend.TargetLauncher, error) {
				if tc.tmuxErr != nil {
					return nil, tc.tmuxErr
				}
				return stubTmux{}, nil
			}

			err := selectTerminalsWith(tc.requested)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, want error %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
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
