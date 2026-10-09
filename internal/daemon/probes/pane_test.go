package probes

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

func tmuxPane(id string, pid int, vars map[string]string) backend.Pane {
	return backend.Pane{
		Ref:        backend.PaneID(id),
		Group:      backend.GroupID("$" + id[1:]),
		Title:      "title",
		CWD:        "/work",
		Vars:       vars,
		Foreground: []backend.Process{{PID: pid, CWD: "/work"}},
	}
}

func TestPaneStoreEpochs(t *testing.T) {
	var store PaneStore
	at := screenNow
	key := func(id string) PaneKey { return PaneKey{Instance: tmuxInstance, Pane: backend.PaneID(id)} }

	if _, ok := store.Observe(key("%1")); ok {
		t.Fatal("a cold store observed a pane")
	}
	if store.Fresh(PaneObservation{Key: key("%1")}) {
		t.Fatal("a cold store called an observation fresh")
	}

	agent := tmuxPane("%1", 100, map[string]string{"amx_harness": "claude", "amx_profile": "work"})
	sibling := tmuxPane("%2", 200, map[string]string{"amx_harness": "codex", "amx_profile": "work"})
	store.Publish(PanePublication{Instance: tmuxInstance, Panes: []backend.Pane{agent, sibling}, CapturedAt: at})
	first, ok := store.Observe(key("%1"))
	if !ok || first.Epoch == 0 || first.CapturedAt != at || first.Pane.Ref != agent.Ref {
		t.Fatalf("Observe = %+v, %v", first, ok)
	}
	other, _ := store.Observe(key("%2"))
	if other.Epoch == first.Epoch {
		t.Fatal("two panes share an epoch")
	}

	// Churn probes do not read — title text, working directory, a later
	// discovery time — keeps the epoch.
	churned := agent
	churned.Title, churned.CWD = "another title", "/elsewhere"
	store.Publish(PanePublication{
		Instance: tmuxInstance, Panes: []backend.Pane{churned, sibling}, CapturedAt: at.Add(time.Second),
	})
	if !store.Fresh(first) {
		t.Fatal("title and cwd churn invalidated the pane")
	}

	// A sibling changing does not touch this pane.
	sibling.Exit = &backend.Exit{Status: 1}
	sibling.Foreground = nil
	store.Publish(PanePublication{Instance: tmuxInstance, Panes: []backend.Pane{churned, sibling}, CapturedAt: at})
	if !store.Fresh(first) {
		t.Fatal("a sibling's exit invalidated the pane")
	}
	if store.Fresh(other) {
		t.Fatal("an exited pane kept its epoch")
	}

	changes := map[string]func(*backend.Pane){
		"program exited":  func(p *backend.Pane) { p.Exit = &backend.Exit{Status: 0}; p.Foreground = nil },
		"process changed": func(p *backend.Pane) { p.Foreground = []backend.Process{{PID: 101}} },
		"muted": func(p *backend.Pane) {
			p.Vars = map[string]string{"amx_harness": "claude", "amx_profile": "work", "cc_mute": "1"}
		},
		"profile changed": func(p *backend.Pane) { p.Vars = map[string]string{"amx_harness": "claude", "amx_profile": "home"} },
		"harness changed": func(p *backend.Pane) { p.Vars = map[string]string{"amx_harness": "codex", "amx_profile": "work"} },
		"moved group":     func(p *backend.Pane) { p.Group = backend.GroupID("$9") },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			var store PaneStore
			store.Publish(PanePublication{Instance: tmuxInstance, Panes: []backend.Pane{agent}, CapturedAt: at})
			before, _ := store.Observe(key("%1"))
			changed := agent
			change(&changed)
			store.Publish(PanePublication{Instance: tmuxInstance, Panes: []backend.Pane{changed}, CapturedAt: at})
			if store.Fresh(before) {
				t.Fatal("the change kept the pane's epoch")
			}
			after, _ := store.Observe(key("%1"))
			if after.Epoch <= before.Epoch {
				t.Fatalf("epoch went from %d to %d", before.Epoch, after.Epoch)
			}
		})
	}
}

