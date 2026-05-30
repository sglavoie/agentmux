package tui

import (
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

// buildProfileColorMap maps profile name → palette color name resolved from
// the profile's required Color field. Used for elements that should track the
// profile (project title, [X] account badge, account chips, detail/preview
// header).
func buildProfileColorMap(m Model) map[string]string {
	result := make(map[string]string, len(m.cfg.Accounts))
	for _, acc := range m.cfg.Accounts {
		result[acc.Name] = acc.Color
	}
	return result
}

func buildHarnessColorMap(m Model) map[string]string {
	result := make(map[string]string, len(m.cfg.HarnessColors))
	for name, color := range m.cfg.HarnessColors {
		result[name] = color
	}
	return result
}

func projectIdentityColor(s session.RunningSession, acctColor map[string]string, cfg *config.Config) string {
	colorName := acctColor[s.EffectiveProfile()]
	if cfg == nil {
		return colorName
	}
	if def := cfg.DefaultProfileFor(s.Project); def != "" {
		if c := acctColor[def]; c != "" {
			return c
		}
	}
	return colorName
}

// stateGroup classifies a session into one of three buckets:
//
//	0 → Needs Attention (key, stop, error, question)
//	1 → Active (bolt, warning, subagent, compact, or empty state within the
//	    idle threshold, or empty state with no activity record)
//	2 → Idle (empty state with stale activity, beyond the threshold)
//
// The warning state is grouped under Active so a session whose last tool
// call failed (carrying the ⚠️ icon) stays visible alongside bolt rows
// instead of hiding under Idle. The terminal error state (❗) stays under
// Needs Attention so stop failures remain high-signal. subagent and compact
// are mid-task states the daemon emits while work is in progress, so they
// classify alongside bolt; question is the elicitation prompt, which requires
// user input and so groups under Needs Attention.
//
// Without a daemon-recorded activity timestamp a session is treated as
// Active rather than Idle so a fresh session does not flicker into the
// Idle bucket the moment it appears.
func stateGroup(s session.RunningSession, lastActivity map[int]time.Time, threshold time.Duration) int {
	switch s.State {
	case "key", "stop", "error", "question":
		return 0
	case "bolt", "warning", "subagent", "compact":
		return 1
	}
	if s.State != "" {
		// Unknown/legacy state names — keep the previous behavior (Idle).
		return 2
	}
	ts, ok := lastActivity[s.WindowID]
	if !ok {
		return 1
	}
	if threshold > 0 && time.Since(ts) >= threshold {
		return 2
	}
	return 1
}

// groupBorder returns the colored 1-cell left border glyph for the given
// group (0 = attention, 1 = active, 2 = idle).
func groupBorder(group int) string {
	switch group {
	case 0:
		return styleGroupBorderAttention.Render(groupBorderGlyph)
	case 1:
		return styleGroupBorderActive.Render(groupBorderGlyph)
	default:
		return styleGroupBorderIdle.Render(groupBorderGlyph)
	}
}
