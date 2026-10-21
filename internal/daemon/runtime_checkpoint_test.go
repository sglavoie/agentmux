package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/runtimesession"
)

// These tests pin the backend-neutral checkpoint: what survives a daemon
// restart, what a restore refuses to resurrect, and that restored state never
// overwrites what a fresh event already established.

func checkpointHandler(t *testing.T, path string, directory *fakeRuntimeDirectory) *Handler {
	t.Helper()
	h := newTestHandler()
	h.SetStopConfirm(0, 0, false)
	h.SetKittyClient(&fakeKittyClient{windows: hookEventWindows("api", []kitty.Window{
		{ID: 7, IsActive: true, UserVars: kitty.UserVars{Profile: "managed", Harness: "claude"}},
	})})
	h.SetRuntimeSessionDirectory(directory)
	h.SetRuntimeCheckpointPath(path)
	return h
}

func readCheckpoint(t *testing.T, path string) runtimeCheckpointFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file runtimeCheckpointFile
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

func checkpointEntry(file runtimeCheckpointFile, id runtimesession.ID) (runtimeCheckpointEntry, bool) {
	for _, e := range file.Sessions {
		if e.RuntimeSessionID == string(id) {
			return e, true
		}
	}
	return runtimeCheckpointEntry{}, false
}

func boundSlot(h *Handler, id runtimesession.ID) (int, bool) {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	slot, ok := h.runtimeRoutes.byRuntime[string(id)]
	return slot, ok
}

func reducerRuntime(h *Handler, slot int) string {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	if st, ok := h.store[slot]; ok && st.Reducer != nil {
		return st.Reducer.RuntimeSession()
	}
	return ""
}

// TestRuntimeCheckpointSurvivesDaemonRestart: a tmux session's facts and
// binding, and a Kitty session's binding, come back in a new daemon; the tmux
// session's next event lands on its restored state instead of replacing it.
func TestRuntimeCheckpointSurvivesDaemonRestart(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), runtimeCheckpointFileName)
	directory := newFakeRuntimeDirectory()
	onTmux, onKitty := newRuntimeID(t), newRuntimeID(t)
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "tmux-t-1", "%3")
	directory.put(onKitty, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "kitty-a-1", "7")

	before := checkpointHandler(t, path, directory)
	requireAccepted(t, before.Handle(runtimeHook(t, onTmux, "UserPromptSubmit", hookevent.HookPayload{SessionID: "tmux-conv"})))
	requireAccepted(t, before.Handle(runtimeHook(t, onTmux, "SubagentStart", hookevent.HookPayload{SessionID: "tmux-conv", AgentID: "agent-1"})))
	requireAccepted(t, before.Handle(runtimeHook(t, onKitty, "UserPromptSubmit", hookevent.HookPayload{SessionID: "kitty-conv"})))
	tmuxFacts := slotFacts(before, slotOf(t, before, onTmux))
	before.maintainRuntimeSessions(time.Now())

	file := readCheckpoint(t, path)
	tmuxEntry, ok := checkpointEntry(file, onTmux)
	if !ok || tmuxEntry.Backend != "tmux" || tmuxEntry.Pane != "%3" || tmuxEntry.Generation != "tmux-t-1" || tmuxEntry.State == "" {
		t.Fatalf("tmux checkpoint entry = %+v (present %v)", tmuxEntry, ok)
	}
	kittyEntry, ok := checkpointEntry(file, onKitty)
	if !ok || kittyEntry.Backend != "kitty" || kittyEntry.Pane != "7" || kittyEntry.State != "" {
		t.Fatalf("Kitty checkpoint entry = %+v (present %v); Kitty state belongs in amx_state", kittyEntry, ok)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("checkpoint mode = %v, err %v", info.Mode(), err)
	}

	after := checkpointHandler(t, path, directory)
	report := after.restoreRuntimeCheckpoint(time.Now())
	if report.Corrupt || report.Outcomes[string(onTmux)] != runtimeRestoreRestored || report.Outcomes[string(onKitty)] != runtimeRestoreRestored {
		t.Fatalf("restore report = %+v", report)
	}
	slot := slotOf(t, after, onTmux)
	if !isRuntimeSyntheticSlot(slot) {
		t.Fatalf("tmux runtime restored onto Kitty-range slot %d", slot)
	}
	restored := slotFacts(after, slot)
	if len(restored) != len(tmuxFacts) || !hasFact(restored, protocol.SourceHarness, "claude:tmux-conv") ||
		!hasFact(restored, protocol.SourceSubagent, "agent-1") {
		t.Fatalf("restored facts = %+v; want %+v", restored, tmuxFacts)
	}
	if got := reducerRuntime(after, slot); got != string(onTmux) {
		t.Fatalf("restored reducer bound to %q; want %s", got, onTmux)
	}
	if kittySlot, ok := boundSlot(after, onKitty); !ok || kittySlot != 7 {
		t.Fatalf("Kitty runtime binding = %d, %v; want slot 7", kittySlot, ok)
	}

	// The session's next event extends the restored state rather than
	// replacing it as a different session's would.
	requireAccepted(t, after.Handle(runtimeHook(t, onTmux, "PreToolUse", hookevent.HookPayload{SessionID: "tmux-conv"})))
	if slotOf(t, after, onTmux) != slot || !hasFact(slotFacts(after, slot), protocol.SourceSubagent, "agent-1") {
		t.Fatalf("post-restart event discarded restored facts: %+v", slotFacts(after, slot))
	}

	// A Kitty restore that lands after the checkpoint (a degraded Kitty
	// startup) binds the rebuilt reducer to the checkpointed session.
	kittyPayload := func() string {
		before.stateMu.Lock()
		defer before.stateMu.Unlock()
		payload, err := before.store[7].MarshalUserVar()
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}()
	after.restoreFromUserVars(hookEventWindows("api", []kitty.Window{
		{ID: 7, UserVars: kitty.UserVars{Profile: "managed", Harness: "claude", AMXState: kittyPayload}},
	}), time.Now())
	if got := reducerRuntime(after, 7); got != string(onKitty) {
		t.Fatalf("Kitty-restored reducer bound to %q; want %s", got, onKitty)
	}
}

