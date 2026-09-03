package ipc

// The dashboard's state-query round trip: its own deadline, a realistic
// topology payload, and both directions of old/new peer compatibility. The
// daemon-side construction of the snapshot block is covered in
// internal/daemon; here the question is only what survives the socket.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// serveOneResponse accepts a single connection on a fresh socket, reads the
// request line, and writes raw as the response. It returns the socket path and
// a channel carrying the request the client actually sent.
func serveOneResponse(t *testing.T, raw []byte) (socketPath string, requests <-chan protocol.Request) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "amx-state-query-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath = filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	seen := make(chan protocol.Request, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		line, readErr := bufio.NewReader(conn).ReadBytes('\n')
		if readErr != nil {
			return
		}
		var req protocol.Request
		_ = json.Unmarshal(line, &req)
		seen <- req
		_, _ = conn.Write(append(raw, '\n'))
	}()
	return socketPath, seen
}

func mustEncodeResponse(t *testing.T, resp protocol.Response) []byte {
	t.Helper()
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("encoding response: %v", err)
	}
	return data
}

// TestStateQueryOwnsADedicatedOneSecondDeadline pins the new budget and, just
// as importantly, that adding it moved none of the existing ones. The hook
// acknowledgement path and the cold-start path are latency contracts with
// harnesses, not internal tuning knobs.
func TestStateQueryOwnsADedicatedOneSecondDeadline(t *testing.T) {
	if stateQueryDeadline != time.Second {
		t.Fatalf("stateQueryDeadline = %v; want one second", stateQueryDeadline)
	}
	if sendDeadline != NormalAckBudget || NormalAckBudget != 250*time.Millisecond {
		t.Fatalf("send deadline = %v (NormalAckBudget %v); want the unchanged 250ms acknowledgement budget",
			sendDeadline, NormalAckBudget)
	}
	if autoStartDeadline != coldStartBudget || coldStartBudget != 2*time.Second {
		t.Fatalf("auto-start deadline = %v; want the unchanged two-second cold-start budget", autoStartDeadline)
	}
	if exportDeadline != 5*time.Second {
		t.Fatalf("export deadline = %v; want the unchanged five-second bulk-export budget", exportDeadline)
	}
	if restartSocketPollCount != 20 || restartSocketPollDelay != 50*time.Millisecond {
		t.Fatalf("shutdown teardown budget = %d x %v; want the unchanged one-second wait",
			restartSocketPollCount, restartSocketPollDelay)
	}
	if maxResponseBytes != 64<<20 {
		t.Fatalf("maxResponseBytes = %d; want the retained 64MiB response ceiling", maxResponseBytes)
	}
}

// TestStateQueryTimesOutOnASilentDaemon proves the budget is enforced rather
// than merely declared: a daemon that accepts the connection and never answers
// must release the caller near one second — long enough that a state query is
// not held to the hook budget, short enough that a dashboard refresh can fall
// back instead of hanging.
func TestStateQueryTimesOutOnASilentDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "amx-state-query-silent-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		accepted <- conn // held open and never written to
	}()

	start := time.Now()
	_, err = NewClientWithSocket(socketPath).StateQuery()
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("StateQuery succeeded against a daemon that never responded")
	}
	select {
	case conn := <-accepted:
		_ = conn.Close()
	default:
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("StateQuery gave up after %v; want it to wait out its one-second budget", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("StateQuery waited %v; want it bounded near one second", elapsed)
	}
}

