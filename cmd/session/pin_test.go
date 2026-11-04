package session

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestPinSessionRequiresRuntimeSession(t *testing.T) {
	t.Setenv(runtimesession.EnvVar, "")
	oldNamed, oldJSON, oldOut := pinSessionRuntimeSession, pinSessionJSON, pinSessionCmd.OutOrStdout()
	t.Cleanup(func() {
		pinSessionRuntimeSession, pinSessionJSON = oldNamed, oldJSON
		pinSessionCmd.SetOut(oldOut)
	})
	pinSessionRuntimeSession = ""
	pinSessionJSON = true
	var out bytes.Buffer
	pinSessionCmd.SetOut(&out)
	err := pinSessionCmd.RunE(pinSessionCmd, nil)
	var exit *cli.ExitError
	if !errors.As(err, &exit) || !exit.Quiet || !strings.Contains(out.String(), `"code":"missing_parameter"`) || !strings.Contains(out.String(), `"name":"--runtime-session"`) {
		t.Fatalf("out = %q, err = %v; want a missing runtime session", out.String(), err)
	}
}

// TestPinSessionNamesATmuxSessionByRuntimeSession: the workspace pins the
// session it shows by runtime session, and the star lands on its group title,
// where the dashboard and the daemon read it.
func TestPinSessionNamesATmuxSessionByRuntimeSession(t *testing.T) {
	f, launched := attachLaunchAgent(t)
	id := string(launched.RuntimeSessionID)
	run := func(named string, unpin bool) (string, error) {
		t.Helper()
		oldNamed, oldUnpin, oldJSON, oldOut := pinSessionRuntimeSession, pinSessionUnpin, pinSessionJSON, pinSessionCmd.OutOrStdout()
		defer func() {
			pinSessionRuntimeSession, pinSessionUnpin, pinSessionJSON = oldNamed, oldUnpin, oldJSON
			pinSessionCmd.SetOut(oldOut)
		}()
		pinSessionRuntimeSession, pinSessionUnpin, pinSessionJSON = named, unpin, true
		var out bytes.Buffer
		pinSessionCmd.SetOut(&out)
		err := pinSessionCmd.RunE(pinSessionCmd, nil)
		return strings.TrimSpace(out.String()), err
	}
	pinned := func() bool {
		t.Helper()
		panes, err := f.Backend.Discover()
		if err != nil {
			t.Fatal(err)
		}
		for _, rs := range session.RunningSessionsFromPanes(runtimesession.BackendTmux, panes, nil) {
			if rs.RuntimeSessionID == launched.RuntimeSessionID {
				return rs.Starred
			}
		}
		t.Fatal("the launched session is not listed")
		return false
	}

	out, err := run(id, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"schema_version":1,"data":{"window_id":0,"runtime_session_id":"` + id + `","pinned":true,"changed":true}}`; out != want {
		t.Fatalf("json = %s\nwant   %s", out, want)
	}
	if !pinned() {
		t.Fatal("the session is not pinned after pin-session")
	}
	if out, err = run(id, false); err != nil || !strings.Contains(out, `"pinned":true,"changed":false`) {
		t.Fatalf("second pin: out = %s, err = %v", out, err)
	}
	if out, err = run(id, true); err != nil || !strings.Contains(out, `"pinned":false,"changed":true`) {
		t.Fatalf("unpin: out = %s, err = %v", out, err)
	}
	if pinned() {
		t.Fatal("the session is still pinned after --unpin")
	}

	unknown := "rs-" + strings.Repeat("0", 32)
	out, err = run(unknown, false)
	var exit *cli.ExitError
	if !errors.As(err, &exit) || !strings.Contains(out, `"code":"not_found"`) {
		t.Fatalf("unknown runtime session: out = %s, err = %v", out, err)
	}
}