// TestRuntimeCheckpointFreshEventsWinOverRestoredState: an event accepted
// before the restore keeps its fact; the restore only adds what the fresh
// state lacks.
func TestRuntimeCheckpointFreshEventsWinOverRestoredState(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), runtimeCheckpointFileName)
	directory := newFakeRuntimeDirectory()
	onTmux := newRuntimeID(t)
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%1")

	before := checkpointHandler(t, path, directory)
	requireAccepted(t, before.Handle(runtimeHook(t, onTmux, "UserPromptSubmit", hookevent.HookPayload{SessionID: "conv"})))
	requireAccepted(t, before.Handle(runtimeHook(t, onTmux, "SubagentStart", hookevent.HookPayload{SessionID: "conv", AgentID: "agent-1"})))
	if err := before.flushRuntimeCheckpoint(); err != nil {
		t.Fatal(err)
	}

	after := checkpointHandler(t, path, directory)
	requireAccepted(t, after.Handle(runtimeHook(t, onTmux, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))
	slot := slotOf(t, after, onTmux)
	fresh, _ := func() (protocol.SourceFactSnapshot, bool) {
		after.stateMu.Lock()
		defer after.stateMu.Unlock()
		return after.store[slot].Reducer.Fact(protocol.SourceID{Kind: protocol.SourceHarness, Key: "claude:conv"})
	}()

	report := after.restoreRuntimeCheckpoint(time.Now())
	if report.Outcomes[string(onTmux)] != runtimeRestoreMerged {
		t.Fatalf("restore outcome = %q; want merged", report.Outcomes[string(onTmux)])
	}
	if slotOf(t, after, onTmux) != slot {
		t.Fatal("restore moved the session to another slot")
	}
	facts := slotFacts(after, slot)
	var harness protocol.SourceFactSnapshot
	for _, f := range facts {
		if f.Kind == protocol.SourceHarness {
			harness = f
		}
	}
	if harness.Icon != "key" || harness.Seq != fresh.Seq || harness.Action != fresh.Action {
		t.Fatalf("fresh harness fact overwritten: %+v; want %+v", harness, fresh)
	}
	if !hasFact(facts, protocol.SourceSubagent, "agent-1") {
		t.Fatalf("disjoint checkpointed subagent not merged: %+v", facts)
	}
	if icon := slotIcon(after, slot); icon != "key" {
		t.Fatalf("effective icon = %q; want the fresh key", icon)
	}
	// Restoring again is idempotent.
	again := after.restoreRuntimeCheckpoint(time.Now())
	if again.Outcomes[string(onTmux)] != runtimeRestoreMerged || len(slotFacts(after, slot)) != len(facts) {
		t.Fatalf("second restore changed state: %+v", slotFacts(after, slot))
	}
}

