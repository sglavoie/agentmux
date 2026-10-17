// The selected session, said in a tile.
//
// This is the inspector's small sibling. The overview draws many sessions at
// once inside boxes of about 46 by 10 cells, and the six-row metadata block is
// most of that before a single line of terminal output is reached. So the header
// is cut to TWO rows, and the cut is a choice about which questions a tile is
// answering rather than an even squeeze of the same six.
//
// Which session, what is it doing and for how long, and what is running it. The
// three that survive are the three an operator asks of a session they are not
// currently looking at. The three that go — branch, directory, and the
// supervision/lock/mute/watchdog line — are the ones asked of a session that has
// already been picked, and the full inspector is where picking one leads.
//
// Layout and status vocabulary are shared with the inspector:
//
//   - The block is a FIXED two lines, for the reason the inspector's is fixed
//     at six. A tile whose divider moved with its content would make a grid of
//     them ripple every time any one session changed.
//   - The status helpers are shared, but the tile projects agent work separately
//     from ASL: a completed turn remains Done beside the run's phase. The full
//     inspector and queue retain the window's aggregate state.
//   - The five output situations are told apart by previewPlaceholder, shared
//     with the inspector: loading, legitimately empty, failed, vanished, and no
//     selection stay five answers here too.
//
// The one fact a tile needs that the inspector does not is the QUALIFIER. The
// inspector draws one session at a time, so a colliding name is unambiguous by
// the time it is on screen; a grid draws the collision side by side. The
// disambiguator is measured across the whole live set, including the sessions on
// other pages, so it cannot be derived from the tile — the caller supplies it.
//
// Nothing here reads a clock, a config file, Kitty, or the daemon.

package dashboard

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

const (
	// CompactMetaLines is the height of the compact header: identity and status
	// on the first line, then agent. Fixed at every width and for every content
	// state. Session inspector metadata is measured separately.
	CompactMetaLines = 2

	// The fixed row each compact fact occupies, named for the same reason the
	// inspector's rows are: the geometry is the contract.
	compactTitleRow = 0
	compactAgentRow = 1

	// CompactChromeLines is what a tile spends before a line of output is drawn.
	// A caller sizing a tile charges this once; the output region is whatever the
	// height leaves after it.
	CompactChromeLines = CompactMetaLines + inspectorDividerLines
)

// CompactInput is every fact one tile is drawn from.
//
// It is a near-subset of InspectorInput and deliberately not an alias of it. The
// two panes answer different questions, so the fields the tile does not draw —
// the watchdog, the supervision marker, the viewport — are absent rather than
// ignored: a caller that had to supply them would have to decide what a tile
// does with a paused snapshot, and the answer is that a tile has no scrollback
// position at all.
type CompactInput struct {
	Width, Height int
	// SummaryOnly folds the header into two rows and omits divider and output.
	SummaryOnly bool

	// Content decides which of the five situations the output half is in. It is
	// the caller's, for the reason it is the caller's in the inspector: only the
	// controller that issued the read knows whether one is outstanding.
	Content InspectorContent

	// Session is the tile's conversation. Read for every state except
	// InspectorNoSelection, including InspectorVanished.
	Session session.RunningSession

	// Qualifier is the disambiguating context measured across the whole live set
	// — NameDisambiguator.Qualifier's answer — and "" when the name stands alone.
	// It is supplied rather than derived because a tile can see one session and a
	// collision is a property of the set.
	Qualifier string

	Cfg *config.Config
	// LastActivity and IdleThreshold are StateGroup's inputs, derived here through
	// the same helper the inspector uses so the two say the same sentence.
	LastActivity  map[int]time.Time
	IdleThreshold time.Duration
	// Now is the clock every elapsed time is measured against.
	Now time.Time

	// Output is the newest snapshot, one entry per line, already split by
	// SplitOutput. Empty under InspectorReady is a legitimately empty buffer.
	Output []string
	// Error is the failed read's message, read only under InspectorFailed.
	Error string
}

// RenderCompactPreview returns exactly Height lines (no trailing newline), the
// same contract RenderInspector and RenderProjectCard keep, so a grid can size
// a tile once and trust what comes back to fill it.
//
// A tile too short for the chrome draws as much of the header as fits and no
// output at all, which is the inspector's priority said again at a smaller size:
// a preview that cannot say which session it is showing is worse than one that
// cannot show its terminal.
func RenderCompactPreview(in CompactInput) string {
	if in.Width <= 0 || in.Height <= 0 {
		return ""
	}
	if in.SummaryOnly {
		return renderCompactSummary(in)
	}
	lines := compactMetaLines(in)
	if in.Height <= len(lines) {
		return strings.Join(lines[:in.Height], "\n")
	}
	// The rule is drawn through the inspector's own divider so the two panes
	// share one answer about which cells it covers. A tile is never paused, so
	// the zero viewport is passed rather than a flag.
	lines = append(lines, inspectorDivider(InspectorInput{Width: in.Width}))
	lines = append(lines, compactOutputLines(in, in.Height-len(lines))...)
	return strings.Join(lines, "\n")
}

func renderCompactSummary(in CompactInput) string {
	lines := make([]string, in.Height)
	if in.Content != InspectorNoSelection {
		lines[0] = compactHeaderLine(in)
		if in.Height > 1 {
			lines[1] = layout.StyledTruncate(styleDim.Render(joinDetailTokens(harnessLabel(in.Session.Harness), in.Session.EffectiveProfile())), in.Width, "…")
		}
	}
	return strings.Join(lines, "\n")
}

