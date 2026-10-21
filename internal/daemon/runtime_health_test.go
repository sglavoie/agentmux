package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// What health says about the daemon's runtime-session work, against a real
// private tmux server: the counts are the registry's, the passes are the ones
// that ran, a journal record is written for an edge and not for a pass, and
// nothing in either names a path, a project, or anything a pane held.

type journalRecord struct {
	stage, outcome, reason string
	evidence               map[string]string
}

type journalRecords struct {
	mu      sync.Mutex
	records []journalRecord
}

func (j *journalRecords) record(stage, outcome, reason string, evidence map[string]string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	copied := make(map[string]string, len(evidence))
	for key, value := range evidence {
		copied[key] = value
	}
	j.records = append(j.records, journalRecord{stage, outcome, reason, copied})
}

func (j *journalRecords) reasons() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]string, len(j.records))
	for i, record := range j.records {
		out[i] = record.reason
	}
	return out
}

func runtimeHealthOf(t *testing.T, h *Handler) *protocol.RuntimeHealth {
	t.Helper()
	resp := h.handleHealth()
	if resp.Runtime == nil {
		t.Fatal("health carries no runtime block")
	}
	return resp.Runtime
}

func TestRuntimeHealthReportsSessionsAttachmentAndReconciliation(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	start := time.Now()
	h, _ := w.startDaemon(start)
	journal := &journalRecords{}
	h.SetRuntimeJournal(journal.record)

	health := runtimeHealthOf(t, h)
	if !health.Reconcile.Enabled || health.Reconcile.Passes != 1 || health.Reconcile.IntervalSeconds != runtimeReconcileInterval.Seconds() {
		t.Fatalf("reconcile after startup = %+v; want one pass of an enabled reconciler", health.Reconcile)
	}
	if !health.Sessions.Known || health.Sessions.Live != 0 {
		t.Fatalf("sessions before any launch = %+v; want an empty registry that was read", health.Sessions)
	}

	watched := w.launch("alpha-project")
	w.launch("beta-project")
	doomed := w.launch("gamma-project")
	viewer, _ := w.attach(watched, 100, 30)
	defer viewer.Close()
	w.own(watched, "owner-1", time.Now())

	h.maintainRuntimeSessions(start.Add(runtimeReconcileInterval))
	health = runtimeHealthOf(t, h)
	want := protocol.RuntimeSessionHealth{Known: true, Live: 3, Tmux: 3, Servers: 1, Attached: 1, Unattached: 2}
	if health.Sessions != want {
		t.Fatalf("sessions = %+v; want %+v", health.Sessions, want)
	}
	if health.Reconcile.Passes != 2 || health.Reconcile.Failures != 0 || health.Reconcile.AgeSeconds == nil ||
		*health.Reconcile.AgeSeconds > 5 || health.Reconcile.LastDurationMs <= 0 || health.Reconcile.FailureClass != "" {
		t.Fatalf("reconcile = %+v; want two clean passes, the last just now", health.Reconcile)
	}
	if got := journal.reasons(); len(got) != 0 {
		t.Fatalf("journal after two passes that changed nothing = %v; want no record", got)
	}

	// An agent that stops is one change: one record, naming it by its
	// runtime-session ID and by nothing else.
	if err := syscall.Kill(doomed.process.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	w.eventually("the killed agent's pane to be dead", func(s tmux.Survey) bool {
		pane, ok := backend.FindPane(s.Panes, doomed.launched.Pane)
		return ok && pane.Exit != nil
	})
	h.maintainRuntimeSessions(start.Add(2 * runtimeReconcileInterval))
	health = runtimeHealthOf(t, h)
	if health.Reconcile.Ended != 1 || health.Sessions.Live != 2 || health.Sessions.Unattached != 1 {
		t.Fatalf("after an agent was killed: reconcile = %+v sessions = %+v", health.Reconcile, health.Sessions)
	}
	if got := journal.reasons(); len(got) != 1 || got[0] != "sessions_changed" {
		t.Fatalf("journal = %v; want one sessions_changed record", got)
	}
	record := journal.records[0]
	if record.stage != runtimeReconcileStage || record.outcome != "success" || record.evidence["ended"] != "1" ||
		!strings.HasPrefix(record.evidence["ended_0"], string(doomed.id)+" ") {
		t.Fatalf("record = %+v; want the ended session by ID", record)
	}

	// Nothing published names where the server lives, what was launched, or
	// which project it was for.
	encoded, err := json.Marshal(health)
	if err != nil {
		t.Fatal(err)
	}
	published := string(encoded) + fmt.Sprint(journal.records)
	for _, private := range []string{w.tmux.Server.Socket(), "alpha-project", "gamma-project", "claude", "/"} {
		if strings.Contains(published, private) {
			t.Errorf("health or journal carries %q:\n%s", private, published)
		}
	}
}

// A server that cannot be read is two journal records however long it lasts:
// the pass that first failed and the pass that recovered.
func TestRuntimeHealthJournalsAFailingStreakOnce(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	journal := &journalRecords{}
	h.SetRuntimeJournal(journal.record)
	failing := true
	h.SetRuntimeSessionReconciler(func(time.Time) (tmux.Report, error) {
		if failing {
			return tmux.Report{}, &tmux.CommandError{
				Args: []string{"list-panes", "-a"}, Stderr: "lost server at /private/tmp/agent-mux-501/tmux.sock",
				Err: errors.New("exit status 1"),
			}
		}
		return tmux.Report{}, nil
	})
	now := time.Now()
	for pass := 0; pass < 5; pass++ {
		// Far enough apart that every one is due, however long the wait
		// between failing passes has grown.
		now = now.Add(runtimeReconcileMaxBackoff)
		h.reconcileRuntimeSessions(now)
	}
	health := runtimeHealthOf(t, h)
	if health.Reconcile.Passes != 5 || health.Reconcile.Failures != 5 || health.Reconcile.ConsecutiveFailures != 5 || health.Reconcile.FailureClass != "command_failed" {
		t.Fatalf("reconcile = %+v; want five failed passes classed command_failed", health.Reconcile)
	}
	if health.Sessions.Known {
		t.Fatalf("sessions = %+v; a daemon without a registry knows none", health.Sessions)
	}

	if got := health.Reconcile.IntervalSeconds; got != runtimeReconcileMaxBackoff.Seconds() {
		t.Fatalf("interval while failing = %vs; want the longest wait, %s", got, runtimeReconcileMaxBackoff)
	}

	failing = false
	now = now.Add(runtimeReconcileMaxBackoff)
	h.reconcileRuntimeSessions(now)
	for pass := 0; pass < 2; pass++ {
		now = now.Add(runtimeReconcileInterval)
		h.reconcileRuntimeSessions(now)
	}
	health = runtimeHealthOf(t, h)
	if health.Reconcile.ConsecutiveFailures != 0 || health.Reconcile.Failures != 5 || health.Reconcile.FailureClass != "" ||
		health.Reconcile.Passes != 8 || health.Reconcile.IntervalSeconds != runtimeReconcileInterval.Seconds() {
		t.Fatalf("reconcile after recovery = %+v; want the two-second cadence back at once", health.Reconcile)
	}
	if got := journal.reasons(); len(got) != 2 || got[0] != "reconcile_failed" || got[1] != "reconcile_recovered" {
		t.Fatalf("journal = %v; want the failure edge and the recovery edge", got)
	}
	failed := journal.records[0]
	if failed.outcome != "error" || failed.evidence["failure_class"] != "command_failed" {
		t.Fatalf("failure record = %+v", failed)
	}
	for key, value := range failed.evidence {
		if strings.ContainsAny(value, "/ ") {
			t.Errorf("failure evidence %s = %q carries more than a count or a class", key, value)
		}
	}
}

// The interval is a bound: however often maintenance asks, the servers are
// read once per interval.
func TestRuntimeReconcileRunsAtMostOncePerInterval(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	passes := 0
	h.SetRuntimeSessionReconciler(func(time.Time) (tmux.Report, error) {
		passes++
		return tmux.Report{}, nil
	})
	start := time.Now()
	for tick := 0; tick < 100; tick++ {
		h.reconcileRuntimeSessions(start.Add(time.Duration(tick) * 100 * time.Millisecond))
	}
	// Ten seconds of ticks at ten a second: a pass every interval less its
	// slack, at 0s, 1.8s, 3.6s, 5.4s, 7.2s, 9s.
	if passes != 6 || runtimeHealthOf(t, h).Reconcile.Passes != 6 {
		t.Fatalf("passes = %d (health says %d); want 6", passes, runtimeHealthOf(t, h).Reconcile.Passes)
	}
}

// The maintenance tick has the period of the interval, and a ticker is never
// exact. A tick that comes a little early must still get its pass, or the
// servers are read every other tick.
func TestRuntimeReconcileRunsOnEveryMaintenanceTick(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	passes := 0
	h.SetRuntimeSessionReconciler(func(time.Time) (tmux.Report, error) {
		passes++
		return tmux.Report{}, nil
	})
	now := time.Now()
	for tick := 0; tick < 20; tick++ {
		// Alternately a little early and a little late, as a ticker is.
		jitter := 3 * time.Millisecond
		if tick%2 == 0 {
			jitter = -jitter
		}
		now = now.Add(runtimeReconcileInterval + jitter)
		h.reconcileRuntimeSessions(now)
	}
	if passes != 20 {
		t.Fatalf("passes over 20 maintenance ticks = %d; want one per tick", passes)
	}
}

type countingObserver struct {
	reads int
	err   error
}

func (o *countingObserver) Observe(pane backend.Ref) (backend.Screen, error) {
	o.reads++
	return backend.Screen{Pane: pane, Text: "a prompt nobody may publish"}, o.err
}

// A probe tick reads each pane once however many probes ask, and health
// counts the reads that reached the backend.
func TestRuntimeHealthCountsScreenReadsNotCacheHits(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	observer := &countingObserver{}
	h.SetScreenObserver(observer)
	pane := backend.Ref{Scope: backend.ScopePane, ID: "%1"}

	screens := h.screenTextCacheFor()
	for probe := 0; probe < 4; probe++ {
		if _, err := screens.Observe(pane); err != nil {
			t.Fatal(err)
		}
	}
	capture := runtimeHealthOf(t, h).Capture
	if observer.reads != 1 || capture.Reads != 1 || capture.Failures != 0 || capture.AgeSeconds == nil {
		t.Fatalf("four probes of one pane: %d backend reads, capture = %+v; want one read", observer.reads, capture)
	}

	observer.err = errors.New("pane closed")
	for tick := 0; tick < 3; tick++ {
		_, _ = h.screenTextCacheFor().Observe(pane)
	}
	capture = runtimeHealthOf(t, h).Capture
	if capture.Reads != 4 || capture.Failures != 3 || capture.ConsecutiveFailures != 3 {
		t.Fatalf("capture after three failed ticks = %+v", capture)
	}
	observer.err = nil
	_, _ = h.screenTextCacheFor().Observe(pane)
	if capture = runtimeHealthOf(t, h).Capture; capture.ConsecutiveFailures != 0 || capture.Reads != 5 {
		t.Fatalf("capture after a good read = %+v", capture)
	}
	if encoded, _ := json.Marshal(runtimeHealthOf(t, h)); strings.Contains(string(encoded), "prompt") {
		t.Fatalf("health carries screen text: %s", encoded)
	}
}

// Health is asked while maintenance reconciles and the probes read screens.
// The tally has to be the same whichever of them gets there first.
func TestHandler_ConcurrentHealthDuringRuntimeMaintenance(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	h, _ := w.startDaemon(time.Now())
	h.SetRuntimeJournal((&journalRecords{}).record)
	agents := []faultAgent{w.launch("one"), w.launch("two"), w.launch("three")}
	h.SetScreenObserver(w.tmux.Backend)

	const passes, reads, asks = 12, 12, 200
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		now := time.Now()
		for pass := 0; pass < passes; pass++ {
			now = now.Add(runtimeReconcileInterval)
			h.maintainRuntimeSessions(now)
		}
	}()
	go func() {
		defer wg.Done()
		// One goroutine, as the probe loop is.
		for tick := 0; tick < reads; tick++ {
			screens := h.screenTextCacheFor()
			for _, agent := range agents {
				if _, err := screens.Observe(agent.launched.Pane); err != nil {
					t.Errorf("observe: %v", err)
				}
			}
		}
	}()
	go func() {
		defer wg.Done()
		var last uint64
		for ask := 0; ask < asks; ask++ {
			health := h.handleHealth().Runtime
			if health.Reconcile.Passes < last || health.TmuxClient.InFlight > health.TmuxClient.Limit {
				t.Errorf("health went backwards or over the limit: %+v", health)
			}
			last = health.Reconcile.Passes
		}
	}()
	wg.Wait()

	health := runtimeHealthOf(t, h)
	if health.Reconcile.Passes != passes+1 || health.Reconcile.Failures != 0 {
		t.Errorf("reconcile = %+v; want %d clean passes", health.Reconcile, passes+1)
	}
	if health.Capture.Reads != reads*uint64(len(agents)) || health.Capture.Failures != 0 {
		t.Errorf("capture = %+v; want %d clean reads", health.Capture, reads*len(agents))
	}
	if health.Sessions.Live != len(agents) || health.TmuxClient.MaxInFlight > tmux.MaxClientCommands {
		t.Errorf("sessions = %+v tmux clients = %+v", health.Sessions, health.TmuxClient)
	}
	for _, agent := range agents {
		w.requireSameAgent(agent, "concurrent maintenance, screen reads, and health")
	}
}

