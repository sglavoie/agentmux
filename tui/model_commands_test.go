package tui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

func TestHealthCmd_StartsDaemonWhenDead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldSpawn := commands.SpawnDaemonDetached
	spawned := ""
	commands.SpawnDaemonDetached = func(amxBin string) error {
		spawned = amxBin
		return nil
	}
	t.Cleanup(func() { commands.SpawnDaemonDetached = oldSpawn })

	msg, ok := commands.Health(&config.Config{}, "/tmp/amx", true, false)().(commands.HealthMsg)
	if !ok {
		t.Fatal("healthCmd returned an unexpected message type")
	}
	if msg.DaemonAlive {
		t.Fatal("daemonAlive = true, want false before the detached daemon is ready")
	}
	if spawned != "/tmp/amx" {
		t.Fatalf("SpawnDetached path = %q, want /tmp/amx", spawned)
	}
}

// TestApplyDaemonStatesStampsBusySince asserts the busy anchor follows the
// daemon: a reported anchor lands as a real timestamp, an omitted one stays
// the zero time rather than the unix epoch, and a window the daemon has no
// entry for is cleared alongside State and Looping.
func TestApplyDaemonStatesStampsBusySince(t *testing.T) {
	t.Parallel()

	anchor := time.Unix(1710000100, 0)
	sessions := []session.RunningSession{
		{WindowID: 1},
		{WindowID: 2},
		{WindowID: 3, State: "bolt", Looping: true, BusySince: anchor},
	}
	commands.ApplyDaemonStates(sessions, map[int]protocol.WindowStateSnapshot{
		1: {IconState: "bolt", BusySinceAt: anchor.Unix()},
		2: {IconState: "key"},
	})

	if !sessions[0].BusySince.Equal(anchor) {
		t.Errorf("BusySince = %v, want the daemon's anchor %v", sessions[0].BusySince, anchor)
	}
	if !sessions[1].BusySince.IsZero() {
		t.Errorf("BusySince = %v for an idle window, want the zero time", sessions[1].BusySince)
	}
	if !sessions[2].BusySince.IsZero() {
		t.Errorf("BusySince = %v for a window the daemon does not know, want the zero time", sessions[2].BusySince)
	}
}

func TestApplyDaemonStatesOverlaysAndClearsPresentationChangedAt(t *testing.T) {
	t.Parallel()

	changedAt := time.Unix(1_710_000_100, 123_000_000)
	sessions := []session.RunningSession{
		{WindowID: 1},
		{WindowID: 2, PresentationChangedAt: changedAt},
		{WindowID: 3, PresentationChangedAt: changedAt},
	}
	commands.ApplyDaemonStates(sessions, map[int]protocol.WindowStateSnapshot{
		1: {PresentationChangedAtMS: changedAt.UnixMilli()},
		2: {},
	})

	if !sessions[0].PresentationChangedAt.Equal(changedAt) {
		t.Errorf("PresentationChangedAt = %v; want %v", sessions[0].PresentationChangedAt, changedAt)
	}
	if !sessions[1].PresentationChangedAt.IsZero() {
		t.Errorf("omitted timestamp left stale value %v", sessions[1].PresentationChangedAt)
	}
	if !sessions[2].PresentationChangedAt.IsZero() {
		t.Errorf("no-entry row left stale value %v", sessions[2].PresentationChangedAt)
	}
}

