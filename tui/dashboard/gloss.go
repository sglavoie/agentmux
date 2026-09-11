package dashboard

// stateGlosses maps the state name (as emitted by StateGroup / palette.StateIcon)
// to a short human-readable meaning shown in the footer when the cursor sits
// on a row of that state. Idle/empty states have no entry: their gloss is the
// absence of an icon, which is gloss enough.
var stateGlosses = map[string]string{
	"bolt":     "active",
	"stop":     "stopped",
	"key":      "needs permission",
	"warning":  "last tool failed",
	"error":    "stopped with failure",
	"subagent": "subagent running",
	"compact":  "compacting context",
	"question": "awaiting response",
}

// StateGloss returns the human-readable meaning for a state name, or "" if
// the state has no gloss (idle / unknown).
func StateGloss(state string) string {
	return stateGlosses[state]
}

// LegendStates lists the state names the icon reference is generated from, in
// the order they appear. Mirrors stateGlosses; idle/unknown states have no
// entry.
//
// The reference used to be a strip above the list and is now the help overlay's
// State icons section. The name is kept because the list is still a legend in
// the only sense that matters here — icon to meaning, one line each, in a fixed
// order — and because what it is read by is not this file's business.
var LegendStates = []string{"bolt", "stop", "key", "warning", "error"}
