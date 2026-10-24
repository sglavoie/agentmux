package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/asl"
	"github.com/sglavoie/agentmux/internal/daemon/ipc"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/testutil"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// This file drives the installed agent-session-loop through real runs against
// this build's daemon handler. It runs when AMX_ASL_INTEGRATION is set, which
// `just asl-integration` does, and is skipped otherwise so `go test ./...`
// stays hermetic. Like the bridge gate in internal/aslbridge, once requested it
// fails rather than skips: a missing asl, tmux, or go toolchain is a gate that
// did not run, not one that passed.
//
// Nothing here touches the operator's world. The runs execute throwaway plans
// in throwaway Git repositories under one temporary root, which is also their
// HOME: the `amx` they find on PATH is built from this checkout, and the daemon
// socket it dials is the one this test serves. The harness is a stand-in
// script; ASL, its mailbox, its lifecycle hooks, its durable projection, and
// its bridge are the real installed program. The tmux server and the
// runtime-session registry are real and private (tmuxtest).

const aslInstalledIntegrationEnv = "AMX_ASL_INTEGRATION"

// aslWorld is the isolated machine one integration test runs on.
type aslWorld struct {
	t         *testing.T
	root      string
	asl       string
	stateRoot string
	plansRoot string
	logs      string
	socket    string
	env       []string
	tmux      *tmuxtest.Fixture
}

func requireInstalledASL(t *testing.T) string {
	t.Helper()
	if os.Getenv(aslInstalledIntegrationEnv) == "" {
		t.Skipf("set %s=1 to run against the installed agent-session-loop", aslInstalledIntegrationEnv)
	}
	command := os.Getenv("AMX_ASL_COMMAND")
	if command == "" {
		command = "asl"
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		t.Fatalf("%s is set but %q is not installed: %v", aslInstalledIntegrationEnv, command, err)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatalf("%s is set but tmux is not installed: %v", aslInstalledIntegrationEnv, err)
	}
	return resolved
}

