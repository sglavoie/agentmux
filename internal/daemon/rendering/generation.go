package rendering

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/daemon/state"
)

// maxGenerationTraces bounds the per-window render generation ring kept for
// export. It is the display model's depth, not a retention policy: older
// generations have already been journalled.
const maxGenerationTraces = 16

// CloneTrace deep-copies a render generation trace so a caller can hold it
// after releasing the lock that guards the window's state.
func CloneTrace(in protocol.RenderGenerationTrace) protocol.RenderGenerationTrace {
	out := in
	out.SourceGenerations = append([]uint64(nil), in.SourceGenerations...)
	out.TraceIDs = append([]string(nil), in.TraceIDs...)
	out.RequestIDs = append([]string(nil), in.RequestIDs...)
	out.SourceEventIDs = append([]string(nil), in.SourceEventIDs...)
	return out
}

// CloneTraces deep-copies a window's render generation ring for export.
func CloneTraces(in []protocol.RenderGenerationTrace) []protocol.RenderGenerationTrace {
	out := make([]protocol.RenderGenerationTrace, len(in))
	for i := range in {
		out[i] = CloneTrace(in[i])
	}
	return out
}

// AppendCausalID adds an identity to one of the bounded, de-duplicated causal
// lists a render generation exports (trace, request, and source event IDs),
// keeping the most recent protocol.MaxRenderCausalIDs entries. Probe-child
// links use it too: they feed the same bounded evidence lists in the export.
func AppendCausalID(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	values = append(values, value)
	if len(values) > protocol.MaxRenderCausalIDs {
		values = append([]string(nil), values[len(values)-protocol.MaxRenderCausalIDs:]...)
	}
	return values
}

// appendSourceGeneration is AppendCausalID for the state generations coalesced
// into one render generation.
func appendSourceGeneration(values []uint64, value uint64) []uint64 {
	if value == 0 {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	values = append(values, value)
	if len(values) > protocol.MaxRenderCausalIDs {
		values = append([]uint64(nil), values[len(values)-protocol.MaxRenderCausalIDs:]...)
	}
	return values
}

// traceForGeneration returns the window's trace for generation, seeding a
// pending dirty-stage row when the generation is new. Callers hold the lock
// guarding st.
func traceForGeneration(st *state.WindowState, generation uint64) *protocol.RenderGenerationTrace {
	for i := range st.RenderGenerations {
		if st.RenderGenerations[i].Generation == generation {
			return &st.RenderGenerations[i]
		}
	}
	st.RenderGenerations = append(st.RenderGenerations, protocol.RenderGenerationTrace{
		Generation:        generation,
		SourceGenerations: []uint64{generation},
		Stage:             stageDirty,
		Outcome:           "pending",
		UpdatedAt:         time.Now().UnixNano(),
	})
	if len(st.RenderGenerations) > maxGenerationTraces {
		st.RenderGenerations = append([]protocol.RenderGenerationTrace(nil), st.RenderGenerations[len(st.RenderGenerations)-maxGenerationTraces:]...)
	}
	return &st.RenderGenerations[len(st.RenderGenerations)-1]
}

// LinkCausalIDs attaches one event's causal identities to the render
// generation it will be presented by. Callers hold the lock guarding st.
func LinkCausalIDs(st *state.WindowState, generation uint64, traceID, requestID, sourceEventID string) {
	if st == nil || generation == 0 {
		return
	}
	trace := traceForGeneration(st, generation)
	trace.SourceGenerations = appendSourceGeneration(trace.SourceGenerations, generation)
	trace.TraceIDs = AppendCausalID(trace.TraceIDs, traceID)
	trace.RequestIDs = AppendCausalID(trace.RequestIDs, requestID)
	trace.SourceEventIDs = AppendCausalID(trace.SourceEventIDs, sourceEventID)
	trace.UpdatedAt = time.Now().UnixNano()
}

// mergeTrace folds src's causal evidence into dst, which is the newer
// generation absorbing an older, still-unrendered one.
func mergeTrace(dst *protocol.RenderGenerationTrace, src protocol.RenderGenerationTrace) {
	for _, generation := range src.SourceGenerations {
		dst.SourceGenerations = appendSourceGeneration(dst.SourceGenerations, generation)
	}
	dst.SourceGenerations = appendSourceGeneration(dst.SourceGenerations, src.Generation)
	for _, id := range src.TraceIDs {
		dst.TraceIDs = AppendCausalID(dst.TraceIDs, id)
	}
	for _, id := range src.RequestIDs {
		dst.RequestIDs = AppendCausalID(dst.RequestIDs, id)
	}
	for _, id := range src.SourceEventIDs {
		dst.SourceEventIDs = AppendCausalID(dst.SourceEventIDs, id)
	}
}

// MarkStage advances a window's render generation to stage and returns a copy
// of the resulting trace. With coalesce set, every older non-terminal
// generation is absorbed into this one, so a superseded generation's causal
// evidence stays attached to the pass that actually presents it. Callers hold
// the lock guarding st.
func MarkStage(st *state.WindowState, generation uint64, stage, outcome, reason string, coalesce bool) protocol.RenderGenerationTrace {
	if st == nil || generation == 0 {
		return protocol.RenderGenerationTrace{}
	}
	if coalesce {
		target := protocol.RenderGenerationTrace{
			Generation: generation,
			Stage:      stageDirty, Outcome: "pending",
		}
		kept := make([]protocol.RenderGenerationTrace, 0, len(st.RenderGenerations)+1)
		for _, existing := range st.RenderGenerations {
			if existing.Generation <= generation && !stageTerminal(existing) {
				mergeTrace(&target, existing)
				continue
			}
			kept = append(kept, existing)
		}
		target.SourceGenerations = appendSourceGeneration(target.SourceGenerations, generation)
		kept = append(kept, target)
		st.RenderGenerations = kept
	}
	trace := traceForGeneration(st, generation)
	trace.Stage = stage
	trace.Outcome = outcome
	trace.Reason = reason
	trace.UpdatedAt = time.Now().UnixNano()
	if len(st.RenderGenerations) > maxGenerationTraces {
		st.RenderGenerations = append([]protocol.RenderGenerationTrace(nil), st.RenderGenerations[len(st.RenderGenerations)-maxGenerationTraces:]...)
		trace = &st.RenderGenerations[len(st.RenderGenerations)-1]
	}
	return CloneTrace(*trace)
}

// FormatSourceGenerations renders a generation's coalesced source generations
// as the comma-separated evidence value the renderer journals.
func FormatSourceGenerations(values []uint64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprint(value))
	}
	return strings.Join(parts, ",")
}
