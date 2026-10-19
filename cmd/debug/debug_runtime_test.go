package debug

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/debugger"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

const debugRuntimeID = "rs-0123456789abcdef0123456789abcdef"

// recordingProbe answers a probe the way the daemon does and keeps what it
// was asked.
type recordingProbe struct{ requests []protocol.Request }

func (p *recordingProbe) SendWithAutoStartObserved(request protocol.Request, _ string, _ ipc.DeliveryObserver) (protocol.Response, error) {
	p.requests = append(p.requests, request)
	return protocol.Response{OK: true, Delivery: &protocol.DeliveryAcknowledgement{
		Status: protocol.DeliveryAcknowledgementAccepted, RuntimeSessionID: request.RuntimeSessionID,
	}}, nil
}

// In a tmux session `debug probe` probes the session it is typed in, by its
// runtime session: the Kitty window the pane inherited is not believed, and
// no window ID is sent. --runtime-session names another, --window still names
// a Kitty window, and the two cannot be combined.
func TestDebugProbeTargetsARuntimeSession(t *testing.T) {
	for _, tc := range []struct {
		name           string
		env, flag      string
		kittyWindow    string
		kittySocket    string
		explicitSocket string
		window         int
		wantRuntime    string
		wantWindow     int
		wantErr        string
		wantNoRequest  bool
	}{
		{name: "the current tmux session", env: debugRuntimeID, kittyWindow: "77", wantRuntime: debugRuntimeID},
		{name: "a named session", env: debugRuntimeID, flag: "rs-ffffffffffffffffffffffffffffffff", wantRuntime: "rs-ffffffffffffffffffffffffffffffff"},
		{name: "a named Kitty window wins over the environment", env: debugRuntimeID, window: 9, explicitSocket: "unix:/kitty-other", wantWindow: 9},
		{name: "the current Kitty window", kittyWindow: "77", kittySocket: "unix:/kitty-local", wantWindow: 77},
		{name: "Kitty window without a socket", window: 9, wantErr: "Kitty socket", wantNoRequest: true},
		{name: "both named", flag: debugRuntimeID, window: 9, wantErr: "cannot be used with --window", wantNoRequest: true},
		{name: "malformed flag", flag: "7", wantErr: "--runtime-session requires a valid runtime session ID", wantNoRequest: true},
		{name: "malformed environment", env: "rs-nope", wantErr: runtimesession.EnvVar + " requires a valid runtime session ID", wantNoRequest: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetDebugTestState(t)
			probe := &recordingProbe{}
			debugProbeClient = func() debugger.ProbeClient { return probe }
			debugExecutable = func() (string, error) { return "/tmp/amx", nil }
			debugProbeJSON = true
			debugRuntimeSession, debugSelectorFlags.windowID, debugProbeKittySocket = tc.flag, tc.window, tc.explicitSocket
			debugGetenv = func(key string) string {
				if key == runtimesession.EnvVar {
					return tc.env
				}
				if key == "KITTY_LISTEN_ON" {
					return tc.kittySocket
				}
				return ""
			}
			t.Setenv("KITTY_WINDOW_ID", tc.kittyWindow)

			var out bytes.Buffer
			debugProbeCmd.SetOut(&out)
			err := debugProbeCmd.RunE(debugProbeCmd, nil)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || len(probe.requests) != 0 {
					t.Fatalf("err = %v, requests = %+v; want %q and nothing sent", err, probe.requests, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantSocket := tc.explicitSocket
			if wantSocket == "" {
				wantSocket = tc.kittySocket
			}
			if tc.wantRuntime != "" {
				wantSocket = ""
			}
			if len(probe.requests) != 1 || probe.requests[0].RuntimeSessionID != tc.wantRuntime || probe.requests[0].WindowID != tc.wantWindow || probe.requests[0].KittyListenOn != wantSocket {
				t.Fatalf("requests = %+v; want runtime %q window %d", probe.requests, tc.wantRuntime, tc.wantWindow)
			}
			var report debugger.ProbeReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if !report.Success || report.RuntimeSession != tc.wantRuntime || report.WindowID != tc.wantWindow {
				t.Fatalf("report = %+v", report)
			}
		})
	}
}

// `debug capture start` typed in a tmux session scopes the capture to that
// session's runtime session, which is the only thing its hooks carry.
func TestDebugCaptureScopesToTheCurrentRuntimeSession(t *testing.T) {
	resetDebugTestState(t)
	oldDuration, oldInclude, oldYes, oldJSON := debugCaptureDuration, debugCaptureInclude, debugCaptureYes, debugCaptureJSON
	oldInteractive, oldOut := debugInputInteractive, debugCaptureStartCmd.OutOrStdout()
	t.Cleanup(func() {
		debugCaptureDuration, debugCaptureInclude, debugCaptureYes, debugCaptureJSON = oldDuration, oldInclude, oldYes, oldJSON
		debugInputInteractive = oldInteractive
		debugCaptureStartCmd.SetOut(oldOut)
	})
	paths := diagnostics.PathsForRoot(filepath.Join(t.TempDir(), "state"))
	debugPaths = func() (diagnostics.Paths, error) { return paths, nil }
	debugNow = func() time.Time { return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC) }
	debugGetenv = func(key string) string {
		if key == runtimesession.EnvVar {
			return debugRuntimeID
		}
		return ""
	}
	t.Setenv("KITTY_WINDOW_ID", "77")
	debugCaptureDuration, debugCaptureInclude, debugCaptureYes, debugCaptureJSON = time.Minute, true, true, true
	debugInputInteractive = func(io.Reader) bool { return false }

	var start bytes.Buffer
	debugCaptureStartCmd.SetOut(&start)
	if err := debugCaptureStartCmd.RunE(debugCaptureStartCmd, nil); err != nil {
		t.Fatal(err)
	}
	var report debugger.CaptureReport
	if err := json.Unmarshal(start.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Descriptor == nil || report.Descriptor.Scope != (diagnostics.CaptureScope{RuntimeSessionID: debugRuntimeID}) {
		t.Fatalf("start report = %+v; want the capture scoped to %s alone", report.Descriptor, debugRuntimeID)
	}
}
