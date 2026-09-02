package daemon

import (
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/palette"
)

// TestProbeCopilotTranscriptStop_CompletedTurnStopsStuckBolt drives the
// native-hook-loss recovery through the authoritative-idle path: a terminal
// (assistant.turn_end) transcript settles the stale interactive bolt, converges
// the tab to 🛑, and records a TranscriptStopProbe row carrying the ACTUAL
// reduced state under the copilot_transcript probe source.
func TestProbeCopilotTranscriptStop_CompletedTurnStopsStuckBolt(t *testing.T) {
	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotTurnEndTranscript)
	h, fake := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	settleCopilotTurnEndProbe(h, base)

	calls := fake.titleCalls()
	if len(calls) != 1 {
		t.Fatalf("SetTabTitle calls = %d, want 1", len(calls))
	}
	if want := palette.BuildTitle("W:api", "stop", false); calls[0].title != want {
		t.Fatalf("SetTabTitle title = %q, want %q", calls[0].title, want)
	}
	if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
		t.Fatalf("effective = %+v visible %q, want stop/stop", eff, visible)
	}

	// The stale interactive bolt fact was retired, so it can no longer mask 🛑.
	h.stateMu.Lock()
	old := h.store[42].Reducer.facts[protocol.SourceID{Kind: protocol.SourceHarness, Key: "copilot:sess-1"}]
	h.stateMu.Unlock()
	if old == nil || !old.acked {
		t.Fatalf("stale interactive bolt was not retired: %+v", old)
	}

	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent)
	if !ok {
		t.Fatalf("no %q row in %+v", copilotTranscriptStopProbeEvent, resp.Events)
	}
	if ev.Action != "stop" {
		t.Fatalf("TranscriptStopProbe action = %q, want stop", ev.Action)
	}
	if ev.Source != string(protocol.SourceProbe)+":"+copilotTranscriptStopProbeSource {
		t.Fatalf("TranscriptStopProbe source = %q, want %q", ev.Source, string(protocol.SourceProbe)+":"+copilotTranscriptStopProbeSource)
	}
}

// TestProbeCopilotTranscriptStop_UserAbortStopsStuckBolt is the interruption
// twin: a user-initiated abort record is terminal evidence and settles the same
// way as a clean completion.
func TestProbeCopilotTranscriptStop_UserAbortStopsStuckBolt(t *testing.T) {
	home := t.TempDir()
	writeCopilotTranscript(t, home, "sess-1", copilotAbortTranscript)
	h, fake := copilotProbeHandler(t, 42, "bolt", home)
	base := time.Now()
	copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

	h.ProbeCopilotTranscriptStop(base)

	if calls := fake.titleCalls(); len(calls) != 1 || calls[0].title != palette.BuildTitle("W:api", "stop", false) {
		t.Fatalf("SetTabTitle calls = %+v, want one stop title", calls)
	}
	if eff, _ := windowEffective(t, h, 42); eff.Icon != "stop" {
		t.Fatalf("effective = %+v, want stop after user abort", eff)
	}
	resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
	if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); !ok || ev.Action != "stop" {
		t.Fatalf("TranscriptStopProbe row = %+v ok=%v, want stop", ev, ok)
	}
}

// TestProbeCopilotTranscriptStop_NonTerminalEvidenceKeepsBolt walks the
// fail-closed evidence shapes the probe must never settle: ongoing activity, a
// non-state-bearing tail (subagent task_complete only), a missing transcript,
// and a malformed tail. Each leaves the stuck ⚡ untouched and records no
// TranscriptStopProbe row.
func TestProbeCopilotTranscriptStop_NonTerminalEvidenceKeepsBolt(t *testing.T) {
	cases := []struct {
		name    string
		content string
		write   bool
	}{
		{name: "active later turn", content: copilotActiveTranscript, write: true},
		{name: "open subagent span turn_end", content: copilotOpenSubagentTranscript, write: true},
		{name: "no state-bearing event", content: copilotSubagentTranscript, write: true},
		{name: "malformed tail", content: copilotMalformedTranscript, write: true},
		{name: "missing transcript", write: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if tc.write {
				writeCopilotTranscript(t, home, "sess-1", tc.content)
			}
			h, fake := copilotProbeHandler(t, 42, "bolt", home)
			base := time.Now()
			copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

			h.ProbeCopilotTranscriptStop(base)

			if got := len(fake.titleCalls()); got != 0 {
				t.Fatalf("SetTabTitle calls = %d, want 0", got)
			}
			if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
				t.Fatalf("effective = %+v, want bolt preserved", eff)
			}
			resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
			if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok {
				t.Fatalf("TranscriptStopProbe row recorded for %s: %+v", tc.name, ev)
			}
		})
	}
}