// TestStateQueryCarriesRepresentativeTopology sends a topology the size a real
// multi-project session produces — roughly 46 KB once every pane's user vars
// and foreground processes are encoded — and pins that it arrives whole. The
// point is headroom: the payload is orders of magnitude below the retained
// 64 MiB ceiling, so the size of the window tree is not what a refresh has to
// worry about.
func TestStateQueryCarriesRepresentativeTopology(t *testing.T) {
	windows := representativeTopology()
	panes := countPanes(windows)
	raw := mustEncodeResponse(t, protocol.Response{
		OK: true,
		States: map[int]protocol.WindowStateSnapshot{
			windows[0].Tabs[0].Windows[0].ID: {IconState: "bolt", LastAction: "active"},
		},
		KittySnapshot: &protocol.KittySnapshot{
			Ready: true, Generation: 7, CapturedAt: 1_700_000_000, AgeMS: 120,
			Coordinator: &protocol.KittyListCoordinatorStatus{Socket: "unix:/tmp/kitty-756", Attempts: 7},
			Windows:     windows,
		},
	})
	if len(raw) < 40_000 || len(raw) > 60_000 {
		t.Fatalf("representative payload is %d bytes; the fixture no longer models the ~46KB case", len(raw))
	}
	if len(raw) >= maxResponseBytes {
		t.Fatalf("representative payload %d bytes exceeds the %d ceiling", len(raw), maxResponseBytes)
	}

	socketPath, requests := serveOneResponse(t, raw)
	resp, err := NewClientWithSocket(socketPath).StateQuery()
	if err != nil {
		t.Fatalf("StateQuery: %v", err)
	}
	if req := <-requests; req.Type != protocol.TypeStateQuery {
		t.Fatalf("daemon received request type %q; want %q", req.Type, protocol.TypeStateQuery)
	}
	if resp.KittySnapshot == nil {
		t.Fatal("snapshot block missing from the decoded response")
	}
	if got := countPanes(resp.KittySnapshot.Windows); got != panes {
		t.Fatalf("decoded %d panes; want all %d", got, panes)
	}
	last := windows[len(windows)-1].Tabs
	wantLast := last[len(last)-1].Windows
	gotTabs := resp.KittySnapshot.Windows[len(resp.KittySnapshot.Windows)-1].Tabs
	gotLast := gotTabs[len(gotTabs)-1].Windows
	if gotLast[len(gotLast)-1].ID != wantLast[len(wantLast)-1].ID {
		t.Fatalf("last pane = %d; want %d — the response was truncated",
			gotLast[len(gotLast)-1].ID, wantLast[len(wantLast)-1].ID)
	}
	if got := gotLast[len(gotLast)-1].UserVars.EffectiveHarness(); got != "claude" {
		t.Fatalf("last pane harness = %q; want the user vars to survive the wire", got)
	}
}

// TestStateQueryAgainstAPreBlockDaemon is the first compatibility direction: a
// new client talking to a daemon that never learned to publish the block. The
// states still arrive, and the nil block is the signal that the client must do
// its own Kitty read rather than render an empty dashboard.
func TestStateQueryAgainstAPreBlockDaemon(t *testing.T) {
	const legacy = `{"ok":true,"activity":{"42":1700000000},` +
		`"watchdog":{"42":{"last_action":"active","last_event_at":1700000000,"threshold_sec":300}},` +
		`"states":{"42":{"icon_state":"bolt","last_action":"active"}}}`

	socketPath, _ := serveOneResponse(t, []byte(legacy))
	resp, err := NewClientWithSocket(socketPath).StateQuery()
	if err != nil {
		t.Fatalf("StateQuery against a pre-block daemon: %v", err)
	}
	if resp.KittySnapshot != nil {
		t.Fatalf("snapshot block = %+v; want nil from a daemon that publishes none", resp.KittySnapshot)
	}
	if resp.States[42].IconState != "bolt" || resp.Activity[42] != 1_700_000_000 ||
		resp.Watchdog[42].ThresholdSec != 300 {
		t.Fatalf("pre-block response lost fields the client still reads: %+v", resp)
	}
}

// TestStateQuerySurfacesDaemonRefusal pins that a rejected query is an error
// rather than a zero-valued success, so a caller cannot mistake a refusal for a
// daemon with no windows.
func TestStateQuerySurfacesDaemonRefusal(t *testing.T) {
	raw := mustEncodeResponse(t, protocol.Response{OK: false, Error: "unknown request type: state_query"})
	socketPath, _ := serveOneResponse(t, raw)
	if _, err := NewClientWithSocket(socketPath).StateQuery(); err == nil {
		t.Fatal("StateQuery returned no error for a refused request")
	}
}