// compactHeaderLine keeps the current state at the right edge while giving a
// long status at most two thirds of the row so the session remains identifiable.
func compactHeaderLine(in CompactInput) string {
	context, state := compactStatusParts(in)
	stateWidth := layout.StyledWidth(state)
	statusWidth := min(layout.StyledWidth(context)+stateWidth+layout.Width(metaSeparator), max(in.Width*2/3, stateWidth))
	statusWidth = min(statusWidth, max(in.Width-3, 0))
	titleWidth := max(in.Width-statusWidth-2, 0)
	if titleWidth <= 0 {
		return layout.StyledTruncate(compactTitleLine(in, in.Session), in.Width, "…")
	}
	title := layout.StyledTruncate(compactTitleLine(in, in.Session), titleWidth, "…")
	var status string
	if stateWidth >= statusWidth {
		status = layout.StyledTruncate(state, statusWidth, "…")
	} else {
		contextWidth := statusWidth - stateWidth - layout.Width(metaSeparator)
		if contextWidth > 0 {
			status = layout.StyledTruncate(context, contextWidth, "…") + styleDim.Render(metaSeparator) + state
		} else {
			status = state
		}
	}
	gap := in.Width - layout.StyledWidth(title) - layout.StyledWidth(status)
	return title + strings.Repeat(" ", gap) + status
}

// compactMetaLines builds exactly CompactMetaLines lines, each already clamped
// to the tile's width.
func compactMetaLines(in CompactInput) []string {
	lines := make([]string, CompactMetaLines)
	if in.Content == InspectorNoSelection {
		return lines
	}
	s := in.Session
	lines[compactTitleRow] = compactHeaderLine(in)
	lines[compactAgentRow] = layout.StyledTruncate(
		styleDim.Render(joinDetailTokens(harnessLabel(s.Harness), s.EffectiveProfile())), in.Width, "…")
	return lines
}

// compactStatusParts separates the agent's outcome from the supervising run.
// The copy is local: the dashboard and tab still receive their aggregate state.
// Keeping them apart lets the title line shorten elapsed/run context without
// truncating the state word at its right edge.
func compactStatusParts(in CompactInput) (context, state string) {
	s := in.Session
	group := StateGroup(s, in.LastActivity, in.IdleThreshold)
	if turn := s.AgentTurn; turn != nil {
		if s.State != turn.IconState {
			s.Unseen = false // an aggregate unread bit cannot describe another fact
			s.Reason = ""
			s.BusySince = time.Time{}
			s.PresentationChangedAt = time.Time{}
		}
		s.State = turn.IconState
		if turn.FinishedAt > 0 {
			s.PresentationChangedAt = time.Unix(turn.FinishedAt, 0)
		}
		group = StateGroup(s, in.LastActivity, in.IdleThreshold)
		if s.State == "" {
			group = idleGroup
			s.PresentationChangedAt = time.Time{}
		}
	}
	style := emphasisStyle(StatusEmphasis(s, group))
	switch StatusText(s, group) {
	case statusIdle:
		style = styleStateWarning.Bold(true)
	case statusDone:
		style = accountStyle("green").Bold(true)
	}
	context = styleDim.Render(ElapsedText(s, group, in.Now))
	if run := compactASLStatus(in.Session); run != "" {
		context += styleDim.Render(" | " + run)
	}
	if unreadStop(s) {
		state = styleUnreadNotice.Render(UnreadNoticeGlyph) + " "
	}
	if label := StatusText(s, group); label != "" {
		state += style.Render(label)
	} else {
		state += styleDim.Render(inspectorUnknown)
	}
	return context, state
}

func compactASLStatus(s session.RunningSession) string {
	var labels []string
	for _, phase := range s.ASLPhases {
		var label string
		switch phase {
		case "starting":
			label = "starting"
		case "turn":
			label = "running"
		case "verification":
			label = "verifying"
		case "between":
			label = "between turns"
		case "waiting":
			label = "waiting"
		case "paused":
			label = "paused"
		default:
			continue
		}
		labels = append(labels, label)
	}
	if len(labels) > 0 {
		return "ASL " + strings.Join(labels, "/")
	}
	if s.Looping {
		return "ASL" // legacy daemon/title: phase unknown
	}
	return ""
}

// compactTitleLine draws the session's name in its project's identity color,
// followed by its collision qualifier in muted text.
//
// The two halves are styled differently for the reason the queue row styles them
// differently: the name is the session and the qualifier is the reason two names
// look alike, and a grid that painted both in the project color would read as a
// project whose title happens to contain a branch name.
func compactTitleLine(in CompactInput, s session.RunningSession) string {
	title := layout.RenderTitleIcon(SessionDisplayTitle(s))
	line := accountStyle(ProjectIdentityColor(s, ProfileColorMap(in.Cfg), in.Cfg)).Render(title)
	if in.Qualifier == "" {
		return line
	}
	return line + styleDim.Render(" ("+in.Qualifier+")")
}

// compactOutputLines returns exactly height lines of output region.
//
// The tail is taken through the zero OutputViewport rather than by slicing here.
// A tile has no scroll position, and following the newest snapshot is precisely
// what the zero value means — reusing it is what keeps "the bottom line is the
// newest line" one rule instead of two.
func compactOutputLines(in CompactInput, height int) []string {
	if height <= 0 {
		return nil
	}
	var lines []string
	if text, ok := previewPlaceholder(in.Content, in.Output, in.Error); ok {
		lines = []string{text}
	} else {
		lines = OutputViewport{}.Window(in.Output, height)
	}

	out := make([]string, height)
	for i := range out {
		if i < len(lines) {
			// StyledTruncate rather than Truncate: a capture carrying escape
			// sequences is measured by the cells it paints, and a sequence cut in
			// half would leak out of this tile and into the one beside it.
			out[i] = layout.StyledTruncate(lines[i], in.Width, "…")
		}
	}
	return out
}