// TestApplyDaemonStatesOverlaysUnseen asserts the unseen bit is overlaid in
// both directions and, crucially, cleared in the no-entry branch alongside
// State/Looping/BusySince. A row the daemon has no entry for carries no
// authority for the bit, and leaving a stale true there would pin it under
// Needs Attention for as long as the daemon stayed silent about it.
func TestApplyDaemonStatesOverlaysUnseen(t *testing.T) {
	t.Parallel()

	sessions := []session.RunningSession{
		{WindowID: 1},
		{WindowID: 2, Unseen: true},
		{WindowID: 3, State: "stop", Unseen: true},
	}
	commands.ApplyDaemonStates(sessions, map[int]protocol.WindowStateSnapshot{
		1: {IconState: "stop", Unseen: true},
		2: {IconState: "stop"},
	})

	if !sessions[0].Unseen {
		t.Error("Unseen = false for a window the daemon reports unseen, want true")
	}
	if sessions[1].Unseen {
		t.Error("Unseen = true after the daemon reported the window seen, want false")
	}
	if sessions[2].Unseen {
		t.Error("Unseen = true for a window the daemon does not know, want false")
	}
	if sessions[2].State != "" || sessions[2].Looping {
		t.Errorf("no-entry row = %+v, want State and Looping cleared alongside Unseen", sessions[2])
	}
}

// TestApplyDaemonStatesOverlaysReason asserts the blocked reason is overlaid in
// both directions and cleared in the no-entry branch alongside
// State/Looping/BusySince/Unseen. A reason only ever explains the icon the same
// entry supplied, so a row the daemon has dropped must not keep glossing a
// prompt that is no longer on screen — and it must not keep glossing one after
// the daemon reports the window blocked by something it could not label either.
func TestApplyDaemonStatesOverlaysReason(t *testing.T) {
	t.Parallel()

	sessions := []session.RunningSession{
		{WindowID: 1},
		{WindowID: 2, Reason: "command_approval"},
		{WindowID: 3, State: "key", Reason: "permission"},
	}
	commands.ApplyDaemonStates(sessions, map[int]protocol.WindowStateSnapshot{
		1: {IconState: "key", Reason: "permission"},
		2: {IconState: "key"},
	})

	if sessions[0].Reason != "permission" {
		t.Errorf("Reason = %q for a labelled window, want %q", sessions[0].Reason, "permission")
	}
	if sessions[1].Reason != "" {
		t.Errorf("Reason = %q after the daemon reported the window unlabelled, want empty", sessions[1].Reason)
	}
	if sessions[2].Reason != "" {
		t.Errorf("Reason = %q for a window the daemon does not know, want empty", sessions[2].Reason)
	}
	if sessions[2].State != "" || sessions[2].Looping {
		t.Errorf("no-entry row = %+v, want State and Looping cleared alongside Reason", sessions[2])
	}
}

// TestApplyDaemonStatesOverlaysBranchAndDirty asserts the Git pair is overlaid
// in both directions and cleared in the no-entry branch alongside every other
// daemon-owned field.
//
// The reset matters more here than for any of the others. A branch describes a
// checkout on disk rather than a fact about the session, so one carried forward
// past the entry that supplied it would go on naming a directory nobody has
// looked at since — for as long as the row stayed on screen, with nothing on the
// row admitting the claim is old. Clearing costs the row nothing visible,
// because a view with no branch does not reserve the column at all.
//
// The pair is also overlaid independently: a window the daemon reports as dirty
// with no branch (a detached HEAD) must not keep the branch it held before, and
// a window it reports clean must lose the dirty bit rather than the branch.
func TestApplyDaemonStatesOverlaysBranchAndDirty(t *testing.T) {
	t.Parallel()

	sessions := []session.RunningSession{
		{WindowID: 1},
		{WindowID: 2, Branch: "amx/stale", Dirty: true},
		{WindowID: 3, Branch: "amx/gone", Dirty: true},
		{WindowID: 4, Branch: "main", Dirty: true},
	}
	commands.ApplyDaemonStates(sessions, map[int]protocol.WindowStateSnapshot{
		1: {IconState: "bolt", Branch: "amx/feat-x", Dirty: true},
		2: {IconState: "bolt"},
		4: {IconState: "bolt", Dirty: true},
	})

	if sessions[0].Branch != "amx/feat-x" || !sessions[0].Dirty {
		t.Errorf("row 1 = %q/%v, want %q/true", sessions[0].Branch, sessions[0].Dirty, "amx/feat-x")
	}
	if sessions[1].Branch != "" || sessions[1].Dirty {
		t.Errorf("row 2 = %q/%v after the daemon reported an unprobed window, want \"\"/false",
			sessions[1].Branch, sessions[1].Dirty)
	}
	if sessions[2].Branch != "" || sessions[2].Dirty {
		t.Errorf("row 3 = %q/%v for a window the daemon does not know, want \"\"/false",
			sessions[2].Branch, sessions[2].Dirty)
	}
	if sessions[2].State != "" || sessions[2].Looping {
		t.Errorf("no-entry row = %+v, want State and Looping cleared alongside the git pair", sessions[2])
	}
	if sessions[3].Branch != "" || !sessions[3].Dirty {
		t.Errorf("row 4 = %q/%v for a detached-HEAD report, want \"\"/true",
			sessions[3].Branch, sessions[3].Dirty)
	}
}

