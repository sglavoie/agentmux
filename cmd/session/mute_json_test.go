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
)

// runMuteToggleJSON runs the real RunE against client with --json set, the
// way the companion app invokes it.
func runMuteToggleJSON(t *testing.T, client *fakeMuteClient, windowID int, jsonOut bool) (string, error) {
	t.Helper()
	oldClient, oldID, oldJSON, oldOut := muteToggleClient, muteToggleWindowID, muteToggleJSON, muteToggleCmd.OutOrStdout()
	t.Cleanup(func() {
		muteToggleClient, muteToggleWindowID, muteToggleJSON = oldClient, oldID, oldJSON
		muteToggleCmd.SetOut(oldOut)
	})
	muteToggleClient = func() kitty.KittyClient { return client }
	muteToggleWindowID = windowID
	muteToggleJSON = jsonOut
	var out bytes.Buffer
	muteToggleCmd.SetOut(&out)
	err := muteToggleCmd.RunE(muteToggleCmd, nil)
	return out.String(), err
}

func mutedTitle() string {
	return palette.BuildTitleParts(palette.TitleParts{Base: "🦊api", State: "bolt", Muted: true})
}

func TestMuteToggleJSONContract(t *testing.T) {
	cases := map[string]struct {
		client   *fakeMuteClient
		windowID int
		want     string
	}{
		"mute": {
			&fakeMuteClient{windows: []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{testManagedTabWithBase(10, 101, "api")}}}},
			101, `{"schema_version":1,"data":{"window_id":101,"muted":true,"changed":true}}`,
		},
		"unmute": {
			&fakeMuteClient{windows: []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{testMutedManagedTab(10, 101, mutedTitle())}}}},
			101, `{"schema_version":1,"data":{"window_id":101,"muted":false,"changed":true}}`,
		},
		"unmanaged": {
			&fakeMuteClient{windows: []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{testUnmanagedTab(20, 201, "shell")}}}},
			201, `{"schema_version":1,"data":{"window_id":201,"muted":false,"changed":false}}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := runMuteToggleJSON(t, tc.client, tc.windowID, true)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(out); got != tc.want {
				t.Fatalf("json = %s\nwant   %s", got, tc.want)
			}
		})
	}
}

// Text mode says what changed in one line, and stays silent for the no-op of
// a window that is not a managed session.
func TestMuteToggleTextModeSaysWhatChanged(t *testing.T) {
	client := &fakeMuteClient{windows: []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{testManagedTabWithBase(10, 101, "api")}}}}
	out, err := runMuteToggleJSON(t, client, 101, false)
	if err != nil || out != "muted Kitty window 101\n" {
		t.Fatalf("text mode: out = %q, err = %v", out, err)
	}
	unmanaged := &fakeMuteClient{windows: []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{testUnmanagedTab(20, 201, "shell")}}}}
	if out, err := runMuteToggleJSON(t, unmanaged, 201, false); err != nil || out != "" {
		t.Fatalf("unmanaged: out = %q, err = %v; want silence", out, err)
	}
	if got := muteToggleSummary(muteToggleResult{RuntimeSessionID: "rs-1", Changed: true}); got != "unmuted rs-1" {
		t.Fatalf("runtime summary = %q", got)
	}
}

// With no window and no runtime session there is nothing to mute: that is a
// missing parameter, not a silent success.
func TestMuteToggleWithNoTargetIsAMissingParameter(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "")
	t.Setenv(runtimesession.EnvVar, "")
	client := &fakeMuteClient{}
	out, err := runMuteToggleJSON(t, client, 0, true)
	var exit *cli.ExitError
	if !errors.As(err, &exit) || !exit.Quiet {
		t.Fatalf("err = %v; want a quiet nonzero exit", err)
	}
	var envelope cli.JSONErrorEnvelope
	if decodeErr := json.Unmarshal([]byte(out), &envelope); decodeErr != nil || envelope.Error.Code != "missing_parameter" {
		t.Fatalf("output = %q (%v)", out, decodeErr)
	}
	if out, err := runMuteToggleJSON(t, client, 0, false); err == nil || out != "" || !strings.Contains(err.Error(), "--window-id is required") {
		t.Fatalf("text: out = %q, err = %v", out, err)
	}
}

func TestMuteToggleJSONFailures(t *testing.T) {
	// A Kitty listing failure changed nothing: the error envelope.
	out, err := runMuteToggleJSON(t, &fakeMuteClient{listErr: errors.New("kitty unreachable")}, 101, true)
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code == 0 || !exit.Quiet {
		t.Fatalf("list failure err = %v; want a quiet nonzero exit", err)
	}
	var envelope cli.JSONErrorEnvelope
	if decodeErr := json.Unmarshal([]byte(out), &envelope); decodeErr != nil || envelope.Error.Code != "mute_toggle_failed" {
		t.Fatalf("list failure output = %q (%v)", out, decodeErr)
	}

	// The mute landed but the title did not: the data envelope reports the
	// durable change, with the same nonzero quiet exit.
	client := &fakeMuteClient{
		windows:  []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{testManagedTabWithBase(10, 101, "api")}}},
		titleErr: errors.New("title write failed"),
	}
	out, err = runMuteToggleJSON(t, client, 101, true)
	if !errors.As(err, &exit) || exit.Code == 0 || !exit.Quiet {
		t.Fatalf("title failure err = %v; want a quiet nonzero exit", err)
	}
	if got := strings.TrimSpace(out); got != `{"schema_version":1,"data":{"window_id":101,"muted":true,"changed":true}}` {
		t.Fatalf("title failure json = %s", got)
	}

	// Text mode reports the same failures as a bare error.
	if out, err := runMuteToggleJSON(t, client, 101, false); err == nil || out != "" {
		t.Fatalf("text title failure: out = %q, err = %v", out, err)
	}
}

// TestMuteToggleNamesATmuxSessionByRuntimeSession: the app's workspace mutes
// the session it shows by runtime session, with no Kitty to ask.
func TestMuteToggleNamesATmuxSessionByRuntimeSession(t *testing.T) {
	f, launched := attachLaunchAgent(t)
	id := string(launched.RuntimeSessionID)
	run := func(named string) (string, error) {
		t.Helper()
		oldClient, oldNamed, oldJSON, oldOut := muteToggleClient, muteToggleRuntimeSession, muteToggleJSON, muteToggleCmd.OutOrStdout()
		defer func() {
			muteToggleClient, muteToggleRuntimeSession, muteToggleJSON = oldClient, oldNamed, oldJSON
			muteToggleCmd.SetOut(oldOut)
		}()
		muteToggleClient = func() kitty.KittyClient {
			t.Fatal("a runtime session was named, yet Kitty was asked")
			return nil
		}
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
