package history

import (
	"fmt"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/layout"
)

// Full values remain readable here even when the compact inspector clips them.
// This is a view of the current snapshot, never a new probe or an action.
func (in DetailInput) metadataLines() []string {
	s, ok := in.detailSession()
	if !ok {
		return []string{"Session is no longer listed."}
	}
	branch := s.Branch
	if s.Dirty && branch != "" {
		branch += "*"
	}
	fields := [][2]string{
		{"Session", dashboard.SessionDisplayTitle(s)},
		{"Harness", s.Harness}, {"Profile", s.EffectiveProfile()},
		{"Directory", s.Directory}, {"Branch", branch},
		{"Runtime session", string(s.RuntimeSessionID)}, {"Backend", string(s.Backend)},
		{"State", s.State}, {"Reason", s.Reason}, {"Locked", fmt.Sprint(s.Locked)},
		{"Muted", fmt.Sprint(s.Muted)}, {"Run phases", strings.Join(s.ASLPhases, ", ")}, {"Supervised", fmt.Sprint(s.Looping || len(s.ASLPhases) > 0)},
	}
	if !s.BusySince.IsZero() {
		fields = append(fields, [2]string{"Working since", s.BusySince.Format(time.RFC3339)})
	}
	if ws := in.Watchdog[s.WindowID]; ws.LastAction != "" {
		fields = append(fields, [2]string{"Last event", dashboard.FormatAge(ws, in.now())})
	}
	var lines []string
	for _, field := range fields {
		value := field[1]
		if value == "" {
			value = "Not recorded"
		}
		lines = append(lines, layout.WrapHard(field[0]+": "+value, max(1, in.detailRowWidth()))...)
	}
	return lines
}

func (in DetailInput) renderMetadata() string {
	rows := in.metadataLines()
	total, visible := in.ScrollBounds(in.VisibleHeight())
	offset := min(max(0, in.Scroll), max(0, total-visible))
	lines := []string{"Session metadata", "Full values from the current session snapshot", "", ""}
	if offset > 0 {
		lines[3] = "▲"
	}
	lines = append(lines, rows[offset:offset+visible]...)
	if offset+visible < total {
		lines = append(lines, "▼")
	} else {
		lines = append(lines, "")
	}
	lines = append(lines, "m events · ↑/↓ scroll · ctrl+f/b page · esc close")
	// Bound the footer/header too; the root compositor constrains the overlay.
	for i, line := range lines {
		lines[i] = layout.Truncate(line, max(1, in.detailRowWidth()), "…")
	}
	return strings.Join(lines, "\n")
}
