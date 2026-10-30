package daemon

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// These scenarios inject the faults a tmux-hosted agent has to live through,
// or be correctly reported after, against a real private tmux server, a real
// registry, and real terminals on pseudo-terminals. Where survival is
// promised (the daemon restarts, a viewer dies, ownership moves) the agent's
// process must be the very same one afterwards: same PID, same start time.
// Where it is not (the agent exits, the server dies) the session must be
// retired as what it is, exited or lost, and nothing may be started in its
// place.

// runtimeFaultWorld is one private tmux server, its registry, and the
// checkpoint file a daemon lifetime leaves for the next.
type runtimeFaultWorld struct {
	t          *testing.T
	tmux       *tmuxtest.Fixture
	checkpoint string
}

func newRuntimeFaultWorld(t *testing.T) *runtimeFaultWorld {
	t.Helper()
	return &runtimeFaultWorld{t: t, tmux: tmuxtest.New(t), checkpoint: filepath.Join(t.TempDir(), runtimeCheckpointFileName)}
}

// startDaemon is one daemon lifetime's runtime-session startup, in the order
// Run performs it: reconcile the registry, then restore the checkpoint.
func (w *runtimeFaultWorld) startDaemon(now time.Time) (*Handler, runtimeRestoreReport) {
	w.t.Helper()
	h := newTestHandler()
	h.SetStopConfirm(0, 0, false)
	reader := runtimesession.NewReader(w.tmux.Registry)
	h.SetRuntimeSessionDirectory(reader)
	h.SetRuntimeSessionReconciler(NewTmuxRuntimeReconciler(w.tmux.Registry, reader))
	h.SetRuntimeCheckpointPath(w.checkpoint)
	h.reconcileRuntimeSessions(now)
	return h, h.restoreRuntimeCheckpoint(now)
}

// faultAgent is one launched stand-in agent and the process it is.
type faultAgent struct {
	id       runtimesession.ID
	launched session.LaunchResult
	instance runtimesession.BackendInstance
	process  runtimesession.ProcessFingerprint
}

func (w *runtimeFaultWorld) launch(title string) faultAgent {
	w.t.Helper()
	project := config.Session{Title: title, Directory: w.t.TempDir(), Profile: "work"}
	cfg := &config.Config{Accounts: []config.Account{{Name: "work", Harness: harness.Claude}}, Sessions: []config.Session{project}}
	launched, err := session.LaunchOn(session.Runtime{Tmux: w.tmux.Backend, Registry: w.tmux.Registry}, project, cfg.Accounts[0], cfg)
	if err != nil {
		w.t.Fatalf("launch %s: %v", title, err)
	}
	record := w.record(launched.RuntimeSessionID)
	if record.State != runtimesession.StateLive || record.Process == nil {
		w.t.Fatalf("launched record = %+v, want live", record)
	}
	return faultAgent{id: launched.RuntimeSessionID, launched: launched, instance: record.Target.Instance, process: *record.Process}
}

func (w *runtimeFaultWorld) record(id runtimesession.ID) runtimesession.Record {
	w.t.Helper()
	record, err := w.tmux.Load(w.t).Get(id)
	if err != nil {
		w.t.Fatal(err)
	}
	return record
}

func (w *runtimeFaultWorld) survey() tmux.Survey {
	w.t.Helper()
	s, err := w.tmux.Backend.Survey()
	if err != nil {
		w.t.Fatal(err)
	}
	return s
}