// TestClientHealthCarriesSnapshotStatus extends the health projection contract:
// the status block reaches Health, and it reaches it without disturbing
// readiness. A daemon can be perfectly ready while Kitty has stopped answering,
// and doctor has to be able to say so.
func TestClientHealthCarriesSnapshotStatus(t *testing.T) {
	raw := mustEncodeResponse(t, protocol.Response{
		OK: true, PID: 42, UptimeSeconds: 7, Ready: true,
		KittySnapshot: &protocol.KittySnapshot{
			Ready: true, Stale: true, Generation: 11, CapturedAt: 1_700_000_000, AgeMS: 9_000,
			Coordinator: &protocol.KittyListCoordinatorStatus{
				Socket: "unix:/tmp/kitty-756", Attempts: 4, Failures: 1, Timeouts: 1,
				ConsecutiveFailures: 1, LastErrorClass: "timeout",
			},
		},
	})
	socketPath, _ := serveOneResponse(t, raw)
	health := NewClientWithSocket(socketPath).Health()
	if !health.Alive || !health.Ready || health.DegradedReason != "" {
		t.Fatalf("health = %+v; want a stale snapshot to leave readiness alone", health)
	}
	if health.KittySnapshot == nil {
		t.Fatal("health carries no snapshot status block")
	}
	if !health.KittySnapshot.Stale || health.KittySnapshot.AgeMS != 9_000 {
		t.Fatalf("snapshot status = %+v; want the stale nine-second-old block", health.KittySnapshot)
	}
	if health.KittySnapshot.Coordinator == nil ||
		health.KittySnapshot.Coordinator.LastErrorClass != string(kitty.ListErrorTimeout) {
		t.Fatalf("coordinator = %+v; want the timeout classification", health.KittySnapshot.Coordinator)
	}
	if health.KittySnapshot.Windows != nil {
		t.Fatalf("health snapshot carried %d windows; want status only", len(health.KittySnapshot.Windows))
	}
}

// TestClientHealthAgainstAPreBlockDaemon is the health half of the first
// compatibility direction: an old daemon's health answer leaves the block nil,
// which doctor must read as unknown rather than as a cold cache.
func TestClientHealthAgainstAPreBlockDaemon(t *testing.T) {
	const legacy = `{"ok":true,"pid":42,"uptime_seconds":7,"ready":true}`
	socketPath, _ := serveOneResponse(t, []byte(legacy))
	health := NewClientWithSocket(socketPath).Health()
	if !health.Alive || health.PID != 42 || !health.Ready {
		t.Fatalf("health = %+v; want the pre-block fields intact", health)
	}
	if health.KittySnapshot != nil {
		t.Fatalf("snapshot status = %+v; want nil from a daemon that publishes none", health.KittySnapshot)
	}
}

// representativeTopology builds four Kitty OS windows of seven tabs each with
// two panes per tab: 56 managed panes carrying the user vars, cmdlines, and
// foreground processes a real session has. It is a size fixture, not a
// behavioral one — nothing here depends on the specific IDs.
func representativeTopology() []kitty.OSWindow {
	var osWindows []kitty.OSWindow
	paneID := 0
	for w := 0; w < 4; w++ {
		osWindow := kitty.OSWindow{ID: w + 1, IsFocused: w == 0}
		for tabIndex := 0; tabIndex < 7; tabIndex++ {
			tab := kitty.Tab{
				ID:       (w+1)*100 + tabIndex,
				IsActive: tabIndex == 0,
				Title:    fmt.Sprintf("W:project-%d ⚡", tabIndex),
			}
			for pane := 0; pane < 2; pane++ {
				paneID++
				tab.Windows = append(tab.Windows, kitty.Window{
					ID:      paneID,
					Title:   "claude",
					CWD:     "/Users/someone/projects/api",
					Cmdline: []string{"claude", "--resume", "0199f0a1-2b3c-4d5e-6f70-8192a3b4c5d6"},
					UserVars: kitty.UserVars{
						Profile:      "work",
						Harness:      "claude",
						HomeDir:      "/Users/someone/.claude-work",
						Project:      "api",
						AMXDirectory: "/Users/someone/projects/api",
						LaunchID:     "0199f0a1-2b3c-4d5e-6f70-8192a3b4c5d6",
					},
					ForegroundProcesses: []kitty.ForegroundProcess{{
						PID:     4000 + paneID,
						Cmdline: []string{"claude"},
						CWD:     "/Users/someone/projects/api",
					}},
				})
			}
			osWindow.Tabs = append(osWindow.Tabs, tab)
		}
		osWindows = append(osWindows, osWindow)
	}
	return osWindows
}

func countPanes(osWindows []kitty.OSWindow) int {
	count := 0
	for _, osWindow := range osWindows {
		for _, tab := range osWindow.Tabs {
			count += len(tab.Windows)
		}
	}
	return count
}
