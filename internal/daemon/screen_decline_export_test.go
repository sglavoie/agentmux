package daemon

// The decline counter is only a health signal if it leaves the daemon. These
// tests pin the publication half of the contract: a live streak appears in the
// exported watchdog with the evidence an operator needs to diff against the
// shipped patterns, a settle and a window close both retract it, and the
// compatibility schema a v12 consumer asked for never sees it at all.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// exportedScreenDeclines reads one window's published decline snapshots back
// out through the ordinary export path rather than out of the maps, because the
// export path is what doctor and `daemon export-events` actually consume.
func exportedScreenDeclines(t *testing.T, h *Handler, windowID int) []protocol.ScreenDeclineSnapshot {
	t.Helper()
	resp := h.handleEventsExport(protocol.Request{WindowID: windowID})
	if !resp.OK {
		t.Fatalf("events export not ok: %+v", resp)
	}
	return resp.WindowMetadata[windowID].Watchdog.ScreenDeclines
}

// TestScreenDeclinePublishedThroughProbeAndRetracted drives the real probe: a
// Codex pane mid-turn is declined on every tick, and the exported watchdog has
// to grow a streak carrying the veto set and status line that caused it. The
// settle at the end is the retraction case — a stale published streak would
// read to doctor as a classifier that is still stuck.
func TestScreenDeclinePublishedThroughProbeAndRetracted(t *testing.T) {
	t.Parallel()

	base := time.Now()
	fake := &fakeKittyClient{
		windows: codexProbeWindows(42, palette.BuildTitle("W:api", "bolt", false), kitty.UserVars{
			Profile: "work",
			Harness: harness.Codex,
		}),
		getText: map[string]string{kitty.MatchWindowByID(42): codexWorkingTurnScreen},
	}
	h := newTestHandler()
	h.SetKittyClient(fake)
	codexSeedActiveBolt(t, h, 42, "sess-published", base.Unix())

	h.ProbeCodexScreenPrompts(base)
	first := exportedScreenDeclines(t, h, 42)
	if len(first) != 1 {
		t.Fatalf("published declines after one tick = %+v, want exactly one source", first)
	}
	if first[0].Source != screenPromptProbeSource {
		t.Fatalf("published source = %q, want %q", first[0].Source, screenPromptProbeSource)
	}
	if first[0].Streak != 1 || first[0].StableFingerprint {
		t.Fatalf("first published decline = %+v, want streak 1 with no stability claim", first[0])
	}
	if first[0].Fingerprint == 0 || first[0].Vetoes == "" || first[0].At == 0 {
		t.Fatalf("published decline is missing its evidence: %+v", first[0])
	}

	h.ProbeCodexScreenPrompts(base.Add(time.Second))
	second := exportedScreenDeclines(t, h, 42)
	if len(second) != 1 || second[0].Streak != 2 || !second[0].StableFingerprint {
		t.Fatalf("second published decline = %+v, want streak 2 on a stable fingerprint", second)
	}
	if second[0].Fingerprint != first[0].Fingerprint {
		t.Fatalf("static frame changed fingerprint across ticks: %d then %d", first[0].Fingerprint, second[0].Fingerprint)
	}

	// The turn completes and the stabilization run lands: the classifier has
	// just proved it can still grade this pane, so nothing may remain published.
	fake.mu.Lock()
	fake.getText[kitty.MatchWindowByID(42)] = codexCompletedTurnScreen
	fake.mu.Unlock()
	probeCodexUntilSettled(t, h, base.Add(2*time.Second))

	if got := exportedScreenDeclines(t, h, 42); len(got) != 0 {
		t.Fatalf("settle left published declines behind: %+v", got)
	}
}

// TestScreenDeclinePublicationClearedOnWindowClose covers the tombstone path.
// A closed window's metadata survives as a tombstone that doctor still reads,
// so the streak has to be retracted explicitly rather than merely stop growing.
func TestScreenDeclinePublicationClearedOnWindowClose(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	source := protocol.SourceID{Kind: protocol.SourceProbe, Key: screenPromptProbeSource}
	idleSeedHarness(t, h, 7, "cur-7", "active", time.Now().Unix())

	h.RecordScreenProbeDecline(7, source, declineEvidence(0x1111))
	h.RecordScreenProbeDecline(7, source, declineEvidence(0x1111))
	if got := exportedScreenDeclines(t, h, 7); len(got) != 1 || got[0].Streak != 2 {
		t.Fatalf("published declines before close = %+v, want a streak of 2", got)
	}

	h.pruneClosedWindows(time.Now(), map[int]bool{})

	if got := exportedScreenDeclines(t, h, 7); len(got) != 0 {
		t.Fatalf("closed window still publishes declines: %+v", got)
	}
}

// TestScreenDeclineAbsentFromSchemaV12 is the wire-compatibility guard. The
// v13 field hangs off the watchdog, which v12, v11 and v10 all still export, so
// a projection that forgot to pin the watchdog shape would leak it into three
// schemas at once.
func TestScreenDeclineAbsentFromSchemaV12(t *testing.T) {
	t.Parallel()

	payload := protocol.ExportEventsPayload{
		SchemaVersion: protocol.EventsExportSchemaVersion,
		Windows: []protocol.ExportWindow{{
			WindowID: 7,
			Watchdog: protocol.WindowDebugWatchdog{ScreenDeclines: []protocol.ScreenDeclineSnapshot{{
				Source: screenPromptProbeSource, Streak: 61, StableFingerprint: true,
				Fingerprint: 0x1111, Vetoes: "working_status_line",
			}}},
		}},
	}

	for _, schema := range []int{protocol.EventsExportSchemaV12, protocol.EventsExportSchemaV11, protocol.EventsExportSchemaV10} {
		projected, err := protocol.ProjectExportEventsPayload(payload, schema)
		if err != nil {
			t.Fatalf("project schema %d: %v", schema, err)
		}
		encoded, err := json.Marshal(projected)
		if err != nil {
			t.Fatalf("encode schema %d: %v", schema, err)
		}
		if strings.Contains(string(encoded), "screen_declines") {
			t.Fatalf("schema %d leaked the v13 decline counter:\n%s", schema, encoded)
		}
	}

	current, err := protocol.ProjectExportEventsPayload(payload, protocol.EventsExportSchemaVersion)
	if err != nil {
		t.Fatalf("project current schema: %v", err)
	}
	encoded, err := json.Marshal(current)
	if err != nil {
		t.Fatalf("encode current schema: %v", err)
	}
	if !strings.Contains(string(encoded), "screen_declines") {
		t.Fatalf("current schema dropped the decline counter:\n%s", encoded)
	}
}
