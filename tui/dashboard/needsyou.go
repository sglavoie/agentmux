// This file is the dashboard's one navigator: `n`/`N`, which walks the filtered
// sessions to the next row that needs the operator.
//
// It replaced a state-name matcher (`]`/`[` chords, `]]`/`[[`, `}`/`{`) that
// differed from it in both halves of what a jump is, and the differences are why
// only one of them survives.
//
// What it looks for. The matcher tested a state NAME against a user-editable
// config set, which is how `]]` came to include recoverable tool warnings (a
// `warning` row sits in Active and needs nothing) and to exclude unread
// completions (an unseen stop is in Needs you, but "an unseen stop" is not a
// state name and cannot be spelled in a config list at all). NeedsYou asks
// StateGroup instead, so the navigator and the bucket the row is drawn under are
// the same judgement rather than two restatements that had already drifted.
//
// Where it looks. The matcher walked the projection's visible lines, so a row
// folded inside a collapsed family was not merely skipped — it was invisible to
// the navigation, and the Projects view could hold a session that needed the
// operator with no key that reached it. This navigator walks the filtered
// underlying SESSIONS, in the order the projection presents them, and answers
// with a RowTarget that names the family to unfold when the row it found has no
// line of its own.

package dashboard

import (
	"time"

	"github.com/sglavoie/agentmux/internal/session"
)

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
// the walk wraps back onto it and returns it. For a collapsed family that is not
// a no-op: the target comes back with Reveal set, so the keystroke unfolds the
// family and shows the operator the row that was being summarized.
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
// the top and a backward walk starts by wrapping to the bottom.
func (w sessionWalk) spanFor(i int) (start, end int) {
	if i < 0 || i >= len(w.spanStart) {
		return 0, -1
	}
	return w.spanStart[i], w.spanEnd[i]
}