// TestApplyDaemonStatesDecodesAnOlderDaemonAsUnbranched pins the Git pair across
// the wire rather than across the struct: a state response written before the
// fields existed carries neither key, and the row it overlays must come out with
// no branch to paint rather than keeping whatever it held before. That
// equivalence is what makes "no branch" a supported steady state — the same one
// a non-repository, a disabled probe, and a timeout all produce — rather than a
// version skew a client would have to detect.
func TestApplyDaemonStatesDecodesAnOlderDaemonAsUnbranched(t *testing.T) {
	t.Parallel()

	var legacy protocol.WindowStateSnapshot
	if err := json.Unmarshal([]byte(`{"icon_state":"bolt","looping":false}`), &legacy); err != nil {
		t.Fatalf("decoding a pre-branch state response failed: %v", err)
	}
	if legacy.Branch != "" || legacy.Dirty {
		t.Fatalf("decoded legacy snapshot = %q/%v, want \"\"/false", legacy.Branch, legacy.Dirty)
	}

	sessions := []session.RunningSession{{WindowID: 1, Branch: "amx/stale", Dirty: true}}
	commands.ApplyDaemonStates(sessions, map[int]protocol.WindowStateSnapshot{1: legacy})
	if sessions[0].Branch != "" || sessions[0].Dirty {
		t.Fatalf("row = %q/%v against an older daemon, want \"\"/false",
			sessions[0].Branch, sessions[0].Dirty)
	}
}

// TestApplyDaemonStatesDecodesAnOlderDaemonAsUnlabelled pins the reason across
// the wire rather than across the struct: a state response written before the
// field existed carries no `reason` key, and the row it overlays must come out
// with nothing to gloss rather than keeping whatever it held before.
func TestApplyDaemonStatesDecodesAnOlderDaemonAsUnlabelled(t *testing.T) {
	t.Parallel()

	var legacy protocol.WindowStateSnapshot
	if err := json.Unmarshal([]byte(`{"icon_state":"key","looping":false}`), &legacy); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	sessions := []session.RunningSession{{WindowID: 1, Reason: "command_approval"}}
	commands.ApplyDaemonStates(sessions, map[int]protocol.WindowStateSnapshot{1: legacy})

	if sessions[0].Reason != "" {
		t.Errorf("Reason = %q from a response with no reason key, want empty", sessions[0].Reason)
	}
}

// TestApplyDaemonStatesDecodesAnOlderDaemonAsSeen pins the inversion across the
// wire rather than across the struct: a state response written before the field
// existed carries no `unseen` key, and the row it overlays must come out seen.
// This is the property that keeps an upgrade from flooding Needs Attention.
func TestApplyDaemonStatesDecodesAnOlderDaemonAsSeen(t *testing.T) {
	t.Parallel()

	var legacy protocol.WindowStateSnapshot
	if err := json.Unmarshal([]byte(`{"icon_state":"stop","looping":false}`), &legacy); err != nil {
		t.Fatalf("json.Unmarshal() error: %v", err)
	}
	sessions := []session.RunningSession{{WindowID: 1, Unseen: true}}
	commands.ApplyDaemonStates(sessions, map[int]protocol.WindowStateSnapshot{1: legacy})

	if sessions[0].Unseen {
		t.Error("Unseen = true from a response with no unseen key, want false (seen)")
	}
}