// A restarted tmux server reuses %1. The old observation is not fresh, the old
// key finds nothing, and the new pane has its own epoch.
func TestPaneStoreServerRestartInvalidatesEveryPane(t *testing.T) {
	var store PaneStore
	agent := tmuxPane("%1", 100, map[string]string{"amx_harness": "claude"})
	store.Publish(PanePublication{Instance: tmuxInstance, Panes: []backend.Pane{agent}, CapturedAt: screenNow})
	oldKey := PaneKey{Instance: tmuxInstance, Pane: agent.Ref}
	before, _ := store.Observe(oldKey)

	restarted := tmuxInstance
	restarted.Generation = "5151-1791588400"
	store.Publish(PanePublication{Instance: restarted, Panes: []backend.Pane{agent}, CapturedAt: screenNow})
	if store.Fresh(before) {
		t.Fatal("an observation survived a server restart")
	}
	if _, ok := store.Observe(oldKey); ok {
		t.Fatal("the old generation's key still resolves")
	}
	after, ok := store.Observe(PaneKey{Instance: restarted, Pane: agent.Ref})
	if !ok || after.Epoch == before.Epoch {
		t.Fatalf("restarted pane = %+v, %v; want a new epoch", after, ok)
	}

	// Another server at another address is independent.
	elsewhere := runtimesession.BackendInstance{Kind: runtimesession.BackendTmux, Address: "/other.sock", Generation: "1-1"}
	store.Publish(PanePublication{Instance: elsewhere, CapturedAt: screenNow})
	if !store.Fresh(after) {
		t.Fatal("publishing another server invalidated this one")
	}

	// A pane the server stops reporting is gone.
	store.Publish(PanePublication{Instance: restarted, CapturedAt: screenNow})
	if store.Fresh(after) {
		t.Fatal("a vanished pane is still fresh")
	}
}

// A screen read for a pane observation must be of that pane and generation,
// and no older than the discovery.
func TestPaneObservationScreenExpectation(t *testing.T) {
	var store PaneStore
	agent := tmuxPane("%3", 100, nil)
	store.Publish(PanePublication{Instance: tmuxInstance, Panes: []backend.Pane{agent}, CapturedAt: screenNow.Add(-2 * time.Second)})
	observation, _ := store.Observe(PaneKey{Instance: tmuxInstance, Pane: agent.Ref})
	want := observation.ScreenExpectation()

	if got := CheckScreen(freshTmuxScreen("x"), nil, want, screenNow); got != ScreenFresh {
		t.Fatalf("a read after the discovery is %s", got)
	}
	early := freshTmuxScreen("x")
	early.CapturedAt = screenNow.Add(-3 * time.Second)
	if got := CheckScreen(early, nil, want, screenNow); got != ScreenStale {
		t.Fatalf("a read from before the discovery is %s, want stale", got)
	}
}

func TestPaneHarnessProcess(t *testing.T) {
	live := tmuxPane("%1", 100, nil)
	dead := live
	dead.Exit = &backend.Exit{Status: -1, Signal: "term"}
	dead.Foreground = nil

	cases := []struct {
		name  string
		pane  backend.Pane
		found bool
		table HarnessProcessInspection
		want  HarnessProcessInspection
	}{
		{"alive", live, true, HarnessProcessAliveSameInstance, HarnessProcessAliveSameInstance},
		{"table says missing", live, true, HarnessProcessMissing, HarnessProcessMissing},
		{"table says replaced", live, true, HarnessProcessReplacedInstance, HarnessProcessReplacedInstance},
		{"table unknown stays unknown", live, true, HarnessProcessUnknown, HarnessProcessUnknown},
		{"table silent stays unknown", live, true, "", HarnessProcessUnknown},
		// The PID was reused by something else; the retained pane says it ended.
		{"retained exited pane", dead, true, HarnessProcessAliveSameInstance, HarnessProcessMissing},
		{"pane gone", backend.Pane{}, false, HarnessProcessAliveSameInstance, HarnessProcessMissing},
		{"pane gone and table unknown", backend.Pane{}, false, HarnessProcessUnknown, HarnessProcessMissing},
	}
	for _, tc := range cases {
		if got := PaneHarnessProcess(tc.pane, tc.found, tc.table); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}
