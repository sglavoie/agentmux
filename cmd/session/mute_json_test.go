package session

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

func TestMuteToggleRequiresRuntimeSession(t *testing.T) {
	t.Setenv(runtimesession.EnvVar, "")
	oldNamed, oldJSON, oldOut := muteToggleRuntimeSession, muteToggleJSON, muteToggleCmd.OutOrStdout()
	t.Cleanup(func() {
		muteToggleRuntimeSession, muteToggleJSON = oldNamed, oldJSON
		muteToggleCmd.SetOut(oldOut)
	})
	muteToggleRuntimeSession = ""
	muteToggleJSON = true
	var out bytes.Buffer
	muteToggleCmd.SetOut(&out)
	err := muteToggleCmd.RunE(muteToggleCmd, nil)
	var exit *cli.ExitError
	if !errors.As(err, &exit) || !exit.Quiet || !strings.Contains(out.String(), `"code":"missing_parameter"`) || !strings.Contains(out.String(), `"name":"--runtime-session"`) {
		t.Fatalf("out = %q, err = %v; want a missing runtime session", out.String(), err)
	}
}

// TestMuteToggleNamesATmuxSessionByRuntimeSession: the app's workspace mutes
// the session it shows by runtime session, with no terminal to ask.
func TestMuteToggleNamesATmuxSessionByRuntimeSession(t *testing.T) {
	f, launched := attachLaunchAgent(t)
	id := string(launched.RuntimeSessionID)
	run := func(named string) (string, error) {
		t.Helper()
		oldNamed, oldJSON, oldOut := muteToggleRuntimeSession, muteToggleJSON, muteToggleCmd.OutOrStdout()
		defer func() {
			muteToggleRuntimeSession, muteToggleJSON = oldNamed, oldJSON
			muteToggleCmd.SetOut(oldOut)
		}()
		muteToggleRuntimeSession, muteToggleJSON = named, true
		var out bytes.Buffer
		muteToggleCmd.SetOut(&out)
		err := muteToggleCmd.RunE(muteToggleCmd, nil)
		return strings.TrimSpace(out.String()), err
	}
	muted := func() string {
		t.Helper()
		panes, err := f.Backend.Discover()
		if err != nil {
			t.Fatal(err)
		}
		for _, pane := range panes {
			if pane.Ref == launched.Pane {
				return pane.Vars["cc_mute"]
			}
		}
		t.Fatal("the launched pane is not listed")
		return ""
	}

	out, err := run(id)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"schema_version":1,"data":{"window_id":0,"runtime_session_id":"` + id + `","muted":true,"changed":true}}`; out != want {
		t.Fatalf("json = %s\nwant   %s", out, want)
	}
	if muted() == "" {
		t.Fatal("the pane carries no mute after the toggle")
	}
	if out, err = run(id); err != nil || !strings.Contains(out, `"muted":false,"changed":true`) {
		t.Fatalf("second toggle: out = %s, err = %v", out, err)
	}
	if got := muted(); got != "" {
		t.Fatalf("the pane still carries mute %q after the second toggle", got)
	}

	// A session named exactly and not running is an error, not a no-op.
	unknown := "rs-" + strings.Repeat("0", 32)
	out, err = run(unknown)
	var exit *cli.ExitError
	if !errors.As(err, &exit) || !strings.Contains(out, `"code":"not_found"`) {
		t.Fatalf("unknown runtime session: out = %s, err = %v", out, err)
	}
	if !strings.Contains(out, "no running tmux session with runtime session "+unknown) {
		t.Fatalf("unknown runtime session message: %s", out)
	}
	if out, err = run("nonsense"); err == nil || !strings.Contains(out, `"code":"invalid_arguments"`) {
		t.Fatalf("malformed runtime session: out = %s, err = %v", out, err)
	}
}
