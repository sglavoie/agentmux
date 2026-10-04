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

func TestMuteToggleTextModePrintsNothing(t *testing.T) {
	client := &fakeMuteClient{windows: []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{testManagedTabWithBase(10, 101, "api")}}}}
	out, err := runMuteToggleJSON(t, client, 101, false)
	if err != nil || out != "" {
		t.Fatalf("text mode: out = %q, err = %v; want silence", out, err)
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
