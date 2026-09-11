package dashboard

import (
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

// defaultAttentionStates is the canonical fallback set used when the caller
// has no configured attention set (only happens for a dashboard built without
// a config). In production the root populates its set from
// cfg.Defaults.Jump.AttentionStates, which applyDefaults fills with the same
// set when the user does not override it.
//
// stop is deliberately absent, and stayed absent when StateGroup learned to put
// an UNSEEN stop under Needs Attention. Jump is keyed by state name, from a
// user-editable config list; "an unseen stop" is not a state name, so honoring
// it here would mean widening the whole matcher from func(string) bool to a
// predicate over the row — a signature change the grouping work explicitly did
// not need. The rows stay reachable regardless: the ]s chord targets every
// stop, seen or not, and an unseen stop now sorts into the top group, which is
// where the cursor already starts. Jump answers "take me to the next one";
// seenness decides where a row sits, not whether ]a stops on it.
var defaultAttentionStates = map[string]bool{
	"key":      true,
	"error":    true,
	"warning":  true,
	"question": true,
}

// FindNextMatching returns the index in the projection of the next item whose
// state passes match, walking from index `from` in direction `dir` (+1 forward,
// -1 backward), wrapping exactly once. Returns -1 if no item matches or the
// projection is empty. If the item at `from` matches, it is skipped — the
// function never returns `from` itself unless `from` is out of range.
//
// A non-session item reports the empty state, which is in no chord set and in
// no attention set, so jump navigation walks past a synthetic row without the
// matchers needing to know that item kinds exist.
func FindNextMatching(list ItemList, from, dir int, match func(string) bool) int {
	n := list.Len()
	if n == 0 || (dir != 1 && dir != -1) {
		return -1
	}
	for step := 1; step <= n; step++ {
		i := ((from+dir*step)%n + n) % n
		if match(list.StateAt(i)) {
			return i
		}
	}
	return -1
}

// AttentionMatcher returns a predicate closed over the configured states.
// When states is nil or empty, the predicate falls back to the canonical
// attention set so a dashboard built without a config still jumps sensibly.
func AttentionMatcher(states map[string]bool) func(string) bool {
	if len(states) == 0 {
		return func(s string) bool { return defaultAttentionStates[s] }
	}
	return func(s string) bool { return states[s] }
}

// chordStateSets maps a follow-up keystroke (after ]/[) to the set of states
// that the cursor should jump to. The map is fixed regardless of the
// user-configured attention set — chords target specific states by user
// choice, independent of [defaults.jump].attention_states.
var chordStateSets = map[string]map[string]bool{
	"p": {"key": true},
	"e": {"error": true, "warning": true},
	"a": {"bolt": true},
	"q": {"question": true},
	"s": {"stop": true},
	"c": {"compact": true},
	"b": {"subagent": true},
}

// ChordStateMatcher returns a predicate matching the state set bound to key,
// or nil when key is not a recognized chord follow-up.
func ChordStateMatcher(key string) func(string) bool {
	set, ok := chordStateSets[key]
	if !ok {
		return nil
	}
	return func(s string) bool { return set[s] }
}

// Needs-you navigation. Everything below is a SECOND navigator, deliberately not
// a widening of FindNextMatching, and the two differ in both halves of what a
// jump is.
//
// What it looks for. FindNextMatching tests a state NAME against a user-editable
// config set, which is how `]]` came to include recoverable tool warnings (a
// `warning` row sits in Active and needs nothing) and to exclude unread
// completions (an unseen stop is in Needs you, but "an unseen stop" is not a
// state name and cannot be spelled in the config list). NeedsYou asks StateGroup
// instead, so the navigator and the bucket the row is drawn under are the same
// judgement rather than two restatements that have already drifted apart once.
//
// Where it looks. FindNextMatching walks the projection's visible lines, so a row
// folded inside a collapsed family is not merely skipped — it is invisible to the
// navigation, and the Projects view can hold a session that needs the operator
// with no key that reaches it. This navigator walks the filtered underlying
// SESSIONS, in the order the projection presents them, and answers with a
// RowTarget that names the family to unfold when the row it found has no line of
// its own.
//
// Both survive: the chord matchers still target specific states by name, which is
// a different and still-wanted question ("take me to the next compaction").

// NeedsYou reports whether a row belongs in the Needs you queue — the same
// question StateGroup answers, named for what it means so a navigator, a count,
// and a heading cannot each re-derive it.
//
// It is the definition, not a copy of it: blocked prompts, elicitations, terminal
// failures and UNREAD completions are in; recoverable tool warnings, working
// rows, seen completions and quiet rows are out.
func NeedsYou(s session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) bool {
	return StateGroup(s, lastActivity, threshold) == attentionGroup
}

// FindNextNeedsYou walks the filtered sessions from the line at `from` in
// direction dir (+1 forward, -1 backward), wrapping exactly once, and returns the
// target that reaches the first row needing the operator.
//
// The cursor's line maps to a SPAN of the session walk rather than to a single
// position, which is what makes the two summary kinds behave the way the screen
// says they should. A collapsed summary spans every member it hides, because on
// screen it IS those sessions: a forward jump from it goes to the next family,
// not into the one already selected. An expanded summary spans nothing and sits
// immediately before its first member, because it is only a heading: a forward
// jump from it may land on its own first child, which is the next line down.
//
// When the only row that needs the operator is the one the cursor already covers,
// the walk wraps back onto it and returns it — the same "wraps to itself"
// behavior FindNextMatching has. For a collapsed family that is not a no-op: the
// target comes back with Reveal set, so the keystroke unfolds the family and
// shows the operator the row that was being summarized.
//
// Reports false when nothing needs the operator, when the projection is empty,
// and for a dir that is neither +1 nor -1. Nothing here reads a session's
// terminal or acknowledges anything: this decides where to go, and no more.
func FindNextNeedsYou(list ItemList, from, dir int, lastActivity map[int]time.Time, threshold time.Duration) (RowTarget, bool) {
	if dir != 1 && dir != -1 {
		return RowTarget{}, false
	}
	walk := newSessionWalk(list)
	n := len(walk.rows)
	if n == 0 {
		return RowTarget{}, false
	}
	start, end := walk.spanFor(from)
	for step := 1; step <= n; step++ {
		var at int
		if dir == 1 {
			at = ((end+step)%n + n) % n
		} else {
			at = ((start-step)%n + n) % n
		}
		row := walk.rows[at]
		if NeedsYou(list.Rows[row], lastActivity, threshold) {
			return list.ResolveRowTarget(row)
		}
	}
	return RowTarget{}, false
}

// sessionWalk is the projection seen as the ordered sequence of sessions it
// presents, with each line's position in that sequence recorded beside it.
//
// It is built per jump rather than cached: a projection is rebuilt on every
// refresh, and a stale walk would navigate by the previous frame's folds.
type sessionWalk struct {
	// rows holds row indices in presentation order. Every filtered session appears
	// exactly once — as its own visible leaf, or, when its family is folded, at the
	// position its summary occupies.
	rows []int
	// spanStart and spanEnd are indexed by ITEM index and bound the stretch of rows
	// each line stands for. A line standing for no row at all (an expanded summary)
	// records spanEnd == spanStart-1, an empty span pinned to the position its
	// first member will take.
	spanStart []int
	spanEnd   []int
}

func newSessionWalk(list ItemList) sessionWalk {
	walk := sessionWalk{
		rows:      make([]int, 0, len(list.Rows)),
		spanStart: make([]int, len(list.Items)),
		spanEnd:   make([]int, len(list.Items)),
	}
	for i, it := range list.Items {
		walk.spanStart[i] = len(walk.rows)
		switch {
		case it.Kind == ItemSession:
			if it.Row >= 0 && it.Row < len(list.Rows) {
				walk.rows = append(walk.rows, it.Row)
			}
		case it.Collapsed:
			// A folded family's members are walked at the summary's position. Every
			// member index is checked rather than trusted, exactly as GroupAt and
			// SummaryCounts check theirs: the summary and the row set travel as
			// separate fields and a caller may pair them wrongly.
			for _, member := range it.Members {
				if member >= 0 && member < len(list.Rows) {
					walk.rows = append(walk.rows, member)
				}
			}
		}
		walk.spanEnd[i] = len(walk.rows) - 1
	}
	return walk
}

// spanFor returns the row positions the line at item index i stands for. An
// out-of-range index — no selection yet, or a cursor left over from a shorter
// list — answers the empty span before the first row, so a forward walk starts at
// the top and a backward walk starts by wrapping to the bottom. That is
// FindNextMatching's behavior for an out-of-range `from`, kept so the two
// navigators do not disagree about a cursor neither of them can place.
func (w sessionWalk) spanFor(i int) (start, end int) {
	if i < 0 || i >= len(w.spanStart) {
		return 0, -1
	}
	return w.spanStart[i], w.spanEnd[i]
}
