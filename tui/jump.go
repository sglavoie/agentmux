package tui

import "github.com/sglavoie/agentmux/internal/session"

// defaultAttentionStates is the canonical fallback set used when the Model
// is constructed without a config (only happens in tests that build Model
// literals directly). In production, NewModel populates Model.attentionStates
// from cfg.Defaults.Jump.AttentionStates, which applyDefaults fills with
// the same set when the user does not override it.
var defaultAttentionStates = map[string]bool{
	"key":      true,
	"error":    true,
	"warning":  true,
	"question": true,
}

// findNextMatching returns the index in rows of the next row whose State
// passes match, walking from index `from` in direction `dir` (+1 forward,
// -1 backward), wrapping exactly once. Returns -1 if no row matches or
// rows is empty. If the row at `from` matches, it is skipped — the
// function never returns `from` itself unless `from` is out of range.
func findNextMatching(rows []session.RunningSession, from, dir int, match func(string) bool) int {
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

// isAttentionStateFor returns a predicate closed over the configured states.
// When states is nil or empty, the predicate falls back to defaultAttentionStates
// so tests that build a Model without a config still get sensible behavior.
func isAttentionStateFor(states map[string]bool) func(string) bool {
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

// chordStateMatcher returns a predicate matching the state set bound to key,
// or nil when key is not a recognized chord follow-up.
func chordStateMatcher(key string) func(string) bool {
	set, ok := chordStateSets[key]
	if !ok {
		return nil
	}
	return func(s string) bool { return set[s] }
}

// clearJumpChord resets any in-flight bracket-key chord. Callers invoke this
// before transitioning away from ModeList so the footer hint disappears and
// the next return to ModeList starts in a clean state.
func (m *Model) clearJumpChord() {
	m.pendingJumpDir = 0
}
