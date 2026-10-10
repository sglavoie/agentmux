package daemon

import (
	"errors"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// tmuxSessionIn returns the v2 snapshot's session for runtime.
func tmuxSessionIn(t *testing.T, h *Handler, runtime runtimesession.ID) protocol.CompanionSession {
	t.Helper()
	snap := h.buildCompanionSnapshots(time.Now())[protocol.CompanionSnapshotVersionRuntime]
	for _, s := range snap.Sessions {
		if s.Runtime != nil && s.Runtime.SessionID == string(runtime) {
			return s
		}
	}
	t.Fatalf("runtime session %s is not in the v2 snapshot: %+v", runtime, snap.Sessions)
	return protocol.CompanionSession{}
}

// TestRuntimeSessionsAreLabeledForCompanions: a tmux session is grouped under
// its registry project from its first hook, gains the profile its pane
// metadata holds on the next pass, and is not read again once it has it. A
// failed read keeps the project and is retried later; an ended session's label
// is forgotten. A v1 subscriber never sees any of it.
func TestRuntimeSessionsAreLabeledForCompanions(t *testing.T) {
	h, directory := newRuntimeTestHandler(t)
	onTmux := newRuntimeID(t)
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	directory.mu.Lock()
	record := directory.records[onTmux]
	record.Project = "api"
	directory.records[onTmux] = record
	directory.mu.Unlock()

	reads := 0
	var failure error
	h.SetRuntimeSessionLabeler(func(record runtimesession.Record) (RuntimeSessionLabel, error) {
		reads++
		if failure != nil {
			return RuntimeSessionLabel{}, failure
		}
		return RuntimeSessionLabel{Project: "API", Profile: "work"}, nil
	})

	requireAccepted(t, h.Handle(runtimeHook(t, onTmux, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))
	if s := tmuxSessionIn(t, h, onTmux); s.Project != "api" || s.Profile != "" || reads != 0 {
		t.Fatalf("after the first hook: project %q profile %q, %d backend reads; want the registry project and no read on the hook path", s.Project, s.Profile, reads)
	}

	now := time.Now()
	failure = errors.New("server unreachable")
	h.labelRuntimeSessions(now)
	if s := tmuxSessionIn(t, h, onTmux); s.Project != "api" || s.Profile != "" || reads != 1 {
		t.Fatalf("after a failed read: project %q profile %q, %d reads", s.Project, s.Profile, reads)
	}
	h.labelRuntimeSessions(now.Add(time.Second))
	if reads != 1 {
		t.Fatalf("a failed read was retried at once: %d reads", reads)
	}

	failure = nil
	h.labelRuntimeSessions(now.Add(runtimeLabelRetry))
	if s := tmuxSessionIn(t, h, onTmux); s.Project != "API" || s.Profile != "work" || reads != 2 {
		t.Fatalf("after the read: project %q profile %q, %d reads", s.Project, s.Profile, reads)
	}
	h.labelRuntimeSessions(now.Add(10 * runtimeLabelRetry))
	if reads != 2 {
		t.Fatalf("a labeled session was read again: %d reads", reads)
	}

	v1 := h.buildCompanionSnapshots(time.Now())[protocol.CompanionSnapshotVersion]
	for _, s := range v1.Sessions {
		if s.WindowID == 0 || s.Runtime != nil || s.Profile == "work" {
			t.Fatalf("v1 lists a tmux session: %+v", s)
		}
	}

	directory.put(onTmux, runtimesession.StateEnded, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%4")
	h.maintainRuntimeSessions(now.Add(11 * runtimeLabelRetry))
	h.stateMu.Lock()
	left := len(h.runtimeLabels)
	h.stateMu.Unlock()
	if left != 0 {
		t.Fatalf("an ended session kept its label: %d left", left)
	}
}

// TestTmuxRuntimeSessionLabelReadsPaneMetadata: against a real private tmux
// server, the label is the project and profile the launch wrote on the pane,
// and a record naming another server generation, another session's pane, or a
// pane that is gone reads nothing.
func TestTmuxRuntimeSessionLabelReadsPaneMetadata(t *testing.T) {
	fixture := tmuxtest.New(t)
	id := newRuntimeID(t)
	launched, err := fixture.Backend.LaunchTarget(backend.LaunchSpec{
		Title:     "api",
		Directory: t.TempDir(),
		Vars: map[string]string{
			"amx_project":            "api",
			"amx_profile":            "work",
			"amx_runtime_session_id": string(id),
		},
		Argv: []string{"/bin/sh", "-c", "exec sleep 600"},
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	record := runtimesession.Record{
		ID:      id,
		Project: "registry-project",
		State:   runtimesession.StateLive,
		Target:  &runtimesession.Target{Instance: launched.Instance, Ref: launched.Pane.ID},
	}

	label, err := TmuxRuntimeSessionLabel(record)
	if err != nil || label != (RuntimeSessionLabel{Project: "api", Profile: "work"}) {
		t.Fatalf("label = %+v, %v", label, err)
	}

	other := record
	other.ID = newRuntimeID(t)
	if _, err := TmuxRuntimeSessionLabel(other); err == nil {
		t.Fatal("read the label of a pane another runtime session holds")
	}
	stale := record
	target := *record.Target
	target.Instance.Generation = "0-0"
	stale.Target = &target
	if _, err := TmuxRuntimeSessionLabel(stale); err == nil {
		t.Fatal("read a label from another server generation")
	}
	gone := record
	missing := *record.Target
	missing.Ref = "%999"
	gone.Target = &missing
	if _, err := TmuxRuntimeSessionLabel(gone); err == nil {
		t.Fatal("read a label for a pane that is not there")
	}
	kitty := record
	onKitty := *record.Target
	onKitty.Instance.Kind = runtimesession.BackendKitty
	kitty.Target = &onKitty
	if _, err := TmuxRuntimeSessionLabel(kitty); !errors.Is(err, errRuntimeLabelUnsupported) {
		t.Fatalf("a Kitty record: %v", err)
	}
}