func newASLWorld(t *testing.T) *aslWorld {
	t.Helper()
	aslPath := requireInstalledASL(t)
	// The daemon socket lives under the world's HOME and socket paths are
	// length-limited, so the root is short rather than t.TempDir().
	root, err := os.MkdirTemp("/tmp", "amxs10")
	if err != nil {
		t.Fatal(err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	w := &aslWorld{
		t:         t,
		root:      root,
		asl:       aslPath,
		stateRoot: filepath.Join(root, "state"),
		plansRoot: filepath.Join(root, "plans"),
		logs:      filepath.Join(root, "logs"),
		socket:    filepath.Join(root, ".local", "share", "agent-mux", "daemon.sock"),
	}
	for _, dir := range []string{"bin", "state", "plans", "logs", "tmp", "xdg-state", "xdg-data", "xdg-config", filepath.Dir(w.socket)} {
		path := dir
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, dir)
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	pathDirs := []string{filepath.Join(root, "bin"), filepath.Dir(aslPath)}
	for _, tool := range []string{"python3", "git", "perl"} {
		resolved, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("%s is set but %s is not installed: %v", aslInstalledIntegrationEnv, tool, err)
		}
		pathDirs = append(pathDirs, filepath.Dir(resolved))
	}
	pathDirs = append(pathDirs, "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	w.env = []string{
		"HOME=" + root,
		"PATH=" + strings.Join(pathDirs, string(os.PathListSeparator)),
		"XDG_STATE_HOME=" + filepath.Join(root, "xdg-state"),
		"XDG_DATA_HOME=" + filepath.Join(root, "xdg-data"),
		"XDG_CONFIG_HOME=" + filepath.Join(root, "xdg-config"),
		"AGENT_PLANS_DIR=" + w.plansRoot,
		"AMX_CONFIG=" + filepath.Join(root, "config.toml"),
		"TMPDIR=" + filepath.Join(root, "tmp"),
		"LC_ALL=C",
	}

	w.buildAMX()
	w.writeHarness()
	w.tmux = tmuxtest.New(t)
	return w
}

// buildAMX builds this checkout's amx into the world's PATH, so the hook
// client ASL invokes is the one under test and dials the world's socket.
func (w *aslWorld) buildAMX() {
	w.t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		w.t.Fatalf("locate module root: %v", err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(w.root, "bin", "amx"), ".")
	build.Dir = strings.TrimSpace(string(out))
	if combined, err := build.CombinedOutput(); err != nil {
		w.t.Fatalf("build amx: %v\n%s", err, combined)
	}
}

// writeHarness installs the stand-in `claude`. One turn of it is one worker
// behaving the way a real one does when it needs a decision: it asks through
// the mailbox, blocks until the supervisor replies, then completes its session.
func (w *aslWorld) writeHarness() {
	w.t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then
	echo "2.1.296 (Claude Code)"
	exit 0
fi
plan=""
while [ $# -gt 0 ]; do
	if [ "$1" = "--add-dir" ]; then plan="$2"; fi
	shift
done
name=$(basename "$plan")
echo "turn started for $plan" >> %[1]q/harness-$name.log
%[2]q ask "$plan" "Proceed with the fixture change?" --state-dir %[3]q --wait --wait-timeout 240 --json \
	> %[1]q/ask-$name.json 2> %[1]q/ask-$name.err
echo "ask returned $?" >> %[1]q/harness-$name.log
perl -pi -e 's/\*\*Status\*\*: pending/**Status**: complete/' "$plan/session-1.md"
echo "Runner status: complete"
`, w.logs, w.asl, w.stateRoot)
	if err := os.WriteFile(filepath.Join(w.root, "bin", "claude"), []byte(script), 0o700); err != nil {
		w.t.Fatal(err)
	}
}

// aslPlan is one throwaway plan and the repository its run works in.
type aslPlan struct {
	name string
	dir  string
	repo string
}

func (w *aslWorld) newPlan(name string) aslPlan {
	w.t.Helper()
	plan := aslPlan{name: name, dir: filepath.Join(w.plansRoot, name), repo: filepath.Join(w.root, "repo-"+name)}
	files := map[string]string{
		"context.md":  "# Context\n\nAgent Mux integration fixture.\n",
		"manifest.md": "# Manifest\n\n- session-1\n",
		"session-1.md": "# Session 1: " + name + "\n\nFixture session.\n\n## Handoff\n\n" +
			"- **Status**: pending\n- **Notes**:\n",
	}
	if err := os.MkdirAll(plan.dir, 0o700); err != nil {
		w.t.Fatal(err)
	}
	for file, content := range files {
		if err := os.WriteFile(filepath.Join(plan.dir, file), []byte(content), 0o600); err != nil {
			w.t.Fatal(err)
		}
	}
	if err := os.MkdirAll(plan.repo, 0o700); err != nil {
		w.t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"git", "init", "-q", "."},
		{"git", "-c", "user.email=s10@example.invalid", "-c", "user.name=s10", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = plan.repo
		cmd.Env = w.env
		if out, err := cmd.CombinedOutput(); err != nil {
			w.t.Fatalf("%v: %v\n%s", argv, err, out)
		}
	}
	return plan
}

// runArgv is the `asl run` a session executes for plan.
func (w *aslWorld) runArgv(plan aslPlan) []string {
	return []string{
		w.asl, "run", plan.dir,
		"--cwd", plan.repo,
		"--harness", "claude",
		"--state-dir", w.stateRoot,
		"--no-supervisor-audit",
		"--allow-not-loop-ready",
		"--max-sessions", "1",
		"--timeout", "300",
		"--json",
	}
}

// sessionEnv is the environment of a process inside the runtime session id.
func (w *aslWorld) sessionEnv(id runtimesession.ID) []string {
	return append(append([]string(nil), w.env...), runtimesession.EnvVar+"="+string(id))
}

// supervise runs one asl command the way the supervising agent of a session
// would: from inside that session, so the hooks it causes name it.
func (w *aslWorld) supervise(id runtimesession.ID, args ...string) map[string]any {
	w.t.Helper()
	cmd := exec.Command(w.asl, args...)
	cmd.Env = w.sessionEnv(id)
	cmd.Dir = w.root
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			stderr = string(exit.Stderr)
		}
		w.t.Fatalf("asl %s: %v\n%s\n%s", strings.Join(args, " "), err, out, stderr)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		w.t.Fatalf("asl %s printed non-JSON: %v\n%s", strings.Join(args, " "), err, out)
	}
	return decoded
}

// surfaceQuestion long-polls the plan's outbox as its supervisor and returns
// the open question's ID. Surfacing an open set is what makes ASL hand the
// question to Agent Mux.
func (w *aslWorld) surfaceQuestion(id runtimesession.ID, plan aslPlan) string {
	w.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		result := w.supervise(id, "outbox", "wait", plan.dir, "--state-dir", w.stateRoot, "--timeout", "10", "--json")
		if questions, _ := result["questions"].([]any); len(questions) > 0 {
			if question, _ := questions[0].(map[string]any); question != nil {
				if qid, _ := question["id"].(string); qid != "" {
					return qid
				}
			}
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("the worker of %s never asked its question; harness log:\n%s", plan.name, w.readLog("harness-"+plan.name+".log"))
		}
	}
}

// tmuxClients lists the clients attached to the private tmux server; empty
// means no viewer of any kind is looking at a session.
func (w *aslWorld) tmuxClients() string {
	w.t.Helper()
	server := w.tmux.Server
	cmd := exec.Command(server.Binary(), "-S", server.Socket(), "-f", server.ConfigPath(), "list-clients")
	cmd.Env = w.env
	out, err := cmd.Output()
	if err != nil {
		w.t.Fatalf("list tmux clients: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func (w *aslWorld) readLog(name string) string {
	data, err := os.ReadFile(filepath.Join(w.logs, name))
	if err != nil {
		return "(unreadable: " + err.Error() + ")"
	}
	return string(data)
}

// aslDaemon is one daemon lifetime: a Handler behind the world's socket.
type aslDaemon struct {
	h      *Handler
	calls  *callRecorder
	server *ipc.Server
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func (w *aslWorld) startDaemon(kittyWindows []kitty.OSWindow) *aslDaemon {
	w.t.Helper()
	notifier, calls := newTestNotifier(&config.NotificationConfig{Permission: true})
	h := NewHandler(activity.NewStateGuard(time.Hour), notifier)
	h.SetStopConfirm(0, 0, false)
	h.SetRuntimeSessionDirectory(runtimesession.NewReader(w.tmux.Registry))
	h.SetRuntimeCheckpointPath(filepath.Join(w.root, runtimeCheckpointFileName))
	h.SetASLStateRoot(w.stateRoot)
	if kittyWindows != nil {
		h.SetKittyClient(&fakeKittyClient{windows: kittyWindows})
	}
	report := h.restoreRuntimeCheckpoint(time.Now())
	if report.Corrupt {
		w.t.Fatalf("runtime checkpoint restore = %+v", report)
	}
	h.maintenanceTick(time.Now())

	server, err := ipc.NewServer(w.socket, h)
	if err != nil {
		w.t.Fatalf("serve the world's daemon socket: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := &aslDaemon{h: h, calls: calls, server: server, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(d.done)
		_ = server.Serve(ctx)
	}()
	w.t.Cleanup(d.stop)
	return d
}

// stop ends the daemon lifetime the way a shutdown does: the runtime
// checkpoint is flushed, then the socket goes away.
func (d *aslDaemon) stop() {
	d.once.Do(func() {
		d.h.maintainRuntimeSessions(time.Now())
		d.cancel()
		d.server.Close()
		<-d.done
	})
}

func (d *aslDaemon) notificationsFor(slot int) []notifyCall {
	var out []notifyCall
	for _, call := range d.calls.snapshot() {
		if call.windowID == slot {
			out = append(out, call)
		}
	}
	return out
}

func (d *aslDaemon) boundSlot(id runtimesession.ID) (int, bool) {
	return boundSlot(d.h, id)
}
func liveLeases(h *Handler, slot int) []string {
	var runs []string
	for _, fact := range slotFacts(h, slot) {
		if fact.Kind == protocol.SourceASL && fact.LeaseLive {
			runs = append(runs, fact.RunID)
		}
	}
	return runs
}

func (w *aslWorld) bridgeLauncher() aslbridge.Launcher {
	return func(context.Context) (*aslbridge.Child, error) {
		args := append(append([]string(nil), aslbridge.BridgeArgs...),
			"--state-dir", w.stateRoot,
			"--plan-root", w.plansRoot,
		)
		cmd := exec.Command(w.asl, args...)
		cmd.Env = w.env
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		return &aslbridge.Child{
			Path:      w.asl,
			Stdin:     stdin,
			Stdout:    stdout,
			Stderr:    stderr,
			Wait:      cmd.Wait,
			Terminate: func() error { return cmd.Process.Kill() },
		}, nil
	}
}

// TestInstalledBridgeRoutesRunsByRuntimeSession runs two real ASL runs — one
// inside a tmux runtime session, one inside a Kitty runtime session — and
// checks that each run's lease, supervisor question, notification, and reply
// reach the session that owns it and nothing else, across a daemon restart and
// with no viewer attached anywhere.
func TestInstalledBridgeRoutesRunsByRuntimeSession(t *testing.T) {
	w := newASLWorld(t)
	tmuxPlan := w.newPlan("plan-tmux")
	kittyPlan := w.newPlan("plan-kitty")

	// The tmux session's pane runs the real `asl run`, isolated to the world.
	// The launch hands the pane its runtime session; everything else in its
	// environment is the world's.
	paneShell := filepath.Join(w.root, "bin", "pane-shell")
	paneScript := "#!/bin/sh\nexec env -i " + shellWords(w.env) +
		" " + runtimesession.EnvVar + "=\"$" + runtimesession.EnvVar + "\" " +
		shellWords(w.runArgv(tmuxPlan)) + " > " + shellQuote(filepath.Join(w.logs, "run-plan-tmux.out")) + " 2>&1\n"
	if err := os.WriteFile(paneShell, []byte(paneScript), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", paneShell)

	// --- Daemon lifetime A: no Kitty, no companion, no attached tmux client.
	first := w.startDaemon(nil)

	project := config.Session{Title: "api", Directory: tmuxPlan.repo, Profile: "work"}
	cfg := &config.Config{
		Accounts: []config.Account{{Name: "work", Harness: harness.Claude, Color: "blue"}},
		Sessions: []config.Session{project},
	}
	launched, err := session.LaunchOn(session.Runtime{Tmux: w.tmux.Backend, Registry: w.tmux.Registry}, project, cfg.Accounts[0], cfg)
	if err != nil {
		t.Fatalf("launch the tmux session: %v", err)
	}
	onTmux := launched.RuntimeSessionID
	if onTmux == "" {
		t.Fatal("the tmux launch registered no runtime session")
	}
	panes, err := w.tmux.Backend.Discover()
	if err != nil || len(panes) != 1 {
		t.Fatalf("tmux panes = %+v, err %v; want the one session pane", panes, err)
	}

	var tmuxSlot int
	testutil.Eventually(t, "the tmux run's lifecycle hooks to bind its runtime session", func() bool {
		slot, ok := first.boundSlot(onTmux)
		tmuxSlot = slot
		return ok && len(liveLeases(first.h, slot)) == 1
	}, testutil.Within(60*time.Second), testutil.Every(50*time.Millisecond))
	if !isRuntimeSyntheticSlot(tmuxSlot) {
		t.Fatalf("the tmux run landed on Kitty-range slot %d", tmuxSlot)
	}
	tmuxRun := liveLeases(first.h, tmuxSlot)[0]
	t.Logf("OBSERVED tmux-asl: run %s delivered lifecycle hooks as runtime session %s; lease live on non-Kitty slot (base+%d), daemon has no Kitty client",
		tmuxRun, onTmux, tmuxSlot-runtimeSlotBase)

	// What a dashboard outside Kitty binds the run on: the state query names
	// the runtime session holding the slot and the run leasing it, so the row
	// discovered from tmux and the bridge's run row meet without a window ID.
	query := first.h.Handle(protocol.Request{Type: protocol.TypeStateQuery})
	slotOwner := query.RuntimeSlots[tmuxSlot]
	if !query.OK || slotOwner.RuntimeSessionID != string(onTmux) || slotOwner.Backend != "tmux" {
		t.Fatalf("state query runtime_slots[%d] = %+v (ok %v), want runtime %s on tmux", tmuxSlot, slotOwner, query.OK, onTmux)
	}
	if runs := query.States[tmuxSlot].ASLRunIDs; len(runs) != 1 || runs[0] != tmuxRun {
		t.Fatalf("state query asl_run_ids for the tmux slot = %v, want [%s]", runs, tmuxRun)
	}
	t.Logf("OBSERVED dashboard-binding: state query ties slot base+%d to runtime session %s (backend %s) and to run %s, phases %v — the keys a dashboard with no Kitty client binds a tmux row and its ASL run on",
		tmuxSlot-runtimeSlotBase, slotOwner.RuntimeSessionID, slotOwner.Backend, tmuxRun, query.States[tmuxSlot].ASLPhases)

	// The worker asks; the supervisor in the same session surfaces it.
	tmuxQuestion := w.surfaceQuestion(onTmux, tmuxPlan)
	testutil.Eventually(t, "the tmux session to show the supervisor question", func() bool {
		_, icon, _ := slotState(first.h, tmuxSlot)
		return icon == "question" && len(first.notificationsFor(tmuxSlot)) >= 1
	}, testutil.Within(30*time.Second), testutil.Every(50*time.Millisecond))
	if clients := w.tmuxClients(); clients != "" {
		t.Fatalf("the private tmux server has attached clients:\n%s", clients)
	}
	target := first.h.notificationTarget(tmuxSlot, "")
	if !target.Navigable || target.KittyListenOn != "" || target.RuntimeSessionID != string(onTmux) || target.Backend != "tmux" {
		t.Fatalf("tmux notification target = %+v", target)
	}
	t.Logf("OBSERVED viewer-absent: question %s raised the question icon and %d notification on the tmux slot with no Kitty client, no companion app, and zero clients attached to the private tmux server (list-clients empty); target runtime_session=%s backend=%s navigable=%v",
		tmuxQuestion, len(first.notificationsFor(tmuxSlot)), target.RuntimeSessionID, target.Backend, target.Navigable)

	// Surfacing the same open set again, and maintenance polling the run's
	// real lease projection, are the same question: still one notification.
	if again := w.surfaceQuestion(onTmux, tmuxPlan); again != tmuxQuestion {
		t.Fatalf("resurfaced question = %s; want %s", again, tmuxQuestion)
	}
	now := time.Now()
	for i := range 3 {
		first.h.maintenanceTick(now.Add(time.Duration(i+1) * 11 * time.Second))
	}
	time.Sleep(500 * time.Millisecond)
	if got := first.notificationsFor(tmuxSlot); len(got) != 1 {
		t.Fatalf("notifications for the tmux slot after a resurfaced question and three maintenance sweeps = %+v; want exactly 1", got)
	}
	if got := len(first.calls.snapshot()); got != 1 {
		t.Fatalf("daemon A dispatched %d notifications in total; want 1", got)
	}
	first.h.stateMu.Lock()
	_, supervised := first.h.aslSupervisors.Target(asl.LeaseRef{WindowID: tmuxSlot, Key: tmuxRun})
	first.h.stateMu.Unlock()
	if !supervised || len(liveLeases(first.h, tmuxSlot)) != 1 {
		t.Fatalf("after real discovery sweeps: supervised %v, leases %v", supervised, liveLeases(first.h, tmuxSlot))
	}
	t.Logf("OBSERVED notification-deduplication (same daemon): question resurfaced by a second `asl outbox wait` plus three reconcile/refresh sweeps over the real state root left exactly 1 notification")

	// A stale action token is refused by the installed bridge, and the
	// refusal changes nothing the daemon holds for the run.
	manager := aslbridge.New(aslbridge.Options{
		Mode:     aslbridge.ModeRequired,
		Command:  w.asl,
		Launcher: w.bridgeLauncher(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	bridgeCtx, cancelBridge := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancelBridge()
	manager.Start(bridgeCtx)
	defer manager.Close()
	testutil.Eventually(t, "the installed bridge to accept mutations", func() bool {
		return manager.Status().MutationsEnabled
	}, testutil.Within(60*time.Second), testutil.Every(50*time.Millisecond))
	first.h.SetASLBridge(manager)
	var row aslbridge.Row
	testutil.Eventually(t, "the bridge to project the tmux run", func() bool {
		snapshot, _, err := manager.Snapshot()
		if err != nil {
			return false
		}
		for _, candidate := range snapshot.Rows {
			if candidate.RunID == tmuxRun {
				row = candidate
				return true
			}
		}
		return false
	}, testutil.Within(30*time.Second), testutil.Every(50*time.Millisecond))
	if row.ActionToken == "" {
		t.Fatalf("bridge row for the tmux run carries no action token: %+v", row)
	}
	stale := first.h.Handle(protocol.Request{Type: protocol.TypeASLAction, ASL: &protocol.ASLRequest{
		Method:      aslbridge.MethodRunStopAfterCurrent,
		PlanDir:     row.PlanDir,
		RunID:       row.RunID,
		OperationID: "amx-s10-stale",
		ActionToken: "0000000000000000000000000000dead",
	}})
	if stale.OK || !strings.Contains(stale.Error, aslbridge.CodeStaleActionToken) {
		t.Fatalf("stale action token response = %+v; want a %s refusal", stale, aslbridge.CodeStaleActionToken)
	}
	for _, receipt := range manager.Receipts() {
		if receipt.OperationID == "amx-s10-stale" {
			t.Fatalf("a refused mutation left a receipt: %+v", receipt)
		}
	}
	if _, icon, _ := slotState(first.h, tmuxSlot); icon != "question" || len(liveLeases(first.h, tmuxSlot)) != 1 {
		t.Fatalf("a refused mutation changed the run's state: icon %q leases %v", icon, liveLeases(first.h, tmuxSlot))
	}
	t.Logf("OBSERVED stale-token-refusal: run.stop_after_current for run %s with a stale action token was refused through the daemon with %q (row state %s); no receipt recorded, question and lease unchanged",
		tmuxRun, stale.Error, row.PrimaryState)
	first.h.SetASLBridge(nil)
	manager.Close()

	// --- Daemon restart, while the run is still blocked on its question.
	first.stop()
	projectKitty := kittyPlan.repo
	kittyWindows := []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{
		// Managed tabs sharing each run's directory and harness: what
		// workspace matching would have picked.
		{ID: 10, Title: palette.BuildTitleParts(palette.TitleParts{Base: "tmux-decoy"}), Windows: []kitty.Window{
			{ID: 41, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXDirectory: tmuxPlan.repo}},
		}},
		{ID: 11, Title: palette.BuildTitleParts(palette.TitleParts{Base: "kitty-decoy"}), Windows: []kitty.Window{
			{ID: 42, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXDirectory: projectKitty}},
		}},
		{ID: 12, Title: palette.BuildTitleParts(palette.TitleParts{Base: "kitty-session"}), Windows: []kitty.Window{
			{ID: 43, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXDirectory: projectKitty}},
		}},
	}}}
	second := w.startDaemon(kittyWindows)
	restoredSlot, bound := second.boundSlot(onTmux)
	if !bound || !isRuntimeSyntheticSlot(restoredSlot) {
		t.Fatalf("after restart the tmux session is bound to %d (bound %v)", restoredSlot, bound)
	}
	if leases := liveLeases(second.h, restoredSlot); len(leases) != 1 || leases[0] != tmuxRun {
		t.Fatalf("after restart the tmux slot holds leases %v; want [%s]", leases, tmuxRun)
	}
	_, restoredIcon, _ := slotState(second.h, restoredSlot)

	// The still-open question is surfaced again to the new daemon. It is the
	// question the operator was already told about.
	if again := w.surfaceQuestion(onTmux, tmuxPlan); again != tmuxQuestion {
		t.Fatalf("question after restart = %s; want %s", again, tmuxQuestion)
	}
	second.h.maintenanceTick(time.Now().Add(11 * time.Second))
	testutil.Eventually(t, "the restarted daemon to show the still-open question", func() bool {
		_, icon, _ := slotState(second.h, restoredSlot)
		return icon == "question"
	}, testutil.Within(30*time.Second), testutil.Every(50*time.Millisecond))
	time.Sleep(500 * time.Millisecond)
	afterRestart := len(second.notificationsFor(restoredSlot))
	if afterRestart != 0 {
		t.Fatalf("the restarted daemon re-notified %d time(s) for a question announced before the restart", afterRestart)
	}
	t.Logf("OBSERVED daemon-restart: new daemon restored runtime session %s on a non-Kitty slot with run %s's lease live (icon after restore %q); the question resurfaced by `asl outbox wait` shows the question icon again with %d new notifications",
		onTmux, tmuxRun, restoredIcon, afterRestart)

	// --- A second run, inside a Kitty runtime session, on the new daemon.
	var onKitty runtimesession.ID
	if err := w.tmux.Registry.Update(func(registry *runtimesession.Registry) error {
		record, err := registry.Begin(runtimesession.BeginRequest{
			Project: "api-kitty", LaunchID: "s10-kitty", Relation: runtimesession.RelationFresh,
		}, time.Now())
		if err != nil {
			return err
		}
		onKitty = record.ID
		_, err = registry.Commit(record.ID, runtimesession.Target{
			Instance: runtimesession.BackendInstance{Kind: runtimesession.BackendKitty, Address: "unix:/tmp/amx-s10-kitty", Generation: "kitty-s10-1"},
			Ref:      "43",
		}, runtimesession.ProcessFingerprint{PID: os.Getpid(), StartToken: "s10-test-process"}, time.Now())
		return err
	}); err != nil {
		t.Fatalf("register the Kitty runtime session: %v", err)
	}
	runLog, err := os.Create(filepath.Join(w.logs, "run-plan-kitty.out"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runLog.Close() }()
	kittyArgv := w.runArgv(kittyPlan)
	kittyRunCmd := exec.Command(kittyArgv[0], kittyArgv[1:]...)
	kittyRunCmd.Env = w.sessionEnv(onKitty)
	kittyRunCmd.Dir = kittyPlan.repo
	kittyRunCmd.Stdout = runLog
	kittyRunCmd.Stderr = runLog
	if err := kittyRunCmd.Start(); err != nil {
		t.Fatalf("start the Kitty session's run: %v", err)
	}
	kittyExited := make(chan error, 1)
	go func() { kittyExited <- kittyRunCmd.Wait() }()
	t.Cleanup(func() {
		_ = kittyRunCmd.Process.Kill()
	})

	testutil.Eventually(t, "the Kitty run's lifecycle hooks to reach window 43", func() bool {
		return len(liveLeases(second.h, 43)) == 1
	}, testutil.Within(60*time.Second), testutil.Every(50*time.Millisecond))
	kittyRun := liveLeases(second.h, 43)[0]
	if slot, ok := second.boundSlot(onKitty); !ok || slot != 43 {
		t.Fatalf("the Kitty runtime session is bound to %d (bound %v); want window 43", slot, ok)
	}
	kittyQuestion := w.surfaceQuestion(onKitty, kittyPlan)
	testutil.Eventually(t, "window 43 to show the Kitty run's question", func() bool {
		_, icon, _ := slotState(second.h, 43)
		return icon == "question" && len(second.notificationsFor(43)) >= 1
	}, testutil.Within(30*time.Second), testutil.Every(50*time.Millisecond))
	second.h.maintenanceTick(time.Now().Add(22 * time.Second))
	time.Sleep(500 * time.Millisecond)
	kittyTarget := second.h.notificationTarget(43, "work")
	if !kittyTarget.Navigable || kittyTarget.KittyListenOn != "unix:/tmp/amx-s10-kitty" || kittyTarget.RuntimeSessionID != string(onKitty) {
		t.Fatalf("Kitty notification target = %+v", kittyTarget)
	}
	if got := second.notificationsFor(43); len(got) != 1 || got[0].title != "kitty-session" {
		t.Fatalf("notifications for window 43 = %+v; want exactly one titled kitty-session", got)
	}
	t.Logf("OBSERVED kitty-asl: run %s delivered lifecycle hooks as Kitty runtime session %s; lease and question %s on its registered window 43, 1 notification, click target socket %s",
		kittyRun, onKitty, kittyQuestion, kittyTarget.KittyListenOn)

	// Nothing crossed: with both runs live and reconciled against the real
	// state root, each lease sits only on its own session, and the managed
	// Kitty tabs sharing the runs' directories hold nothing.
	second.h.maintenanceTick(time.Now().Add(33 * time.Second))
	for _, decoy := range []int{41, 42} {
		if leases := liveLeases(second.h, decoy); len(leases) != 0 {
			t.Fatalf("decoy Kitty window %d holds leases %v", decoy, leases)
		}
		if _, icon, _ := slotState(second.h, decoy); icon != "" {
			t.Fatalf("decoy Kitty window %d shows icon %q", decoy, icon)
		}
		if got := second.notificationsFor(decoy); len(got) != 0 {
			t.Fatalf("decoy Kitty window %d was notified: %+v", decoy, got)
		}
	}
	if leases := liveLeases(second.h, restoredSlot); len(leases) != 1 || leases[0] != tmuxRun {
		t.Fatalf("tmux slot leases = %v; want only %s", leases, tmuxRun)
	}
	if leases := liveLeases(second.h, 43); len(leases) != 1 || leases[0] != kittyRun {
		t.Fatalf("window 43 leases = %v; want only %s", leases, kittyRun)
	}
	if total := len(second.calls.snapshot()); total != 1 {
		t.Fatalf("daemon B dispatched %d notifications in total; want 1 (the Kitty question)", total)
	}
	t.Logf("OBSERVED notification-deduplication (across sessions and restart): daemon A dispatched 1 notification (tmux question), daemon B dispatched 1 (Kitty question); decoy Kitty windows 41 and 42 sharing the runs' directories hold no lease, icon, or notification")

	// --- Replies route by run: each clears only its own session's question.
	w.supervise(onTmux, "inbox", "reply", tmuxPlan.dir, "--to", tmuxQuestion, "--text", "Go ahead.", "--state-dir", w.stateRoot, "--json")
	testutil.Eventually(t, "the tmux session's question to clear after its reply", func() bool {
		_, icon, _ := slotState(second.h, restoredSlot)
		return icon != "question"
	}, testutil.Within(30*time.Second), testutil.Every(50*time.Millisecond))
	if _, icon, _ := slotState(second.h, 43); icon != "question" {
		t.Fatalf("replying to the tmux run cleared the Kitty session's question (icon %q)", icon)
	}
	w.supervise(onKitty, "inbox", "reply", kittyPlan.dir, "--to", kittyQuestion, "--text", "Go ahead.", "--state-dir", w.stateRoot, "--json")
	testutil.Eventually(t, "the Kitty session's question to clear after its reply", func() bool {
		_, icon, _ := slotState(second.h, 43)
		return icon != "question"
	}, testutil.Within(30*time.Second), testutil.Every(50*time.Millisecond))

	// Both workers finish their sessions; each run's end releases its lease.
	select {
	case <-kittyExited:
	case <-time.After(120 * time.Second):
		t.Fatalf("the Kitty session's run did not exit; log:\n%s", w.readLog("run-plan-kitty.out"))
	}
	testutil.Eventually(t, "the Kitty run's lease to be released", func() bool {
		return len(liveLeases(second.h, 43)) == 0
	}, testutil.Within(30*time.Second), testutil.Every(50*time.Millisecond))
	testutil.Eventually(t, "the tmux run's lease to be released", func() bool {
		return len(liveLeases(second.h, restoredSlot)) == 0
	}, testutil.Within(120*time.Second), testutil.Every(50*time.Millisecond))
	t.Logf("OBSERVED misrouting check: replies cleared each session's question independently; both runs ended and released their leases on their own slots (tmux harness log: %q; kitty harness log: %q)",
		strings.TrimSpace(w.readLog("harness-plan-tmux.log")), strings.TrimSpace(w.readLog("harness-plan-kitty.log")))
}

func shellQuote(word string) string {
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

func shellWords(words []string) string {
	quoted := make([]string, len(words))
	for i, word := range words {
		quoted[i] = shellQuote(word)
	}
	return strings.Join(quoted, " ")
}
