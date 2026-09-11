package tui

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// This file is the root's seam onto the health issue model: it projects the
// model's scattered health fields onto dashboard.HealthFacts and renders the
// details overlay from the result.
//
// The projection is the only place health facts are read for presentation. The
// header hint strip and the status bar's degraded reading both go through it
// too, so the three surfaces cannot drift into three accounts of one condition —
// which is the duplication the session's review criterion names.
//
// It reads CACHED facts and starts nothing. commands.Health is the only thing
// that observes, it runs solely from the tick schedule, and it spawns a detached
// daemon when its ping fails; a render path that could reach it would make
// opening an overlay a side effect on the operator's machine.

// healthFacts projects the model's health state onto the leaf's input.
//
// Two derivations are deliberately performed HERE rather than in the leaf. The
// topology age comes from topologyAge, which already owns the "age the tree on
// screen carries plus everything elapsed since" rule that lets an outage age a
// warm tree with no message arriving. And the stale threshold is the daemon's
// own protocol.KittySnapshotStaleAfter, passed in as a value so the leaf keeps
// its single in-repo dependency instead of importing the daemon wire contract to
// paint a line.
func healthFacts(m Model) dashboard.HealthFacts {
	now := modelNow(m)
	facts := dashboard.HealthFacts{
		Now:                   now,
		DaemonCheckedAt:       m.daemonHealthCheckedAt,
		DaemonAlive:           m.daemonAlive,
		HooksCheckedAt:        m.hookHealthCheckedAt,
		HookInSync:            m.hookHealth.InSync,
		HookDrifted:           m.hookHealth.Drifted,
		HookMissing:           m.hookHealth.Missing,
		TopologyStaleAfter:    protocol.KittySnapshotStaleAfter,
		TopologySource:        string(m.topology.Source),
		TopologyStale:         m.topology.Stale,
		TopologyGeneration:    m.topology.Generation,
		DeprecationsDismissed: m.deprecationDismissed,
	}
	if m.hookHealthErr != nil {
		facts.HookErr = m.hookHealthErr.Error()
	}
	if m.err != nil {
		facts.TopologyErr = m.err.Error()
	}
	facts.TopologyAge, facts.TopologyWarm = topologyAge(m, now)
	for _, d := range m.deprecations {
		facts.Deprecations = append(facts.Deprecations, dashboard.HealthDeprecation{
			Key:         d.Key,
			Replacement: d.Replacement,
		})
	}
	return facts
}

// healthIssues is the current reading, rebuilt on every call. Nothing caches it:
// the ages in it move with the clock, and a reading held across a frame would
// report a freshness that has since expired.
func healthIssues(m Model) []dashboard.HealthIssue {
	return dashboard.BuildHealthIssues(healthFacts(m))
}

// healthBoxWidth is the width styleOverlayBox is rendered at. It is wider than
// the project switcher's fifty columns because this overlay's payload is prose —
// a joined hooks error, a list of drifted profile names — rather than a label and
// three tallies, and truncating a diagnostic is how an operator ends up running
// the wrong remedy.
func healthBoxWidth(m Model) int {
	width := 72
	if avail := m.width - 4; avail < width {
		width = avail
	}
	if width < 1 {
		width = 1
	}
	return width
}

// healthContentWidth is the width the leaf lays a line out at: the box width
// less the border column and the padding column on each side, which is what
// styleOverlayBox re-wraps anything wider than.
func healthContentWidth(m Model) int {
	width := healthBoxWidth(m) - styleOverlayBox.GetHorizontalFrameSize()
	if width < 1 {
		width = 1
	}
	return width
}

// healthVisibleHeight is the rows the issue body may spend, including its ▲/▼
// markers. It charges the same chrome the project switcher does — the two border
// rows, the heading, and a one-row margin — and caps at the same sixteen-ish
// detour height so the dimmed dashboard stays visible around it.
func healthVisibleHeight(m Model) int {
	if m.height <= 0 {
		return 0
	}
	chrome := styleOverlayBox.GetVerticalFrameSize() + 2
	avail := rootBodyHeight(m) - chrome
	if avail < 1 {
		return 1
	}
	if avail > 16 {
		return 16
	}
	return avail
}

func healthInput(m Model) dashboard.HealthInput {
	return dashboard.HealthInput{
		Issues: healthIssues(m),
		Width:  healthContentWidth(m),
		Height: healthVisibleHeight(m),
		Offset: m.healthOffset,
	}
}

// healthDismissFooterBinding advertises `d` only while there is something it
// would actually silence. The overlay always lists all four issues, and three of
// them can never be dismissed, so a footer that offered the key unconditionally
// would read as an offer to silence whichever failure the operator came to look
// at.
func healthDismissFooterBinding(m Model) string {
	for _, issue := range healthIssues(m) {
		if issue.Dismissible && issue.WantsAttention() {
			return renderFooterBinding("d", "dismiss config notice")
		}
	}
	return ""
}

// renderHealthBox wraps the leaf's content in styleOverlayBox so the details can
// be composited over the dimmed dashboard.
func renderHealthBox(m Model) string {
	content := dashboard.RenderHealth(healthInput(m))
	return styleOverlayBox.Width(healthBoxWidth(m)).Render(strings.TrimRight(content, "\n"))
}
