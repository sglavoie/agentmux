package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/diagnostics"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/testutil"
)

const (
	lifecycleParentChildWindow   = 42
	lifecycleCodexPipeWindow     = 43
	lifecycleCopilotPipeWindow   = 44
	lifecycleConcurrentWindow    = 45
	lifecycleProtectedWindow     = 46
	lifecycleNativeBeforeWindow  = 47
	lifecycleNativeAfterWindow   = 48
	lifecycleClaudeInteractive   = 49
	lifecycleCodexInteractive    = 50
	lifecycleCopilotInteractive  = 51
	lifecycleRestartWindow       = 52
	lifecycleASLWindow           = 53
	lifecycleResetWindow         = 54
	lifecycleStopHoldWindow      = 55
	lifecycleHookRetryBudget     = 2 * time.Second
	lifecycleAcceptanceKittyAddr = "unix:/tmp/agent-mux-process-lifecycle-acceptance-kitty"
)

// The confirmed-idle gate is configured far longer than this fixture runs, so a
// held stop can only leave the gate by being bypassed, never by timing out.
const (
	lifecycleAcceptanceStopConfirmDelay   = 300
	lifecycleAcceptanceStopConfirmTimeout = 600
)

type processLifecycleAcceptanceFixture struct {
	t            *testing.T
	home         string
	stateBase    string
	kittyFixture string
	kittyLog     string
	psState      string
	psBlock      string
	windows      []kitty.OSWindow
	live         map[int]string
	requestSeq   int
	cfg          *config.Config
	cancel       context.CancelFunc
	done         chan error
}

func newProcessLifecycleAcceptanceFixture(t *testing.T) *processLifecycleAcceptanceFixture {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "amx-process-lifecycle-acceptance-")
	if err != nil {
		t.Fatal(err)
	}
	stateBase := filepath.Join(home, "state")
	fixture := &processLifecycleAcceptanceFixture{
		t: t, home: home, stateBase: stateBase,
		kittyFixture: filepath.Join(home, "kitty.json"),
		kittyLog:     filepath.Join(home, "kitten-commands.log"),
		psState:      filepath.Join(home, "ps-state.txt"),
		psBlock:      filepath.Join(home, "ps-block"),
		live:         make(map[int]string),
	}
	disableContextProbe := false
	fixture.cfg = &config.Config{Defaults: config.Defaults{
		GuardDuration: 5,
		Diagnostics:   config.DiagnosticsConfig{JournalKeepDays: 14, JournalMaxMiB: 16},
		Watchdog: config.WatchdogConfig{
			Active: 600, Subagent: 1800, Compact: 180, HookSilence: 14400,
			ClaudeContextLimitProbe: &disableContextProbe,
		},
		StopConfirm: config.StopConfirmConfig{
			Delay:   lifecycleAcceptanceStopConfirmDelay,
			Timeout: lifecycleAcceptanceStopConfirmTimeout,
		},
	}}
	fixture.windows = lifecycleAcceptanceWindows(t, home)
	fixture.writeKittyFixture()
	fixture.writeProcessState()
	fixture.installExecutables()

	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", stateBase)
	t.Setenv("KITTY_LISTEN_ON", lifecycleAcceptanceKittyAddr)
	t.Setenv("KITTY_WINDOW_ID", strconv.Itoa(lifecycleParentChildWindow))
	t.Setenv("AMX_ACCEPTANCE_KITTY_FIXTURE", fixture.kittyFixture)
	t.Setenv("AMX_ACCEPTANCE_KITTY_LOG", fixture.kittyLog)
	t.Setenv("AMX_ACCEPTANCE_PS_STATE", fixture.psState)
	t.Setenv("AMX_ACCEPTANCE_PS_BLOCK", fixture.psBlock)
	t.Setenv("PATH", filepath.Join(home, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		fixture.stopDaemon()
		_ = os.RemoveAll(home)
	})
	return fixture
}

