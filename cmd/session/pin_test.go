package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

// runPinSessionCmd runs the real RunE against client, the way the companion
// app invokes it.
func runPinSessionCmd(t *testing.T, client *fakeMuteClient, windowID int, unpin, jsonOut bool) (string, error) {
	t.Helper()
	oldClient, oldID, oldNamed, oldUnpin, oldJSON, oldOut := pinSessionClient, pinSessionWindowID, pinSessionRuntimeSession, pinSessionUnpin, pinSessionJSON, pinSessionCmd.OutOrStdout()
	t.Cleanup(func() {
		pinSessionClient, pinSessionWindowID, pinSessionRuntimeSession, pinSessionUnpin, pinSessionJSON = oldClient, oldID, oldNamed, oldUnpin, oldJSON
		pinSessionCmd.SetOut(oldOut)
	})
	pinSessionClient = func() kitty.KittyClient { return client }
	pinSessionWindowID, pinSessionRuntimeSession, pinSessionUnpin, pinSessionJSON = windowID, "", unpin, jsonOut
	var out bytes.Buffer
	pinSessionCmd.SetOut(&out)
	err := pinSessionCmd.RunE(pinSessionCmd, nil)
	return out.String(), err
}

func pinnedTitle() string {
	return palette.BuildTitleParts(palette.TitleParts{Base: "🦊api", State: "bolt", Starred: true, Muted: true})
}

func TestPinSessionJSONContract(t *testing.T) {
	managed := func(title string) *fakeMuteClient {
		return &fakeMuteClient{windows: []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{testManagedTabWithBase(10, 101, title)}}}}
	}
	cases := map[string]struct {
		client *fakeMuteClient
		unpin  bool
		want   string
		title  string
	}{
		"pin":            {managed("🦊api"), false, `{"schema_version":1,"data":{"window_id":101,"pinned":true,"changed":true}}`, "★🦊api"},
		"already pinned": {managed(pinnedTitle()), false, `{"schema_version":1,"data":{"window_id":101,"pinned":true,"changed":false}}`, ""},
		"unpin":          {managed(pinnedTitle()), true, `{"schema_version":1,"data":{"window_id":101,"pinned":false,"changed":true}}`, "🔇⚡🦊api"},
		"unmanaged": {
			&fakeMuteClient{windows: []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{testUnmanagedTab(20, 201, "shell")}}}}, false,
			`{"schema_version":1,"data":{"window_id":201,"pinned":false,"changed":false}}`, "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			windowID := 101
			if name == "unmanaged" {
				windowID = 201
			}
			out, err := runPinSessionCmd(t, tc.client, windowID, tc.unpin, true)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(out); got != tc.want {
				t.Fatalf("json = %s\nwant   %s", got, tc.want)
			}
			if tc.title == "" {
				if len(tc.client.setTabTitleCalls) != 0 || len(tc.client.setUserVarsCalls) != 0 {
					t.Fatalf("a no-op wrote: %+v %+v", tc.client.setTabTitleCalls, tc.client.setUserVarsCalls)
				}
				return
			}
			if calls := tc.client.setTabTitleCalls; len(calls) != 1 || calls[0].title != tc.title {
				t.Fatalf("title writes = %+v, want %q", calls, tc.title)
			}
			if len(tc.client.setUserVarsCalls) != 0 {
				t.Fatalf("a pin wrote user vars: %+v", tc.client.setUserVarsCalls)
			}
		})
	}
}

func TestPinSessionTextModeSaysWhatChanged(t *testing.T) {
	client := &fakeMuteClient{windows: []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{testManagedTabWithBase(10, 101, "api")}}}}
	if out, err := runPinSessionCmd(t, client, 101, false, false); err != nil || out != "pinned Kitty window 101\n" {
		t.Fatalf("text mode: out = %q, err = %v", out, err)
	}
	unmanaged := &fakeMuteClient{windows: []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{testUnmanagedTab(20, 201, "shell")}}}}
	for _, unpin := range []bool{false, true} {
		if out, err := runPinSessionCmd(t, unmanaged, 201, unpin, false); err != nil || out != "" {
			t.Fatalf("unmanaged (unpin=%v): out = %q, err = %v; want silence", unpin, out, err)
		}
	}
	for result, want := range map[pinSessionResult]string{
		{RuntimeSessionID: "rs-1", Changed: true}:               "unpinned rs-1",
		{RuntimeSessionID: "rs-1", Pinned: true}:                "rs-1 is already pinned",
		{WindowID: 7}:                                           "Kitty window 7 is not pinned",
		{RuntimeSessionID: "rs-1", Pinned: true, Changed: true}: "pinned rs-1",
	} {
		if got := pinSessionSummary(result); got != want {
			t.Errorf("summary(%+v) = %q, want %q", result, got, want)
		}
	}
}

func TestPinSessionWithNoTargetIsAMissingParameter(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv(runtimesession.EnvVar, "")
	out, err := runPinSessionCmd(t, &fakeMuteClient{}, 0, false, true)
	var exit *cli.ExitError
	if !errors.As(err, &exit) || !exit.Quiet {
		t.Fatalf("err = %v; want a quiet nonzero exit", err)
	}
	var envelope cli.JSONErrorEnvelope
	if decodeErr := json.Unmarshal([]byte(out), &envelope); decodeErr != nil || envelope.Error.Code != "missing_parameter" {
		t.Fatalf("output = %q (%v)", out, decodeErr)
	}
}

func TestPinSessionFailureIsTheErrorEnvelope(t *testing.T) {
	out, err := runPinSessionCmd(t, &fakeMuteClient{listErr: errors.New("kitty unreachable")}, 101, false, true)
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code == 0 || !exit.Quiet {
		t.Fatalf("err = %v; want a quiet nonzero exit", err)
	}
	var envelope cli.JSONErrorEnvelope
	if decodeErr := json.Unmarshal([]byte(out), &envelope); decodeErr != nil || envelope.Error.Code != "pin_session_failed" {
		t.Fatalf("output = %q (%v)", out, decodeErr)
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
		oldClient, oldID, oldNamed, oldUnpin, oldJSON, oldOut := pinSessionClient, pinSessionWindowID, pinSessionRuntimeSession, pinSessionUnpin, pinSessionJSON, pinSessionCmd.OutOrStdout()
		defer func() {
			pinSessionClient, pinSessionWindowID, pinSessionRuntimeSession, pinSessionUnpin, pinSessionJSON = oldClient, oldID, oldNamed, oldUnpin, oldJSON
			pinSessionCmd.SetOut(oldOut)
		}()
		pinSessionClient = func() kitty.KittyClient {
			t.Fatal("a runtime session was named, yet Kitty was asked")
			return nil
		}
		pinSessionWindowID, pinSessionRuntimeSession, pinSessionUnpin, pinSessionJSON = 0, named, unpin, true
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
