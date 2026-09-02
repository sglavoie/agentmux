package rendering

import (
	"fmt"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

func TestAppendCausalIDDeduplicatesTrimsAndIgnoresBlanks(t *testing.T) {
	var ids []string
	ids = AppendCausalID(ids, "  t-1  ")
	ids = AppendCausalID(ids, "t-1")
	ids = AppendCausalID(ids, "   ")
	ids = AppendCausalID(ids, "")
	if len(ids) != 1 || ids[0] != "t-1" {
		t.Fatalf("ids = %v, want [t-1]", ids)
	}
}

func TestAppendCausalIDKeepsTheMostRecentBoundedWindow(t *testing.T) {
	var ids []string
	total := protocol.MaxRenderCausalIDs + 5
	for i := 0; i < total; i++ {
		ids = AppendCausalID(ids, fmt.Sprintf("t-%d", i))
	}
	if len(ids) != protocol.MaxRenderCausalIDs {
		t.Fatalf("len(ids) = %d, want %d", len(ids), protocol.MaxRenderCausalIDs)
	}
	if ids[0] != fmt.Sprintf("t-%d", total-protocol.MaxRenderCausalIDs) {
		t.Fatalf("ids[0] = %q, want the oldest retained ID", ids[0])
	}
	if ids[len(ids)-1] != fmt.Sprintf("t-%d", total-1) {
		t.Fatalf("ids[last] = %q, want the newest ID", ids[len(ids)-1])
	}
}

func TestLinkCausalIDsSeedsAPendingDirtyGeneration(t *testing.T) {
	st := &state.WindowState{}
	LinkCausalIDs(st, 3, "t-1", "r-1", "s-1")
	LinkCausalIDs(st, 3, "t-2", "r-1", "")

	if len(st.RenderGenerations) != 1 {
		t.Fatalf("RenderGenerations = %+v, want one generation", st.RenderGenerations)
	}
	trace := st.RenderGenerations[0]
	if trace.Generation != 3 || trace.Stage != stageDirty || trace.Outcome != "pending" {
		t.Fatalf("trace = %+v, want generation 3 pending/dirty", trace)
	}
	if len(trace.TraceIDs) != 2 || len(trace.RequestIDs) != 1 || len(trace.SourceEventIDs) != 1 {
		t.Fatalf("trace causal lists = %+v, want 2 trace IDs and 1 each of request/source", trace)
	}
	if len(trace.SourceGenerations) != 1 || trace.SourceGenerations[0] != 3 {
		t.Fatalf("SourceGenerations = %v, want [3]", trace.SourceGenerations)
	}
}

func TestLinkCausalIDsIgnoresAnUnknownGeneration(t *testing.T) {
	st := &state.WindowState{}
	LinkCausalIDs(st, 0, "t-1", "r-1", "s-1")
	LinkCausalIDs(nil, 1, "t-1", "r-1", "s-1")
	if len(st.RenderGenerations) != 0 {
		t.Fatalf("RenderGenerations = %+v, want none", st.RenderGenerations)
	}
}

func TestMarkStageCoalescesOlderUnrenderedGenerations(t *testing.T) {
	st := &state.WindowState{}
	LinkCausalIDs(st, 1, "t-1", "r-1", "s-1")
	LinkCausalIDs(st, 2, "t-2", "r-2", "s-2")

	trace := MarkStage(st, 3, StageQueued, "pending", "latest_generation", true)
	if len(st.RenderGenerations) != 1 {
		t.Fatalf("RenderGenerations = %+v, want the three generations coalesced into one", st.RenderGenerations)
	}
	if trace.Generation != 3 || trace.Stage != StageQueued || trace.Reason != "latest_generation" {
		t.Fatalf("trace = %+v, want the queued generation 3", trace)
	}
	// The superseded generations' causal evidence rides along on the pass that
	// actually presents them.
	if got := len(trace.TraceIDs); got != 2 {
		t.Fatalf("TraceIDs = %v, want the two superseded trace IDs", trace.TraceIDs)
	}
	if got := len(trace.SourceGenerations); got != 3 {
		t.Fatalf("SourceGenerations = %v, want 1, 2 and 3", trace.SourceGenerations)
	}
}

func TestMarkStageLeavesRenderedGenerationsAlone(t *testing.T) {
	st := &state.WindowState{}
	MarkStage(st, 1, StageRendered, "success", "render_complete", false)
	// A failed generation stays retryable, so it may still coalesce forward.
	MarkStage(st, 2, StageFailed, "failed", "render_title", false)

	trace := MarkStage(st, 3, StageQueued, "pending", "latest_generation", true)
	if len(st.RenderGenerations) != 2 {
		t.Fatalf("RenderGenerations = %+v, want the rendered row kept beside the coalesced one", st.RenderGenerations)
	}
	if st.RenderGenerations[0].Generation != 1 || st.RenderGenerations[0].Stage != StageRendered {
		t.Fatalf("first row = %+v, want the untouched rendered generation 1", st.RenderGenerations[0])
	}
	if len(trace.SourceGenerations) != 2 {
		t.Fatalf("SourceGenerations = %v, want the failed generation 2 folded into 3", trace.SourceGenerations)
	}
}

func TestMarkStageBoundsTheExportedGenerationRing(t *testing.T) {
	st := &state.WindowState{}
	for generation := uint64(1); generation <= maxGenerationTraces+8; generation++ {
		MarkStage(st, generation, StageRendered, "success", "render_complete", false)
	}
	if len(st.RenderGenerations) != maxGenerationTraces {
		t.Fatalf("len(RenderGenerations) = %d, want %d", len(st.RenderGenerations), maxGenerationTraces)
	}
	newest := st.RenderGenerations[len(st.RenderGenerations)-1]
	if newest.Generation != maxGenerationTraces+8 {
		t.Fatalf("newest generation = %d, want %d", newest.Generation, maxGenerationTraces+8)
	}
}

func TestMarkStageIgnoresAnUnknownGeneration(t *testing.T) {
	st := &state.WindowState{}
	if trace := MarkStage(st, 0, StageQueued, "pending", "latest_generation", true); trace.Generation != 0 {
		t.Fatalf("trace = %+v, want the zero trace", trace)
	}
	if trace := MarkStage(nil, 1, StageQueued, "pending", "latest_generation", true); trace.Generation != 0 {
		t.Fatalf("trace = %+v, want the zero trace", trace)
	}
	if len(st.RenderGenerations) != 0 {
		t.Fatalf("RenderGenerations = %+v, want none", st.RenderGenerations)
	}
}

func TestMarkStageReturnsACopyDetachedFromTheWindow(t *testing.T) {
	st := &state.WindowState{}
	LinkCausalIDs(st, 1, "t-1", "r-1", "s-1")
	trace := MarkStage(st, 1, StageQueued, "pending", "latest_generation", false)

	trace.TraceIDs[0] = "mutated"
	trace.SourceGenerations[0] = 99
	if st.RenderGenerations[0].TraceIDs[0] != "t-1" || st.RenderGenerations[0].SourceGenerations[0] != 1 {
		t.Fatalf("mutating the returned trace reached the window: %+v", st.RenderGenerations[0])
	}
}

func TestCloneTracesDeepCopiesTheDisplayModel(t *testing.T) {
	in := []protocol.RenderGenerationTrace{{
		Generation:        1,
		SourceGenerations: []uint64{1},
		TraceIDs:          []string{"t-1"},
		RequestIDs:        []string{"r-1"},
		SourceEventIDs:    []string{"s-1"},
	}}
	out := CloneTraces(in)
	out[0].TraceIDs[0] = "mutated"
	out[0].RequestIDs[0] = "mutated"
	out[0].SourceEventIDs[0] = "mutated"
	out[0].SourceGenerations[0] = 99
	if in[0].TraceIDs[0] != "t-1" || in[0].RequestIDs[0] != "r-1" ||
		in[0].SourceEventIDs[0] != "s-1" || in[0].SourceGenerations[0] != 1 {
		t.Fatalf("CloneTraces shared backing arrays: %+v", in[0])
	}
}

func TestFormatSourceGenerations(t *testing.T) {
	if got := FormatSourceGenerations(nil); got != "" {
		t.Fatalf("FormatSourceGenerations(nil) = %q, want empty", got)
	}
	if got := FormatSourceGenerations([]uint64{3, 1, 2}); got != "3,1,2" {
		t.Fatalf("FormatSourceGenerations = %q, want 3,1,2", got)
	}
}