func (w *runtimeFaultWorld) eventually(what string, ok func(tmux.Survey) bool) tmux.Survey {
	w.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := w.survey()
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			w.t.Fatalf("timed out waiting for %s: panes=%+v clients=%+v sizes=%+v", what, s.Panes, s.Clients, s.Sizes)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// attach puts a real terminal of the given size on the agent, the way
// `amx attach` does, and waits until it owns the session.
func (w *runtimeFaultWorld) attach(agent faultAgent, cols, rows int) (*tmuxtest.Viewer, tmux.Client) {
	w.t.Helper()
	client, err := w.tmux.Backend.AttachCommand(agent.launched.Pane, tmux.AttachView{})
	if err != nil {
		w.t.Fatal(err)
	}
	viewer := tmuxtest.StartViewer(w.t, client, cols, rows)
	s := w.eventually("the terminal to own the session", func(s tmux.Survey) bool {
		return len(s.ClientsOf(agent.launched.Pane)) == 1 && s.Sizes[agent.launched.Pane.ID] == tmux.Size{Cols: cols, Rows: rows}
	})
	return viewer, s.ClientsOf(agent.launched.Pane)[0]
}

// requireSameAgent fails unless the agent is the process it was launched as:
// the registry still names it live at the same target, the server still
// holds it in the same pane under the same generation, and the operating
// system reports the same start time for its PID.
func (w *runtimeFaultWorld) requireSameAgent(agent faultAgent, after string) {
	w.t.Helper()
	record := w.record(agent.id)
	if record.State != runtimesession.StateLive || record.Process == nil || *record.Process != agent.process {
		w.t.Fatalf("after %s the registry has %s %+v (%q), want live %+v", after, record.State, record.Process, record.StaleReason, agent.process)
	}
	s := w.survey()
	if s.Instance != agent.instance {
		w.t.Fatalf("after %s the server is %+v, want %+v", after, s.Instance, agent.instance)
	}
	pane, ok := backend.FindPane(s.Panes, agent.launched.Pane)
	if !ok || pane.Exit != nil || len(pane.Foreground) != 1 || pane.Foreground[0].PID != agent.process.PID {
		w.t.Fatalf("after %s the pane is %+v, want pid %d running", after, pane, agent.process.PID)
	}
	now, err := session.ProcessFingerprint(agent.process.PID)
	if err != nil || now != agent.process {
		w.t.Fatalf("after %s the process is %+v (%v), want %+v", after, now, err, agent.process)
	}
}

func (w *runtimeFaultWorld) own(agent faultAgent, owner string, at time.Time) {
	w.t.Helper()
	if err := w.tmux.Registry.Update(func(registry *runtimesession.Registry) error {
		_, err := registry.Attach(agent.id, runtimesession.Attachment{ID: owner, Client: "test terminal"}, at)
		return err
	}); err != nil {
		w.t.Fatal(err)
	}
}

func listedInSnapshot(h *Handler, id runtimesession.ID) bool {
	snap := h.buildCompanionSnapshots(time.Now())[protocol.CompanionSnapshotVersionRuntime]
	return slices.ContainsFunc(snap.Sessions, func(s protocol.CompanionSession) bool {
		return s.Runtime != nil && s.Runtime.SessionID == string(id)
	})
}

// A daemon that crashes and starts again finds the same agent, still owned by
// the same terminal at the same size, and lists it at once.
func TestFaultDaemonRestartLeavesTmuxAgentAndItsOwnerUnchanged(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	agent := w.launch("api")
	_, owner := w.attach(agent, 103, 33)
	w.own(agent, "view-1", time.Now().Add(-time.Hour))

	start := time.Now()
	first, _ := w.startDaemon(start)
	requireAccepted(t, first.Handle(runtimeHook(t, agent.id, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))
	first.maintainRuntimeSessions(start.Add(time.Minute))
	if !listedInSnapshot(first, agent.id) {
		t.Fatal("the session is not listed before the restart")
	}
	icon := slotIcon(first, slotOf(t, first, agent.id))

	// The daemon dies here: nothing is flushed, nothing says goodbye.
	second, report := w.startDaemon(start.Add(2 * time.Minute))
	if report.Corrupt || report.Outcomes[string(agent.id)] != runtimeRestoreRestored {
		t.Fatalf("restore report = %+v, want the session restored", report)
	}
	if !listedInSnapshot(second, agent.id) {
		t.Fatal("the restarted daemon does not list the session")
	}
	if got := slotIcon(second, slotOf(t, second, agent.id)); got != icon || got == "" {
		t.Fatalf("restored icon = %q, want %q", got, icon)
	}
	second.maintainRuntimeSessions(start.Add(3 * time.Minute))
	w.requireSameAgent(agent, "a daemon restart")
	if a := w.record(agent.id).Attachment; a == nil || a.ID != "view-1" {
		t.Fatalf("ownership after the restart = %+v, want view-1 kept", a)
	}
	after := w.survey()
	clients := after.ClientsOf(agent.launched.Pane)
	if len(clients) != 1 || clients[0].Name != owner.Name || clients[0].PID != owner.PID {
		t.Fatalf("terminals after the restart = %+v, want only %+v", clients, owner)
	}
	if got := after.Sizes[agent.launched.Pane.ID]; got != (tmux.Size{Cols: 103, Rows: 33}) {
		t.Fatalf("size after the restart = %+v, want 103x33", got)
	}
}

// A viewer that is killed (the app crashed) takes nothing with it: the agent
// runs on at the size it had, and the ownership the viewer could not release
// is released for it.
func TestFaultViewerCrashLeavesAgentRunningAtItsSize(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	agent := w.launch("api")
	start := time.Now()
	h, _ := w.startDaemon(start)
	requireAccepted(t, h.Handle(runtimeHook(t, agent.id, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))

	_, owner := w.attach(agent, 111, 37)
	w.own(agent, "crashed-view", start)
	if err := syscall.Kill(owner.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	w.eventually("the dead terminal to go", func(s tmux.Survey) bool { return len(s.Clients) == 0 })

	// Too soon to tell a crashed owner from one still connecting.
	h.maintainRuntimeSessions(start.Add(runtimeReconcileInterval))
	if a := w.record(agent.id).Attachment; a == nil {
		t.Fatal("ownership was released before the grace passed")
	}
	h.maintainRuntimeSessions(start.Add(time.Minute))
	if a := w.record(agent.id).Attachment; a != nil {
		t.Fatalf("ownership of a dead terminal was kept: %+v", a)
	}
	w.requireSameAgent(agent, "its viewer was killed")
	if got := w.survey().Sizes[agent.launched.Pane.ID]; got != (tmux.Size{Cols: 111, Rows: 37}) {
		t.Fatalf("size after the viewer died = %+v, want its last size 111x37", got)
	}
	if !listedInSnapshot(h, agent.id) {
		t.Fatal("the session stopped being listed when its viewer died")
	}

	// The next terminal picks the same agent up and sets its own size.
	w.attach(agent, 84, 21)
	w.requireSameAgent(agent, "a new terminal attached")
}

// Ownership moves to whoever attaches last. There is never a second terminal
// on a session, and reading its screen is not a terminal.
func TestFaultOwnershipTransferLeavesOneOwnerAndTheSameAgent(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	agent := w.launch("api")
	start := time.Now()
	h, _ := w.startDaemon(start)
	requireAccepted(t, h.Handle(runtimeHook(t, agent.id, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))

	first, a := w.attach(agent, 100, 30)
	_, b := w.attach(agent, 80, 20)
	if a.Name == b.Name || !first.Exited(5*time.Second) {
		t.Fatalf("the first terminal %+v was not replaced by %+v", a, b)
	}

	// A passive look at the pane, as a preview or a probe takes it.
	for range 3 {
		if _, err := w.tmux.Backend.Capture(agent.launched.Pane, backend.ExtentAll); err != nil {
			t.Fatal(err)
		}
		if screen, err := w.tmux.Backend.Observe(agent.launched.Pane); err != nil || screen.Cols != 80 || screen.Rows != 20 {
			t.Fatalf("observed %+v, %v; want the owner's 80x20", screen, err)
		}
	}
	h.maintainRuntimeSessions(start.Add(time.Minute))
	s := w.survey()
	if clients := s.ClientsOf(agent.launched.Pane); len(clients) != 1 || clients[0].Name != b.Name {
		t.Fatalf("terminals = %+v, want only the last to attach %+v", clients, b)
	}
	if got := s.Sizes[agent.launched.Pane.ID]; got != (tmux.Size{Cols: 80, Rows: 20}) {
		t.Fatalf("size = %+v, want the owner's 80x20", got)
	}
	w.requireSameAgent(agent, "ownership moved")

	// A terminal that attaches without taking over (a hand-typed tmux attach)
	// would share the keyboard and the size. Maintenance leaves one owner.
	time.Sleep(1100 * time.Millisecond)
	raw := exec.Command(w.tmux.Server.Binary(), "-S", w.tmux.Server.Socket(), "-f", w.tmux.Server.ConfigPath(), "attach-session", "-t", agent.launched.Pane.ID)
	raw.Env = slices.DeleteFunc(raw.Environ(), func(kv string) bool { return len(kv) > 5 && kv[:5] == "TMUX=" })
	tmuxtest.StartViewer(t, raw, 130, 44)
	w.eventually("two terminals on one session", func(s tmux.Survey) bool { return len(s.ClientsOf(agent.launched.Pane)) == 2 })
	h.maintainRuntimeSessions(start.Add(2 * time.Minute))
	w.eventually("one owner at its own size", func(s tmux.Survey) bool {
		clients := s.ClientsOf(agent.launched.Pane)
		return len(clients) == 1 && clients[0].Name != b.Name && s.Sizes[agent.launched.Pane.ID] == tmux.Size{Cols: 130, Rows: 44}
	})
	w.requireSameAgent(agent, "a competing terminal was detached")
}

// An agent whose process ends is retired as exited, with how it ended; the
// server keeps its pane and nothing is started in its place.
func TestFaultAgentExitIsRetiredAsExitedAndNotRelaunched(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	agent := w.launch("api")
	other := w.launch("web")
	start := time.Now()
	h, _ := w.startDaemon(start)
	for _, a := range []faultAgent{agent, other} {
		requireAccepted(t, h.Handle(runtimeHook(t, a.id, "PermissionRequest", hookevent.HookPayload{SessionID: "conv-" + string(a.id)})))
	}
	registered := len(w.tmux.Load(t).List())

	if err := syscall.Kill(agent.process.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	var exitSignal string
	w.eventually("the agent's pane to show its exit", func(s tmux.Survey) bool {
		pane, ok := backend.FindPane(s.Panes, agent.launched.Pane)
		if ok && pane.Exit != nil {
			exitSignal = pane.Exit.Signal
			return true
		}
		return false
	})
	if exitSignal == "" {
		t.Fatal("the agent's pane did not report its exit signal")
	}
	h.maintainRuntimeSessions(start.Add(time.Minute))

	// Preserve the backend's exit detail, whether tmux uses a name or number.
	wantReason := "agent exited (signal " + exitSignal + ")"
	record := w.record(agent.id)
	if record.State != runtimesession.StateStale || record.Outcome() != runtimesession.OutcomeExited || record.StaleReason != wantReason {
		t.Fatalf("record = %s %q (%s), want stale: %s", record.State, record.StaleReason, record.Outcome(), wantReason)
	}
	if listedInSnapshot(h, agent.id) {
		t.Fatal("an exited agent is still listed as a live session")
	}
	if _, bound := boundSlot(h, agent.id); bound {
		t.Fatal("an exited agent still holds a slot")
	}
	requireRefused(t, h.Handle(runtimeHook(t, agent.id, "PermissionRequest", hookevent.HookPayload{SessionID: "late"})),
		protocol.OriginReasonRuntimeSessionEnded, false)

	// Later passes, a restart, and a late hook start nothing.
	h.maintainRuntimeSessions(start.Add(2 * time.Minute))
	restarted, report := w.startDaemon(start.Add(3 * time.Minute))
	if outcome := report.Outcomes[string(agent.id)]; outcome == runtimeRestoreRestored || outcome == runtimeRestoreMerged {
		t.Fatalf("the restarted daemon restored an exited agent: %+v", report)
	}
	if listedInSnapshot(restarted, agent.id) || !listedInSnapshot(restarted, other.id) {
		t.Fatal("the restarted daemon lists the exited agent, or lost the running one")
	}
	s := w.survey()
	if len(s.Panes) != 2 || len(w.tmux.Load(t).List()) != registered {
		t.Fatalf("after an agent exited there are %d panes and %d registrations, want 2 and %d: nothing relaunched", len(s.Panes), len(w.tmux.Load(t).List()), registered)
	}
	if pane, _ := backend.FindPane(s.Panes, agent.launched.Pane); pane.Exit == nil {
		t.Fatalf("the exited agent's pane = %+v, want it retained as exited", pane)
	}
	w.requireSameAgent(other, "another agent exited")
}

// When the server dies, by itself or while no daemon runs, its agents are
// retired as lost: what became of them was not seen. No server is started
// and no agent is launched to stand in for them.
func TestFaultTmuxServerLossIsRetiredAsLostAndNothingIsRelaunched(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	agent := w.launch("api")
	start := time.Now()
	first, _ := w.startDaemon(start)
	requireAccepted(t, first.Handle(runtimeHook(t, agent.id, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))
	first.maintainRuntimeSessions(start.Add(time.Minute))
	registered := len(w.tmux.Load(t).List())

	// The server is killed outright while the daemon is down.
	pid, _, _ := strings.Cut(agent.instance.Generation, "-")
	serverPID, err := strconv.Atoi(pid)
	if err != nil || serverPID <= 1 {
		t.Fatalf("server generation %q names no PID", agent.instance.Generation)
	}
	if err := syscall.Kill(serverPID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill tmux server %d: %v", serverPID, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for w.survey().Running {
		if time.Now().After(deadline) {
			t.Fatal("the killed server still answers")
		}
		time.Sleep(25 * time.Millisecond)
	}

	second, report := w.startDaemon(start.Add(2 * time.Minute))
	if outcome := report.Outcomes[string(agent.id)]; outcome != runtimeRestoreStale {
		t.Fatalf("restore outcome = %q, want stale: %+v", outcome, report)
	}
	record := w.record(agent.id)
	if record.State != runtimesession.StateStale || record.Outcome() != runtimesession.OutcomeLost {
		t.Fatalf("record = %s %q (%s), want stale and lost", record.State, record.StaleReason, record.Outcome())
	}
	if listedInSnapshot(second, agent.id) {
		t.Fatal("a lost agent is listed as a live session")
	}
	requireRefused(t, second.Handle(runtimeHook(t, agent.id, "PermissionRequest", hookevent.HookPayload{SessionID: "late"})),
		protocol.OriginReasonRuntimeSessionEnded, false)
	second.maintainRuntimeSessions(start.Add(3 * time.Minute))
	if s := w.survey(); s.Running || len(s.Panes) != 0 {
		t.Fatalf("after its server was lost a server is running: %+v", s)
	}
	if got := len(w.tmux.Load(t).List()); got != registered {
		t.Fatalf("registrations = %d, want %d: nothing relaunched", got, registered)
	}

	// A server started later on the same socket reuses pane IDs. The lost
	// session is not revived by it.
	successor := w.launch("api")
	second.maintainRuntimeSessions(start.Add(4 * time.Minute))
	if successor.launched.Pane != agent.launched.Pane {
		t.Logf("the new server did not reuse pane %s (got %s)", agent.launched.Pane.ID, successor.launched.Pane.ID)
	}
	if got := w.record(agent.id); got.State != runtimesession.StateStale {
		t.Fatalf("the lost session came back as %s", got.State)
	}
	w.requireSameAgent(successor, "a lost predecessor")
}
