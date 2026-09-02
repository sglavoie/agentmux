package history

import (
	"fmt"
	"sort"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

type timelinePhase struct {
	kind         string // "preamble" | "active" | "subagent" | "compact" | "gap" | "separator"
	startAt      time.Time
	endAt        time.Time // zero = ongoing
	events       []protocol.SessionEvent
	termEvent    *protocol.SessionEvent // event that closed this phase (nil = ongoing)
	contextLimit bool                   // phase terminated by context-limit probe
}

// phaseDuration returns a human-readable duration string, or "ongoing" if endAt is zero.
func (p timelinePhase) phaseDuration() string {
	if p.endAt.IsZero() {
		return "ongoing"
	}
	d := p.endAt.Sub(p.startAt)
	if d < 0 {
		d = 0
	}
	switch {
	case d < 60*time.Second:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		return fmt.Sprintf("%dh%02dm", h, m)
	}
}

// toolSummary collapses PreToolUse ToolName values into "bash ×3  write_file ×2" (top 4, freq desc).
func (p timelinePhase) toolSummary() string {
	counts := map[string]int{}
	for _, ev := range p.events {
		if ev.Event == "PreToolUse" && ev.ToolName != "" {
			counts[ev.ToolName]++
		}
	}
	if len(counts) == 0 {
		return ""
	}

	type kv struct {
		name  string
		count int
	}
	pairs := make([]kv, 0, len(counts))
	for name, count := range counts {
		pairs = append(pairs, kv{name, count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].name < pairs[j].name
	})
	if len(pairs) > 4 {
		pairs = pairs[:4]
	}

	result := ""
	for i, kv := range pairs {
		if i > 0 {
			result += "  "
		}
		result += fmt.Sprintf("%s ×%d", kv.name, kv.count)
	}
	return result
}

// phaseIcon returns the display icon for a phase kind.
func phaseIcon(kind string) string {
	switch kind {
	case "active":
		return "⚡"
	case "subagent":
		return "🤖"
	case "compact":
		return "🌀"
	default:
		return "·"
	}
}

// phaseKindLabel returns the display label for a phase kind.
func phaseKindLabel(kind string) string {
	switch kind {
	case "active":
		return "Active"
	case "subagent":
		return "Subagent"
	case "compact":
		return "Compact"
	default:
		return kind
	}
}