// TestProbeCopilotTranscriptStop_SubagentSpanBeganOutsideTail proves the
// tail-boundary repair end to end. When the opening subagent.started scrolled
// off the top of the 256 KiB tail, the probe must NOT settle the parent bolt on
// the nested assistant.turn_end (the span is reconstructed from the unmatched
// subagent.completed). Once a genuine main assistant.turn_end follows the
// reconstructed span's close, the probe settles stop as usual.
func TestProbeCopilotTranscriptStop_SubagentSpanBeganOutsideTail(t *testing.T) {
	t.Run("open span keeps bolt", func(t *testing.T) {
		home := t.TempDir()
		writeCopilotTranscript(t, home, "sess-1", copilotSpanBeforeTailTranscript(true))
		h, fake := copilotProbeHandler(t, 42, "bolt", home)
		base := time.Now()
		copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

		h.ProbeCopilotTranscriptStop(base)

		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d, want 0 (nested turn_end inside off-tail span)", got)
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective = %+v, want bolt preserved", eff)
		}
		resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
		if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok {
			t.Fatalf("TranscriptStopProbe row recorded for an open off-tail span: %+v", ev)
		}
	})

	t.Run("still-open span beyond window keeps bolt", func(t *testing.T) {
		// The span never closes and its opening subagent.started scrolled off the
		// top of the reader's whole window with no visible anchor, so the reader
		// fails closed. The probe must NOT settle the parent bolt on the nested
		// assistant.turn_end and must record no TranscriptStopProbe.
		home := t.TempDir()
		writeCopilotTranscript(t, home, "sess-1", copilotOpenSpanBeyondWindowTranscript())
		h, fake := copilotProbeHandler(t, 42, "bolt", home)
		base := time.Now()
		copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

		h.ProbeCopilotTranscriptStop(base)

		if got := len(fake.titleCalls()); got != 0 {
			t.Fatalf("SetTabTitle calls = %d, want 0 (still-open span beyond the window)", got)
		}
		if eff, _ := windowEffective(t, h, 42); eff.Icon != "bolt" {
			t.Fatalf("effective = %+v, want bolt preserved", eff)
		}
		resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
		if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); ok {
			t.Fatalf("TranscriptStopProbe row recorded for a still-open off-window span: %+v", ev)
		}
	})

	t.Run("main terminal after span closes settles stop", func(t *testing.T) {
		home := t.TempDir()
		writeCopilotTranscript(t, home, "sess-1", copilotSpanBeforeTailTranscript(false))
		h, fake := copilotProbeHandler(t, 42, "bolt", home)
		base := time.Now()
		copilotSeedActiveBolt(t, h, 42, "sess-1", base.Unix())

		settleCopilotTurnEndProbe(h, base)

		if calls := fake.titleCalls(); len(calls) != 1 || calls[0].title != palette.BuildTitle("W:api", "stop", false) {
			t.Fatalf("SetTabTitle calls = %+v, want one stop title", calls)
		}
		if eff, visible := windowEffective(t, h, 42); eff.Icon != "stop" || visible != "stop" {
			t.Fatalf("effective = %+v visible %q, want stop/stop after span closes", eff, visible)
		}
		resp := h.Handle(protocol.Request{Type: protocol.TypeSessionDetail, WindowID: 42})
		if ev, ok := lastEventNamed(resp.Events, copilotTranscriptStopProbeEvent); !ok || ev.Action != "stop" {
			t.Fatalf("TranscriptStopProbe row = %+v ok=%v, want stop", ev, ok)
		}
	})
}
