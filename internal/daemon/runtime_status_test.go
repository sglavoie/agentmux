package daemon

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

// statusWrites records what a daemon asked its backends to show.
type statusWrites struct {
	batches [][]RuntimeStatus
	err     error
}

func (w *statusWrites) write(statuses []RuntimeStatus) error {
	w.batches = append(w.batches, append([]RuntimeStatus(nil), statuses...))
	return w.err
}

// take returns the batches written since the last call.
func (w *statusWrites) take() [][]RuntimeStatus {
	batches := w.batches
	w.batches = nil
	return batches
}

func brightBg(t *testing.T, name string) string {
	t.Helper()
	c, err := palette.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return c.BrightBg
}

// TestSettledTmuxStateIsRenderedOnceIntoItsSessionsStatus: the daemon writes
// what a Kitty tab would show (state icon, title, color) for a tmux session
// once the checkpoint holds its state, in one write for the whole pass, and
// writes nothing while nothing a terminal shows has changed. A change of
// state, title, or color is written by the next pass; a Kitty session's tab
// is still Kitty's.
func TestSettledTmuxStateIsRenderedOnceIntoItsSessionsStatus(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	onKitty, api, web := newRuntimeID(t), newRuntimeID(t), newRuntimeID(t)
	directory.put(onKitty, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")
	directory.put(api, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	directory.put(web, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%5")
	h.SetProfileColors(map[string]string{"work": "blue"})
	h.SetRuntimeSessionLabeler(func(record runtimesession.Record) (RuntimeSessionLabel, error) {
		return RuntimeSessionLabel{Project: "proj-" + record.Target.Ref, Profile: "work"}, nil
	})
	writes := &statusWrites{}
	h.SetRuntimeStatusWriter(writes.write)
	for _, runtime := range []runtimesession.ID{onKitty, api, web} {
		requireAccepted(t, h.Handle(runtimeHook(t, runtime, "PermissionRequest", hookevent.HookPayload{SessionID: "conv-" + string(runtime)})))
	}
	rendered := func(runtime runtimesession.ID, base string) string {
		t.Helper()
		state := tmuxSessionIn(t, h, runtime).IconState
		if state == "" {
			t.Fatalf("%s shows no state", runtime)
		}
		return palette.BuildTitleParts(palette.TitleParts{Base: base, State: state})
	}
	instance := runtimesession.BackendInstance{Kind: runtimesession.BackendTmux, Address: tmuxT, Generation: "tmux-t-1"}
	byPane := func(batch []RuntimeStatus) map[string]RuntimeStatus {
		out := make(map[string]RuntimeStatus, len(batch))
		for _, status := range batch {
			out[status.Pane] = status
		}
		return out
	}

	now := time.Now()
	h.maintainRuntimeSessions(now)
	batches := writes.take()
	if len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("first pass wrote %+v, want both tmux sessions in one write and nothing for the Kitty one", batches)
	}
	got := byPane(batches[0])
	for pane, runtime := range map[string]runtimesession.ID{"%4": api, "%5": web} {
		want := RuntimeStatus{Instance: instance, Pane: pane, Text: rendered(runtime, "proj-"+pane), Color: brightBg(t, "blue")}
		if got[pane] != want {
			t.Fatalf("status of %s = %+v, want %+v", pane, got[pane], want)
		}
	}

	h.maintainRuntimeSessions(now.Add(time.Second))
	if batches := writes.take(); len(batches) != 0 {
		t.Fatalf("a pass with nothing new wrote %+v", batches)
	}

	// The title and the color are the pane's, read by the reconciliation pass.
	h.observeRuntimeTitles([]tmux.Titled{{ID: api, Title: palette.StarIcon() + "🦊 api"}})
	h.observeRuntimeColors([]tmux.Colored{{ID: api, Color: "red"}})
	h.maintainRuntimeSessions(now.Add(2 * time.Second))
	batches = writes.take()
	wantText := palette.BuildTitleParts(palette.TitleParts{Base: "🦊 api", Starred: true, State: tmuxSessionIn(t, h, api).IconState})
	if len(batches) != 1 || len(batches[0]) != 1 || batches[0][0] != (RuntimeStatus{Instance: instance, Pane: "%4", Text: wantText, Color: brightBg(t, "red")}) {
		t.Fatalf("after a new title and color: %+v, want only %%4 as %q on red", batches, wantText)
	}

	// A change of state is rendered by the pass that checkpoints it.
	before := tmuxSessionIn(t, h, web).IconState
	requireAccepted(t, h.Handle(runtimeHook(t, web, "UserPromptSubmit", hookevent.HookPayload{SessionID: "conv-" + string(web)})))
	if after := tmuxSessionIn(t, h, web).IconState; after == before {
		t.Fatalf("the prompt left the state at %q", after)
	}
	if batches := writes.take(); len(batches) != 0 {
		t.Fatalf("a hook wrote a status off the maintenance schedule: %+v", batches)
	}
	h.maintainRuntimeSessions(now.Add(3 * time.Second))
	batches = writes.take()
	if len(batches) != 1 || len(batches[0]) != 1 || batches[0][0].Pane != "%5" || batches[0][0].Text != rendered(web, "proj-%5") {
		t.Fatalf("after a change of state: %+v, want only %%5 as %q", batches, rendered(web, "proj-%5"))
	}

	// An ended session is forgotten, so a session bound again is written again.
	directory.put(web, runtimesession.StateEnded, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%5")
	h.maintainRuntimeSessions(now.Add(4 * time.Second))
	if _, kept := h.runtimeStatusSent[string(web)]; kept || len(writes.take()) != 0 {
		t.Fatal("an ended session kept its status, or was written one")
	}
}

// TestUnsettledTmuxStateIsNotRendered: a state the checkpoint does not hold
// yet is not shown; the pass that makes it durable renders it.
func TestUnsettledTmuxStateIsNotRendered(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	id := newRuntimeID(t)
	directory.put(id, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	h.SetRuntimeSessionLabeler(func(runtimesession.Record) (RuntimeSessionLabel, error) {
		return RuntimeSessionLabel{Project: "api", Profile: "work"}, nil
	})
	writes := &statusWrites{}
	h.SetRuntimeStatusWriter(writes.write)
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.SetRuntimeCheckpointPath(filepath.Join(blocker, runtimeCheckpointFileName))
	requireAccepted(t, h.Handle(runtimeHook(t, id, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))

	now := time.Now()
	h.maintainRuntimeSessions(now)
	if batches := writes.take(); len(batches) != 0 {
		t.Fatalf("a state no checkpoint holds was rendered: %+v", batches)
	}
	h.SetRuntimeCheckpointPath(filepath.Join(t.TempDir(), runtimeCheckpointFileName))
	h.maintainRuntimeSessions(now.Add(time.Second))
	if batches := writes.take(); len(batches) != 1 || len(batches[0]) != 1 || batches[0][0].Pane != "%4" {
		t.Fatalf("after the checkpoint was written: %+v, want the session rendered", batches)
	}
}

// TestFailedStatusWriteIsRepeatedLaterNotEveryPass: a write the backend
// refused is not tried again by the next pass, which would be a client every
// pass; everything is written once more after the retry interval, and a
// change in between is still written when it happens.
func TestFailedStatusWriteIsRepeatedLaterNotEveryPass(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	id := newRuntimeID(t)
	directory.put(id, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	h.SetRuntimeSessionLabeler(func(runtimesession.Record) (RuntimeSessionLabel, error) {
		return RuntimeSessionLabel{Project: "api", Profile: "work"}, nil
	})
	writes := &statusWrites{err: errors.New("no such pane")}
	h.SetRuntimeStatusWriter(writes.write)
	requireAccepted(t, h.Handle(runtimeHook(t, id, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))

	now := time.Now()
	h.maintainRuntimeSessions(now)
	if n := len(writes.take()); n != 1 {
		t.Fatalf("first pass made %d writes, want 1", n)
	}
	h.maintainRuntimeSessions(now.Add(time.Second))
	if n := len(writes.take()); n != 0 {
		t.Fatalf("a refused write was repeated by the next pass (%d writes)", n)
	}
	h.observeRuntimeColors([]tmux.Colored{{ID: id, Color: "red"}})
	h.maintainRuntimeSessions(now.Add(2 * time.Second))
	if batches := writes.take(); len(batches) != 1 || batches[0][0].Color != brightBg(t, "red") {
		t.Fatalf("a change while a write is owed was not written: %+v", batches)
	}
	writes.err = nil
	h.maintainRuntimeSessions(now.Add(2*time.Second + runtimeLabelRetry))
	if batches := writes.take(); len(batches) != 1 || len(batches[0]) != 1 {
		t.Fatalf("the refused write was not repeated after the retry interval: %+v", batches)
	}
	h.maintainRuntimeSessions(now.Add(3*time.Second + 2*runtimeLabelRetry))
	if n := len(writes.take()); n != 0 {
		t.Fatalf("a write that succeeded was repeated (%d writes)", n)
	}
}

// TestRenderedStatusReachesARealTmuxPaneInOneClientPerPass: against a real
// private tmux server and the production writer, with no Kitty, a pass that
// has statuses to write is the reconciliation client and one more however
// many sessions changed, a quiet pass is the reconciliation client alone, and
// the options on each agent's pane hold what the daemon rendered.
func TestRenderedStatusReachesARealTmuxPaneInOneClientPerPass(t *testing.T) {
	w := newRuntimeFaultWorld(t)
	h, _ := w.startDaemon(time.Now())
	h.SetRuntimeSessionLabeler(TmuxRuntimeSessionLabel)
	h.SetProfileColors(map[string]string{"work": "blue"})
	h.SetRuntimeStatusWriter(NewTmuxRuntimeStatusWriter())
	agents := []faultAgent{w.launch("api"), w.launch("web"), w.launch("docs")}
	option := func(pane backend.Ref, name string) string {
		t.Helper()
		out, err := exec.Command(w.tmux.Server.Binary(), "-S", w.tmux.Server.Socket(), "-f", w.tmux.Server.ConfigPath(),
			"display-message", "-p", "-t", pane.ID, "#{"+name+"}").Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSuffix(string(out), "\n")
	}

	// The first pass adopts and labels the sessions; the second has read
	// their titles. Both are warm-up: what is measured is the pass after a
	// change of state.
	now := time.Now().Add(runtimeReconcileInterval)
	h.maintainRuntimeSessions(now)
	now = now.Add(runtimeReconcileInterval)
	h.maintainRuntimeSessions(now)
	for _, agent := range agents {
		title := tmuxSessionTitle(t, h, agent.id)
		if got := option(agent.launched.Pane, tmux.StatusOption); got != title || title == "" {
			t.Fatalf("status of %s = %q, want its title %q", agent.id, got, title)
		}
		if got := option(agent.launched.Pane, tmux.StatusColorOption); got != brightBg(t, "blue") {
			t.Fatalf("color of %s = %q, want its profile's", agent.id, got)
		}
	}

	for _, agent := range agents {
		requireAccepted(t, h.Handle(runtimeHook(t, agent.id, "PermissionRequest", hookevent.HookPayload{SessionID: "conv-" + string(agent.id)})))
	}
	before := tmux.Stats()
	now = now.Add(runtimeReconcileInterval)
	h.maintainRuntimeSessions(now)
	if got := tmux.Stats().Commands - before.Commands; got != 2 {
		t.Fatalf("a pass that rendered three sessions started %d tmux clients, want the reconciliation and one write", got)
	}
	for _, agent := range agents {
		want := palette.BuildTitleParts(palette.TitleParts{
			Base:  palette.ParseTitleParts(tmuxSessionTitle(t, h, agent.id)).Base,
			State: tmuxSessionIn(t, h, agent.id).IconState,
		})
		if got := option(agent.launched.Pane, tmux.StatusOption); got != want || want == tmuxSessionTitle(t, h, agent.id) {
			t.Fatalf("status of %s = %q, want %q", agent.id, got, want)
		}
	}

	before = tmux.Stats()
	now = now.Add(runtimeReconcileInterval)
	h.maintainRuntimeSessions(now)
	if got := tmux.Stats().Commands - before.Commands; got != 1 {
		t.Fatalf("a quiet pass started %d tmux clients, want the reconciliation alone", got)
	}
}

// tmuxSessionTitle is the title the daemon holds for a tmux session.
func tmuxSessionTitle(t *testing.T, h *Handler, runtime runtimesession.ID) string {
	t.Helper()
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.runtimeLabels[string(runtime)].title
}
