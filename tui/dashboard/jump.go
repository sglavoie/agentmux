package dashboard

import "github.com/sglavoie/agentmux/internal/session"

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

// FindNextMatching returns the index in rows of the next row whose State
// passes match, walking from index `from` in direction `dir` (+1 forward,
// -1 backward), wrapping exactly once. Returns -1 if no row matches or
// rows is empty. If the row at `from` matches, it is skipped — the
// function never returns `from` itself unless `from` is out of range.
func FindNextMatching(rows []session.RunningSession, from, dir int, match func(string) bool) int {
	n := len(rows)
	if n == 0 || (dir != 1 && dir != -1) {
		return -1
	}
	for step := 1; step <= n; step++ {
		i := ((from+dir*step)%n + n) % n
		if match(rows[i].State) {
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