func lifecycleAcceptanceWindows(t *testing.T, home string) []kitty.OSWindow {
	t.Helper()
	harnesses := map[int]string{
		lifecycleParentChildWindow: "claude", lifecycleCodexPipeWindow: "codex",
		lifecycleCopilotPipeWindow: "copilot", lifecycleConcurrentWindow: "codex",
		lifecycleProtectedWindow: "claude", lifecycleNativeBeforeWindow: "codex",
		lifecycleNativeAfterWindow: "claude", lifecycleClaudeInteractive: "claude",
		lifecycleCodexInteractive: "codex", lifecycleCopilotInteractive: "copilot",
		lifecycleRestartWindow: "claude", lifecycleASLWindow: "claude", lifecycleResetWindow: "copilot",
		lifecycleStopHoldWindow: "claude",
	}
	ids := make([]int, 0, len(harnesses))
	for id := range harnesses {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	tabs := make([]kitty.Tab, 0, len(ids))
	for _, id := range ids {
		workspace := filepath.Join(home, "workspace", strconv.Itoa(id))
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		harnessName := harnesses[id]
		tabs = append(tabs, kitty.Tab{
			ID: id + 1000, IsActive: true, Title: fmt.Sprintf("W:lifecycle-%d", id),
			Windows: []kitty.Window{{
				ID: id, IsActive: true, CWD: workspace, Cmdline: []string{harnessName},
				UserVars: kitty.UserVars{
					Profile: "acceptance", Harness: harnessName, Project: fmt.Sprintf("lifecycle-%d", id),
					AMXDirectory: workspace, HarnessSessionID: fmt.Sprintf("managed-%d", id),
				},
			}},
		})
	}
	return []kitty.OSWindow{{ID: 1, IsActive: true, IsFocused: true, Tabs: tabs}}
}

func (f *processLifecycleAcceptanceFixture) installExecutables() {
	f.t.Helper()
	binDir := filepath.Join(f.home, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	kittenScript := `#!/bin/sh
printf '%s\n' "$*" >> "$AMX_ACCEPTANCE_KITTY_LOG"
case " $* " in
  *" ls "*) exec /bin/cat "$AMX_ACCEPTANCE_KITTY_FIXTURE" ;;
  *) exit 0 ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "kitten"), []byte(kittenScript), 0o700); err != nil {
		f.t.Fatal(err)
	}
	psScript := `#!/bin/sh
pids=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-p" ] && [ "$#" -gt 1 ]; then
    pids="$2"
    shift 2
    continue
  fi
  shift
done
source_file="$AMX_ACCEPTANCE_PS_STATE"
block_pid=""
if [ -f "$AMX_ACCEPTANCE_PS_BLOCK.enabled" ]; then
  block_pid=$(/bin/cat "$AMX_ACCEPTANCE_PS_BLOCK.enabled")
fi
case ",$pids," in
  *",$block_pid,"*)
    if [ -n "$block_pid" ]; then
      source_file="$AMX_ACCEPTANCE_PS_BLOCK.snapshot.$$"
      /bin/cp "$AMX_ACCEPTANCE_PS_STATE" "$source_file"
      : > "$AMX_ACCEPTANCE_PS_BLOCK.entered"
      while [ ! -f "$AMX_ACCEPTANCE_PS_BLOCK.release" ]; do /bin/sleep 0.01; done
    fi
    ;;
esac
/usr/bin/awk -v wanted="$pids" '
BEGIN { n = split(wanted, values, ","); for (i = 1; i <= n; i++) keep[values[i]] = 1 }
$1 in keep { print; found = 1 }
END { if (!found) exit 1 }
' "$source_file"
status=$?
case "$source_file" in "$AMX_ACCEPTANCE_PS_BLOCK.snapshot."*) /bin/rm -f "$source_file" ;; esac
exit "$status"
`
	if err := os.WriteFile(filepath.Join(binDir, "ps"), []byte(psScript), 0o700); err != nil {
		f.t.Fatal(err)
	}
}

func (f *processLifecycleAcceptanceFixture) startDaemon() {
	f.t.Helper()
	if f.cancel != nil {
		f.t.Fatal("acceptance daemon already running")
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	f.done = make(chan error, 1)
	go func() { f.done <- daemon.Run(ctx, f.cfg) }()
	testutil.Eventually(f.t, "process lifecycle daemon readiness", func() bool {
		health := ipc.NewClient().Health()
		return health.Alive && health.Ready
	})
}

func (f *processLifecycleAcceptanceFixture) stopDaemon() {
	if f.cancel == nil {
		return
	}
	f.cancel()
	select {
	case err := <-f.done:
		if err != nil {
			f.t.Fatalf("process lifecycle daemon shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		f.t.Fatal("process lifecycle daemon did not stop")
	}
	f.cancel = nil
	f.done = nil
}

func (f *processLifecycleAcceptanceFixture) workspace(windowID int) string {
	return filepath.Join(f.home, "workspace", strconv.Itoa(windowID))
}

func (f *processLifecycleAcceptanceFixture) sendHook(
	windowID int,
	harnessName string,
	event string,
	sessionID string,
	seq int,
	pipeMode bool,
	identity *protocol.HarnessProcessIdentity,
	extra map[string]any,
) {
	f.t.Helper()
	f.requestSeq++
	payload := map[string]any{
		"source_sequence": strconv.Itoa(seq),
		"source_event_id": fmt.Sprintf("lifecycle-%d:%d:%d", windowID, f.requestSeq, seq),
		"emitted_time":    strconv.FormatInt(time.Now().Unix(), 10),
	}
	if sessionID != "" {
		payload["session_id"] = sessionID
		payload["turn_id"] = "turn-1"
	}
	for key, value := range extra {
		payload[key] = value
	}
	stampASLContract(f.t, payload)
	encoded, err := json.Marshal(payload)
	if err != nil {
		f.t.Fatal(err)
	}
	var process *protocol.HarnessProcessIdentity
	if identity != nil {
		copy := *identity
		process = &copy
	}
	req := protocol.Request{
		Type: protocol.TypeHookEvent, WindowID: windowID, Event: event,
		Harness: harnessName, ProducerHarness: harnessName, HarnessProcess: process,
		PipeMode: pipeMode, PayloadJSON: string(encoded), Account: "acceptance",
		SessionTitle: fmt.Sprintf("W:lifecycle-%d", windowID), KittyListenOn: lifecycleAcceptanceKittyAddr,
		RequestID:   fmt.Sprintf("lifecycle-request-%d", f.requestSeq),
		TraceID:     fmt.Sprintf("lifecycle-trace-%d", f.requestSeq),
		PayloadSize: int64(len(encoded)), PayloadEncoding: "json",
	}
	client := ipc.NewClient()
	deadline := time.Now().Add(lifecycleHookRetryBudget)
	var resp protocol.Response
	// Package-level load can exceed the production acknowledgement budget even
	// though the hermetic daemon is making progress. Bound the complete retry
	// window by the production cold-start budget and reuse the request ID so a
	// retry observes the dedupe result instead of applying the hook twice.
	attempt := 1
	for {
		req.Attempt = attempt
		resp, err = client.Send(req)
		if err == nil && !resp.Retryable {
			break
		}
		var netErr net.Error
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && (!errors.As(err, &netErr) || !netErr.Timeout()) {
			f.t.Fatalf("send %s for window %d: %v", event, windowID, err)
		}
		if time.Now().After(deadline) {
			if err != nil {
				f.t.Fatalf("send %s for window %d: %v", event, windowID, err)
			}
			f.t.Fatalf("send %s for window %d response remained retryable: %+v", event, windowID, resp)
		}
		attempt++
		time.Sleep(10 * time.Millisecond)
	}
	acknowledged := resp.Delivery != nil &&
		(resp.Delivery.Status == protocol.DeliveryAcknowledgementAccepted ||
			attempt > 1 && resp.Delivery.Status == protocol.DeliveryAcknowledgementDuplicate)
	if !resp.OK || !acknowledged {
		f.t.Fatalf("send %s for window %d response = %+v", event, windowID, resp)
	}
}

func (f *processLifecycleAcceptanceFixture) setProcess(pid int, token string) {
	f.live[pid] = token
	f.writeProcessState()
}

func (f *processLifecycleAcceptanceFixture) removeProcess(pid int) {
	delete(f.live, pid)
	f.writeProcessState()
}

func (f *processLifecycleAcceptanceFixture) writeProcessState() {
	f.t.Helper()
	pids := make([]int, 0, len(f.live))
	for pid := range f.live {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	var content strings.Builder
	for _, pid := range pids {
		fmt.Fprintf(&content, "%d %s\n", pid, f.live[pid])
	}
	tmp := f.psState + ".tmp"
	if err := os.WriteFile(tmp, []byte(content.String()), 0o600); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(tmp, f.psState); err != nil {
		f.t.Fatal(err)
	}
}

func (f *processLifecycleAcceptanceFixture) enableProcessBlock(pid int) {
	f.t.Helper()
	for _, suffix := range []string{".entered", ".release"} {
		_ = os.Remove(f.psBlock + suffix)
	}
	if err := os.WriteFile(f.psBlock+".enabled", []byte(strconv.Itoa(pid)), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *processLifecycleAcceptanceFixture) waitForProcessBlock() {
	f.t.Helper()
	testutil.Eventually(f.t, "process inspection block", func() bool {
		_, err := os.Stat(f.psBlock + ".entered")
		return err == nil
	})
}

func (f *processLifecycleAcceptanceFixture) releaseProcessBlock() {
	f.t.Helper()
	if err := os.WriteFile(f.psBlock+".release", nil, 0o600); err != nil {
		f.t.Fatal(err)
	}
	_ = os.Remove(f.psBlock + ".enabled")
}

func (f *processLifecycleAcceptanceFixture) waitForExport(
	description string,
	ready func(protocol.EventsExport) bool,
) protocol.EventsExport {
	f.t.Helper()
	var last protocol.EventsExport
	testutil.Eventually(f.t, description, func() bool {
		export, err := ipc.NewClient().ExportEvents(0)
		if err != nil {
			return false
		}
		last = export
		return ready(export)
	})
	return last
}

func (f *processLifecycleAcceptanceFixture) persistedState(windowID int) string {
	f.t.Helper()
	data, err := os.ReadFile(f.kittyLog)
	if err != nil {
		f.t.Fatal(err)
	}
	match := "--match=id:" + strconv.Itoa(windowID)
	state := ""
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, " set-user-vars ") || !strings.Contains(line, match) {
			continue
		}
		if _, value, ok := strings.Cut(line, "amx_state="); ok && json.Valid([]byte(value)) {
			state = value
		}
	}
	if state == "" {
		f.t.Fatalf("no persisted amx_state for window %d in:\n%s", windowID, data)
	}
	return state
}

func (f *processLifecycleAcceptanceFixture) setPersistedState(windowID int, state string) {
	f.t.Helper()
	found := false
	for oi := range f.windows {
		for ti := range f.windows[oi].Tabs {
			for wi := range f.windows[oi].Tabs[ti].Windows {
				win := &f.windows[oi].Tabs[ti].Windows[wi]
				if win.ID == windowID {
					win.UserVars.AMXState = state
					found = true
				}
			}
		}
	}
	if !found {
		f.t.Fatalf("window %d not found in Kitty fixture", windowID)
	}
	f.writeKittyFixture()
}

func (f *processLifecycleAcceptanceFixture) writeKittyFixture() {
	f.t.Helper()
	data, err := json.Marshal(f.windows)
	if err != nil {
		f.t.Fatal(err)
	}
	tmp := f.kittyFixture + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(tmp, f.kittyFixture); err != nil {
		f.t.Fatal(err)
	}
}

func (f *processLifecycleAcceptanceFixture) journalRecords() []diagnostics.TraceEnvelope {
	f.t.Helper()
	paths := diagnostics.PathsForRoot(filepath.Join(f.stateBase, "agent-mux"))
	journal, err := diagnostics.NewJournal(paths, diagnostics.JournalOptions{})
	if err != nil {
		f.t.Fatal(err)
	}
	var records []diagnostics.TraceEnvelope
	testutil.Eventually(f.t, "process lifecycle journal availability", func() bool {
		if _, err = journal.PromoteSpool(f.t.Context()); err != nil {
			return false
		}
		records, err = journal.Read(f.t.Context(), diagnostics.Selector{})
		return err == nil
	})
	return records
}