// TestRuntimeCheckpointDropsStaleTargets: a session the registry ended, no
// longer knows, or lists at another generation or pane is not resurrected,
// and the next flush forgets it.
func TestRuntimeCheckpointDropsStaleTargets(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), runtimeCheckpointFileName)
	directory := newFakeRuntimeDirectory()
	ended, moved, forgotten, live := newRuntimeID(t), newRuntimeID(t), newRuntimeID(t), newRuntimeID(t)
	for i, id := range []runtimesession.ID{ended, moved, forgotten, live} {
		directory.put(id, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%"+string(rune('1'+i)))
	}
	before := checkpointHandler(t, path, directory)
	for _, id := range []runtimesession.ID{ended, moved, forgotten, live} {
		requireAccepted(t, before.Handle(runtimeHook(t, id, "UserPromptSubmit", hookevent.HookPayload{SessionID: string(id)})))
	}
	if err := before.flushRuntimeCheckpoint(); err != nil {
		t.Fatal(err)
	}

	directory.setState(ended, runtimesession.StateEnded)
	directory.put(moved, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g2", "%2")
	directory.mu.Lock()
	delete(directory.records, forgotten)
	directory.mu.Unlock()

	after := checkpointHandler(t, path, directory)
	report := after.restoreRuntimeCheckpoint(time.Now())
	for id, want := range map[runtimesession.ID]runtimeRestoreOutcome{
		ended: runtimeRestoreStale, moved: runtimeRestoreStale, forgotten: runtimeRestoreStale, live: runtimeRestoreRestored,
	} {
		if got := report.Outcomes[string(id)]; got != want {
			t.Fatalf("outcome for %s = %q; want %q", id, got, want)
		}
	}
	for _, id := range []runtimesession.ID{ended, moved, forgotten} {
		if _, bound := boundSlot(after, id); bound {
			t.Fatalf("stale session %s was rebound", id)
		}
	}
	after.stateMu.Lock()
	slots := len(after.store)
	after.stateMu.Unlock()
	if slots != 1 {
		t.Fatalf("store holds %d slots; want only the live session's", slots)
	}
	// The moved session's new generation starts clean on its own slot.
	requireAccepted(t, after.Handle(runtimeHook(t, moved, "PermissionRequest", hookevent.HookPayload{SessionID: "moved-again"})))
	if hasFact(slotFacts(after, slotOf(t, after, moved)), protocol.SourceHarness, "claude:"+string(moved)) {
		t.Fatal("a stale target's checkpointed fact reached the session's new target")
	}

	if err := after.flushRuntimeCheckpoint(); err != nil {
		t.Fatal(err)
	}
	file := readCheckpoint(t, path)
	for _, id := range []runtimesession.ID{ended, forgotten} {
		if _, ok := checkpointEntry(file, id); ok {
			t.Fatalf("flush kept stale session %s", id)
		}
	}
	if e, ok := checkpointEntry(file, moved); !ok || e.Generation != "g2" {
		t.Fatalf("moved session entry = %+v; want its new generation", e)
	}
}

// TestRuntimeCheckpointDefersRestoreWhileRegistryIsUnreadable: a degraded
// startup that cannot read the registry neither restores nor drops the
// checkpointed sessions; maintenance restores them once it can.
func TestRuntimeCheckpointDefersRestoreWhileRegistryIsUnreadable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), runtimeCheckpointFileName)
	directory := newFakeRuntimeDirectory()
	onTmux := newRuntimeID(t)
	directory.put(onTmux, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%1")
	before := checkpointHandler(t, path, directory)
	requireAccepted(t, before.Handle(runtimeHook(t, onTmux, "PermissionRequest", hookevent.HookPayload{SessionID: "conv"})))
	if err := before.flushRuntimeCheckpoint(); err != nil {
		t.Fatal(err)
	}

	unreadable := newFakeRuntimeDirectory()
	unreadable.err = os.ErrPermission
	after := checkpointHandler(t, path, unreadable)
	report := after.restoreRuntimeCheckpoint(time.Now())
	if report.Outcomes[string(onTmux)] != runtimeRestoreDeferred {
		t.Fatalf("outcome = %q; want deferred", report.Outcomes[string(onTmux)])
	}
	if _, bound := boundSlot(after, onTmux); bound {
		t.Fatal("an unverified session was restored")
	}
	after.maintainRuntimeSessions(time.Now())
	if _, ok := checkpointEntry(readCheckpoint(t, path), onTmux); !ok {
		t.Fatal("maintenance flush dropped a deferred session")
	}

	unreadable.mu.Lock()
	unreadable.err = nil
	unreadable.records = directory.records
	unreadable.mu.Unlock()
	after.maintainRuntimeSessions(time.Now())
	slot, bound := boundSlot(after, onTmux)
	if !bound || slotIcon(after, slot) != "key" {
		t.Fatalf("deferred session not restored once the registry answered (bound %v)", bound)
	}
	after.stateMu.Lock()
	pending := len(after.runtimeRestorePending)
	after.stateMu.Unlock()
	if pending != 0 {
		t.Fatalf("%d entries still pending after restore", pending)
	}
}

