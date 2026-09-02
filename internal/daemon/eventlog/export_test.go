package eventlog

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func TestDetailOfAnUnknownWindowIsEmptyNotNil(t *testing.T) {
	detail := Detail(nil, nil)
	if detail == nil {
		t.Fatal("an unknown window must yield an empty slice, not nil")
	}
	if len(detail) != 0 {
		t.Fatalf("detail = %v; want empty", detail)
	}
}

func TestDetailDeepCopiesAndEnriches(t *testing.T) {
	var seq Sequence
	st := &state.WindowState{}
	Append(st, &seq, protocol.SessionEvent{Event: "PreToolUse", ProbeTraceIDs: []string{"probe-1"}})

	detail := Detail(st, func(event protocol.SessionEvent) protocol.SessionEvent {
		event.NotificationOutcome = "sent"
		return event
	})

	if len(detail) != 1 || detail[0].NotificationOutcome != "sent" {
		t.Fatalf("detail = %+v; want the enrichment applied", detail)
	}
	if st.EventLog[0].NotificationOutcome != "" {
		t.Fatal("enrichment must not be retained in the ring")
	}

	detail[0].ProbeTraceIDs[0] = "mutated"
	detail[0].Event = "mutated"
	if st.EventLog[0].ProbeTraceIDs[0] != "probe-1" || st.EventLog[0].Event != "PreToolUse" {
		t.Fatal("a caller mutating the projection must not corrupt the ring")
	}
}

func TestTrimLastKeepsTheTailAndNeverAliases(t *testing.T) {
	rows := []protocol.SessionEvent{{At: 1}, {At: 2}, {At: 3}}

	all := TrimLast(rows, 0)
	if len(all) != 3 {
		t.Fatalf("TrimLast(rows, 0) = %v; want everything", all)
	}
	all[0].At = 99
	if rows[0].At != 1 {
		t.Fatal("TrimLast must return a fresh slice")
	}

	tail := TrimLast(rows, 2)
	if len(tail) != 2 || tail[0].At != 2 || tail[1].At != 3 {
		t.Fatalf("TrimLast(rows, 2) = %v; want the last two rows", tail)
	}

	if got := TrimLast(rows, 10); len(got) != 3 {
		t.Fatalf("TrimLast(rows, 10) = %v; want everything when the tail exceeds the rows", got)
	}
}

func TestCloneWindowMetadataDeepCopiesEveryReachableContainer(t *testing.T) {
	in := protocol.WindowDebugMetadata{}
	in.Stats.SuppressionReasons = map[string]int{"guard_blocked": 1}
	in.Shadow.Mismatches = []protocol.ShadowMismatch{{LegacyIcon: "bolt"}}
	in.Watchdog.ScreenDeclines = []protocol.ScreenDeclineSnapshot{{Streak: 2}}
	in.Renderer.Generations = []protocol.RenderGenerationTrace{{Generation: 1, TraceIDs: []string{"trace-1"}}}

	out := CloneWindowMetadata(in)

	out.Stats.SuppressionReasons["guard_blocked"] = 99
	out.Shadow.Mismatches[0].LegacyIcon = "mutated"
	out.Watchdog.ScreenDeclines[0].Streak = 99
	out.Renderer.Generations[0].TraceIDs[0] = "mutated"

	if in.Stats.SuppressionReasons["guard_blocked"] != 1 {
		t.Fatal("suppression histogram is shared with the clone")
	}
	if in.Shadow.Mismatches[0].LegacyIcon != "bolt" {
		t.Fatal("shadow mismatches are shared with the clone")
	}
	if in.Watchdog.ScreenDeclines[0].Streak != 2 {
		t.Fatal("screen declines are shared with the clone")
	}
	if in.Renderer.Generations[0].TraceIDs[0] != "trace-1" {
		t.Fatal("render generation traces are shared with the clone")
	}
}
