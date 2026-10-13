package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
)

const exportRuntimeID = "rs-0123456789abcdef0123456789abcdef"

// runtimeExportClient is a daemon that holds one tmux session in a slot only
// it can name.
type runtimeExportClient struct {
	fakeDaemonExportClient
	slot    int
	runtime []string
}

func (f *runtimeExportClient) ExportRuntimeSessionEvents(id string) (protocol.EventsExport, error) {
	f.runtime = append(f.runtime, id)
	if id != exportRuntimeID {
		return protocol.EventsExport{}, fmt.Errorf("daemon error: %s", protocol.OriginReasonRuntimeSessionUnknown)
	}
	return protocol.EventsExport{
		EventsByWindow: map[int][]protocol.SessionEvent{
			f.slot: {{At: 7, Event: "Stop", Action: "stop", ResultIconState: "stop"}},
		},
		RuntimeSlots: map[int]protocol.RuntimeSlot{f.slot: {RuntimeSessionID: id, Backend: "tmux"}},
	}, nil
}

func runRuntimeExport(t *testing.T, client *runtimeExportClient) (protocol.ExportEventsPayload, error) {
	t.Helper()
	newDaemonExportClient = func() cli.DaemonExportClient { return client }
	// The slot is the daemon's alone and no Kitty window: Kitty is not asked
	// about it, and the session's pane on tmux is what says how it looks now.
	newDaemonExportKitty = func() cli.KittyWindowLister {
		t.Error("an export of a tmux session asked Kitty for its windows")
		return fakeKittyWindowLister{}
	}
	newDaemonExportRuntime = func() ([]session.RunningSession, error) {
		return []session.RunningSession{
			{RuntimeSessionID: "rs-ffffffffffffffffffffffffffffffff", Backend: runtimesession.BackendTmux, Project: "other"},
			{RuntimeSessionID: exportRuntimeID, Backend: runtimesession.BackendTmux, Title: "W:api", Base: "W:api", Project: "api", Profile: "work", Harness: "claude", Directory: "/work/api"},
		}, nil
	}
	daemonExportNow = func() time.Time { return time.Unix(700, 0) }
	daemonExportEventsWindow, daemonExportEventsLast = 0, 0
	daemonExportEventsOutput, daemonExportEventsPretty = "", false

	var out bytes.Buffer
	daemonExportEventsCmd.SetOut(&out)
	if err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil); err != nil {
		return protocol.ExportEventsPayload{}, err
	}
	var payload protocol.ExportEventsPayload
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal payload: %v\n%s", err, out.String())
	}
	return payload, nil
}

func newRuntimeExportClient() *runtimeExportClient {
	return &runtimeExportClient{
		fakeDaemonExportClient: fakeDaemonExportClient{health: ipc.Health{Alive: true, PID: 7, Uptime: 7 * time.Second}},
		slot:                   1<<40 + 3,
	}
}

func requireRuntimeWindow(t *testing.T, payload protocol.ExportEventsPayload, client *runtimeExportClient) {
	t.Helper()
	if len(client.runtime) != 1 || client.runtime[0] != exportRuntimeID || len(client.calls) != 0 {
		t.Fatalf("daemon asked by runtime %v and by window %v; want one request for %s", client.runtime, client.calls, exportRuntimeID)
	}
	if len(payload.Windows) != 1 {
		t.Fatalf("payload windows = %+v; want one", payload.Windows)
	}
	window := payload.Windows[0]
	if window.WindowID != client.slot || window.RuntimeSessionID != exportRuntimeID || window.Backend != "tmux" {
		t.Fatalf("window = id %d runtime %q backend %q; want slot %d of %s on tmux", window.WindowID, window.RuntimeSessionID, window.Backend, client.slot, exportRuntimeID)
	}
	if len(window.Events) != 1 || window.Events[0].Event != "Stop" {
		t.Fatalf("window events = %+v; want the session's one event", window.Events)
	}
	want := protocol.WindowLiveMetadata{Found: true, TabTitle: "W:api", BaseTitle: "W:api", Project: "api", Profile: "work", Harness: "claude", Directory: "/work/api"}
	if window.Live != want {
		t.Fatalf("live = %+v; want the session's own pane, joined by runtime session with no tab or OS window: %+v", window.Live, want)
	}
}

// In a tmux session --current-window is the session itself. The Kitty window
// the pane inherited from the terminal that launched it is not believed.
func TestDaemonExportEventsCommand_CurrentWindowResolvesRuntimeSession(t *testing.T) {
	resetDaemonExportTestState(t)
	t.Setenv(runtimesession.EnvVar, exportRuntimeID)
	t.Setenv("KITTY_WINDOW_ID", "77")
	daemonExportEventsCurrentWindow = true

	client := newRuntimeExportClient()
	payload, err := runRuntimeExport(t, client)
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}
	requireRuntimeWindow(t, payload, client)
}

func TestDaemonExportEventsCommand_RuntimeSessionFlag(t *testing.T) {
	resetDaemonExportTestState(t)
	t.Setenv("KITTY_WINDOW_ID", "")
	daemonExportEventsCurrentWindow = false
	daemonExportEventsRuntime = exportRuntimeID

	client := newRuntimeExportClient()
	payload, err := runRuntimeExport(t, client)
	if err != nil {
		t.Fatalf("RunE: %v", err)
	}
	requireRuntimeWindow(t, payload, client)
}

func TestDaemonExportEventsCommand_RuntimeSessionRefusals(t *testing.T) {
	for _, tc := range []struct {
		name          string
		env, flag     string
		current       bool
		window        int
		want          string
		wantDaemonAsk bool
	}{
		{name: "malformed environment", env: "rs-nope", current: true, want: "requires a valid runtime session ID"},
		{name: "malformed flag", flag: "7", want: "--runtime-session requires a valid runtime session ID"},
		{name: "flag with current window", flag: exportRuntimeID, current: true, want: "--runtime-session cannot be used with"},
		{name: "flag with window", flag: exportRuntimeID, window: 4, want: "--runtime-session cannot be used with"},
		{name: "unknown to the daemon", flag: "rs-ffffffffffffffffffffffffffffffff", want: protocol.OriginReasonRuntimeSessionUnknown, wantDaemonAsk: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetDaemonExportTestState(t)
			t.Setenv(runtimesession.EnvVar, tc.env)
			t.Setenv("KITTY_WINDOW_ID", "")
			daemonExportEventsCurrentWindow = tc.current
			daemonExportEventsRuntime = tc.flag

			client := newRuntimeExportClient()
			newDaemonExportClient = func() cli.DaemonExportClient { return client }
			daemonExportEventsWindow = tc.window
			var out bytes.Buffer
			daemonExportEventsCmd.SetOut(&out)
			err := daemonExportEventsCmd.RunE(daemonExportEventsCmd, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RunE error = %v; want one containing %q", err, tc.want)
			}
			if out.Len() != 0 {
				t.Fatalf("a refused export wrote %q", out.String())
			}
			if asked := len(client.runtime)+len(client.calls) > 0; asked != tc.wantDaemonAsk {
				t.Fatalf("daemon asked = %v (runtime %v, window %v); want %v", asked, client.runtime, client.calls, tc.wantDaemonAsk)
			}
		})
	}
}
