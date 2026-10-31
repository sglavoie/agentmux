package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/flightlog"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestFlightLogCountsEffectivePresentationChangesAcrossSourcesOnly(t *testing.T) {
	store := flightlog.Store{Path: filepath.Join(t.TempDir(), "flight.jsonl")}
	collector, err := flightlog.NewCollector(store)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, nil)
	h.flightLog = collector
	h.runtimeRoutes.bySlot = map[int]runtimeBinding{1: {runtime: "rs-one"}}
	h.runtimeLabels = map[string]runtimeLabel{"rs-one": {project: "API", profile: "work", harness: "codex"}}
	st := &state.WindowState{}
	h.store[1] = st
	h.stateMu.Lock()
	h.publishEffectiveLocked(1, st, state.EffectiveState{Icon: "bolt", Source: protocol.SourceID{Key: "parent"}})
	h.publishEffectiveLocked(1, st, state.EffectiveState{Icon: "bolt", Source: protocol.SourceID{Key: "child"}}) // hidden ownership change
	h.publishEffectiveLocked(1, st, state.EffectiveState{Icon: "bolt", Source: protocol.SourceID{Key: "child"}}) // duplicate publication
	h.publishEffectiveLocked(1, st, state.EffectiveState{Icon: "bolt", Looping: true})
	h.publishEffectiveLocked(1, st, state.EffectiveState{Icon: "stop", Looping: true})
	h.stateMu.Unlock()
	h.recordDecisionEvent(protocol.SessionEvent{TargetWindowID: 1, Event: "AgentSessionLoopFinished", RunID: "run-one", ReducerDecision: protocol.DecisionApplied})
	collector.Close()
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := flightlog.Build(snapshot, flightlog.Filters{Period: "all"}, time.Now().Add(time.Second), []string{"codex"})
	if r.Totals.StateChanges != 3 || r.Totals.TurnsFinished != 1 || r.Totals.RunsFinished != 1 {
		t.Fatalf("wrong effective-state counts with journal disabled: %+v", r.Totals)
	}
	if len(r.Sessions) != 1 || r.Sessions[0].Key != "rs-one" || r.Sessions[0].Project != "API" {
		t.Fatalf("lost canonical identity: %+v", r.Sessions)
	}
}

func TestFlightLogKeepsRecordedRunOwnerWhenItsSlotHasBeenReused(t *testing.T) {
	store := flightlog.Store{Path: filepath.Join(t.TempDir(), "flight.jsonl")}
	collector, err := flightlog.NewCollector(store)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(nil, nil)
	h.flightLog = collector
	h.store[1] = &state.WindowState{LastHarness: "codex", LastAccount: "other"}
	h.runtimeRoutes.bySlot = map[int]runtimeBinding{1: {runtime: "rs-replacement"}}
	h.runtimeLabels = map[string]runtimeLabel{"rs-replacement": {project: "Other project", harness: "codex"}}
	h.recordDecisionEvent(protocol.SessionEvent{TargetWindowID: 1, Event: "AgentSessionLoopFinished", RunID: "original-run", ProducerHarness: "codex", Profile: "work", SessionID: "original-conversation", ReducerDecision: protocol.DecisionApplied, Origin: &protocol.HookOrigin{RuntimeSessionID: "rs-original"}})
	collector.Close()
	snapshot, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := flightlog.Build(snapshot, flightlog.Filters{Period: "all"}, time.Now().Add(time.Second), []string{"codex"})
	if len(r.Sessions) != 1 || r.Sessions[0].Key != "rs-original" || r.Sessions[0].ConversationID != "codex:original-conversation" || r.Sessions[0].Project != "" {
		t.Fatalf("run attributed to replacement: %+v", r.Sessions)
	}
}

func TestFlightLogUsesTheMatchingKittySocketForLaunchIdentity(t *testing.T) {
	h := NewHandler(nil, nil)
	h.runtimeRoutes.kittyBySlot = map[int]kittySlotKey{100: {socket: "unix:other", windowID: 1}}
	h.otherKittyTrees = map[string][]kitty.OSWindow{"unix:other": {{Tabs: []kitty.Tab{{Windows: []kitty.Window{{ID: 1, UserVars: kitty.UserVars{LaunchID: "other-launch", Project: "Other project"}}}}}}}}
	e := h.flightEventLocked(100, &state.WindowState{LastHarness: "codex"}, time.Now())
	if e.Key != "launch:other-launch" || e.Project != "Other project" {
		t.Fatalf("lost other-socket launch identity: %+v", e)
	}
}