// TestRuntimeCheckpointCorruptFileIsQuarantined: a corrupt checkpoint
// restores nothing, is moved aside, and the next flush starts it over.
func TestRuntimeCheckpointCorruptFileIsQuarantined(t *testing.T) {
	t.Parallel()

	for name, content := range map[string]string{
		"garbage":       "{not json",
		"unknown field": `{"schema_version":1,"sessions":[],"extra":true}`,
		"future schema": `{"schema_version":2,"sessions":[]}`,
		"trailing data": `{"schema_version":1,"sessions":[]} {}`,
		"duplicate":     `{"schema_version":1,"sessions":[{"runtime_session_id":"rs-0123456789abcdef0123456789abcdef","backend":"tmux","instance":"/t","generation":"g","pane":"%1"},{"runtime_session_id":"rs-0123456789abcdef0123456789abcdef","backend":"tmux","instance":"/t","generation":"g","pane":"%2"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, runtimeCheckpointFileName)
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			h := checkpointHandler(t, path, newFakeRuntimeDirectory())
			report := h.restoreRuntimeCheckpoint(time.Now())
			if !report.Corrupt || report.Quarantined == "" || len(report.Outcomes) != 0 {
				t.Fatalf("report = %+v; want corrupt and quarantined", report)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("corrupt checkpoint left in place: %v", err)
			}
			if data, err := os.ReadFile(report.Quarantined); err != nil || string(data) != content {
				t.Fatalf("quarantined copy = %q, %v", data, err)
			}
			if err := h.flushRuntimeCheckpoint(); err != nil {
				t.Fatal(err)
			}
			if file := readCheckpoint(t, path); file.SchemaVersion != runtimeCheckpointSchemaVersion || len(file.Sessions) != 0 {
				t.Fatalf("fresh checkpoint = %+v", file)
			}
		})
	}
}

// TestRuntimeCheckpointRejectsInvalidAndConflictingEntries: a malformed entry
// is skipped without blocking the others, and a slot another live session
// already holds stays with it.
func TestRuntimeCheckpointRejectsInvalidAndConflictingEntries(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), runtimeCheckpointFileName)
	directory := newFakeRuntimeDirectory()
	good, badState, holder, displaced := newRuntimeID(t), newRuntimeID(t), newRuntimeID(t), newRuntimeID(t)
	directory.put(good, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%1")
	directory.put(badState, runtimesession.StateLive, runtimesession.BackendTmux, tmuxT, "g1", "%2")
	directory.put(holder, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "k1", "7")
	directory.put(displaced, runtimesession.StateLive, runtimesession.BackendKitty, kittyA, "k1", "7")
	file := runtimeCheckpointFile{SchemaVersion: runtimeCheckpointSchemaVersion, Sessions: []runtimeCheckpointEntry{
		{RuntimeSessionID: "not-a-runtime-id", Backend: "tmux", Instance: tmuxT, Generation: "g1", Pane: "%9"},
		{RuntimeSessionID: string(good), Backend: "tmux", Instance: tmuxT, Generation: "g1", Pane: "%1"},
		{RuntimeSessionID: string(badState), Backend: "tmux", Instance: tmuxT, Generation: "g1", Pane: "%2", State: "{broken"},
		{RuntimeSessionID: string(displaced), Backend: "kitty", Instance: kittyA, Generation: "k1", Pane: "7"},
	}}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	h := checkpointHandler(t, path, directory)
	requireAccepted(t, h.Handle(runtimeHook(t, holder, "PermissionRequest", hookevent.HookPayload{SessionID: "holder"})))
	report := h.restoreRuntimeCheckpoint(time.Now())
	for id, want := range map[string]runtimeRestoreOutcome{
		"not-a-runtime-id": runtimeRestoreInvalid,
		string(good):       runtimeRestoreRestored,
		string(badState):   runtimeRestoreInvalid,
		string(displaced):  runtimeRestoreConflict,
	} {
		if got := report.Outcomes[id]; got != want {
			t.Fatalf("outcome for %s = %q; want %q", id, got, want)
		}
	}
	if slot, ok := boundSlot(h, holder); !ok || slot != 7 || !hasFact(slotFacts(h, 7), protocol.SourceHarness, "claude:holder") {
		t.Fatal("a checkpoint displaced the live holder of Kitty slot 7")
	}
	if _, bound := boundSlot(h, badState); bound {
		t.Fatal("an entry with a malformed payload was bound")
	}
	h.stateMu.Lock()
	reserved := len(h.runtimeRoutes.synthetic)
	h.stateMu.Unlock()
	if reserved != 1 {
		t.Fatalf("%d synthetic slots reserved; a declined restore must not keep one", reserved)
	}
}