// A server that does not answer costs a pass a whole client timeout, on the
// maintenance loop. Asking it again every two seconds would leave that loop
// little else; the wait doubles instead, to a ceiling.
func TestRuntimeReconcileBacksOffWhileItFails(t *testing.T) {
	t.Parallel()

	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for failures, wait := range want {
		if got := runtimeReconcileWait(uint64(failures)); got != wait {
			t.Errorf("wait after %d consecutive failures = %s; want %s", failures, got, wait)
		}
	}

	h := newTestHandler()
	passes := 0
	h.SetRuntimeSessionReconciler(func(time.Time) (tmux.Report, error) {
		passes++
		return tmux.Report{}, errors.New("server did not answer")
	})
	start := time.Now()
	// Two minutes of two-second ticks against a server that never answers:
	// passes at 0s, 4s, 12s, 28s, 58s, 88s, 118s.
	for tick := 0; tick < 60; tick++ {
		h.reconcileRuntimeSessions(start.Add(time.Duration(tick) * runtimeReconcileInterval))
	}
	if passes != 7 {
		t.Fatalf("passes in two minutes of failure = %d; want 7, not one per tick", passes)
	}
}

// A tmux session has no Kitty tab to write its state to. Its state is durable
// once the runtime checkpoint holds it, which is what ends its dirty stretch:
// it is never queued for the Kitty writer, and doctor's renderer invariant
// (dirty for longer than two intervals) is never tripped by it.
func TestRuntimeSlotsSettleThroughTheCheckpointNotTheKittyWriter(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	agent := w.launch("api")
	start := time.Now()
	h, _ := w.startDaemon(start)
	requireAccepted(t, h.Handle(runtimeHook(t, agent.id, "UserPromptSubmit", hookevent.HookPayload{SessionID: "conv"})))
	slot := slotOf(t, h, agent.id)

	rendererOf := func() protocol.WindowDebugRenderer {
		t.Helper()
		export := h.Handle(protocol.Request{Type: protocol.TypeEventsExport})
		meta, ok := export.WindowMetadata[slot]
		if !ok {
			t.Fatalf("no metadata exported for slot %d", slot)
		}
		return meta.Renderer
	}
	if !h.renderQueue.Empty() {
		t.Fatal("a tmux slot was queued for the Kitty writer")
	}
	before := rendererOf()
	if before.DirtySince == 0 || before.QueuedGen == 0 || before.RenderedGen >= before.QueuedGen {
		t.Fatalf("renderer before the checkpoint = %+v; want an unsettled generation", before)
	}

	h.maintainRuntimeSessions(start.Add(runtimeReconcileInterval))
	after := rendererOf()
	if after.DirtySince != 0 || after.DirtyAgeSeconds != 0 || after.RenderedGen < after.QueuedGen || after.PersistedGen < after.QueuedGen {
		t.Fatalf("renderer after the checkpoint = %+v; want generation %d settled and nothing dirty", after, after.QueuedGen)
	}
	if icon := slotIcon(h, slot); icon == "" {
		t.Fatal("settling lost the slot's state")
	}

	// A checkpoint that cannot be written settles nothing: the state is not
	// durable, and the invariant is right to say so.
	h.SetRuntimeCheckpointPath(filepath.Join(w.checkpoint, "not-a-directory", runtimeCheckpointFileName))
	requireAccepted(t, h.Handle(runtimeHook(t, agent.id, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))
	h.maintainRuntimeSessions(start.Add(2 * runtimeReconcileInterval))
	if unwritten := rendererOf(); unwritten.DirtySince == 0 || unwritten.RenderedGen >= unwritten.QueuedGen {
		t.Fatalf("renderer after a failed checkpoint = %+v; want it still dirty", unwritten)
	}
	h.SetRuntimeCheckpointPath(w.checkpoint)
	h.maintainRuntimeSessions(start.Add(3 * runtimeReconcileInterval))
	if recovered := rendererOf(); recovered.DirtySince != 0 || recovered.RenderedGen < recovered.QueuedGen {
		t.Fatalf("renderer once the checkpoint is written = %+v; want it settled", recovered)
	}
}
