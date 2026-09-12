// The selected session, said in full.
//
// The redesigned queue row answers three questions — which session, what is it
// doing, for how long — and nothing else. Everything the row stopped carrying
// (profile, harness, branch, dirty state, directory, supervision, lock, mute,
// the watchdog's silence) is the SELECTED session's context, and this is where
// it is said. Below it, the same pane shows that session's terminal output,
// which is the one fact about a session that no projection can summarize.
//
// Two properties are the design rather than details of it.
//
// The metadata block is a FIXED six lines. Every line is always drawn, and a
// fact this build cannot supply renders as `—` rather than collapsing the line.
// A block that shrank with its content would move the divider and the output
// under it on every cursor move, so scanning down a list would make the text the
// operator is reading walk up and down the pane. Six lines cost six lines on the
// session with nothing to say, and that is the cheaper half of the trade.
//
// And the output half distinguishes five situations that the pane it replaces
// collapsed into two. A read that has not landed, a buffer that is legitimately
// empty, a read that failed, a selection that is not a session, and a session
// whose window is gone are five different things to tell an operator; the old
// preview said "(loading…)" for the first two and "(session not running)" for
// the last two. The state is an explicit input here rather than inferred from an
// empty string, because an empty string is exactly the evidence that cannot
// separate them.
//
// Nothing in this file reads a clock, a config file, Kitty, or the daemon. The
// caller supplies every fact, including `Now`, which is what lets a whole
// inspector be asserted byte-for-byte against a pinned moment.

package dashboard

import (
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/layout"
)

const (
	// InspectorMetaLines is the height of the metadata block: title, status and
	// age, agent, branch, directory, and the supervision/lock/mute/watchdog line.
	// It is fixed at every width and for every content state.
	InspectorMetaLines = 6

	// The fixed row each metadata fact occupies. They are named because the
	// block's geometry is a contract — the pane scans top to bottom and a fact
	// that moved rows would defeat that — and a caller or a test that wants one
	// line should say which fact it means rather than count.
	inspectorTitleRow   = 0
	inspectorStatusRow  = 1
	inspectorAgentLine  = 2
	inspectorBranchLine = 3
	inspectorDirLine    = 4
	inspectorWatchLine  = 5

	// inspectorDividerLines is the rule between the metadata block and the
	// output, which is also where the Paused indication is drawn.
	inspectorDividerLines = 1

	// InspectorChromeLines is what the inspector spends before a single line of
	// output is drawn. A caller sizing the pane charges this once; the output
	// region is whatever the height leaves after it.
	InspectorChromeLines = InspectorMetaLines + inspectorDividerLines

	// inspectorLabelWidth is the label column of the three labelled metadata
	// lines, sized to the longest label plus its gap so the values start on one
	// cell. A column rather than a separator: the operator reads down the block
	// looking for one fact, and a ragged left edge makes them read all four.
	inspectorLabelWidth = 8

	// inspectorUnknown is what a fact this build cannot supply renders as. It is
	// the same `—` the queue's Time column uses for an unknown clock, and it means
	// the same thing: the line exists, and there is nothing true to put in it.
	inspectorUnknown = "—"

	// inspectorPausedLabel is the indication that the output pane is showing a
	// held snapshot rather than the newest one.
	inspectorPausedLabel = "Paused"
)

// The five sentences a preview shows INSTEAD of terminal output. They are
// constants in one place because two surfaces now say them — the full inspector
// and the compact tile the overview draws — and the whole reason the content
// state is an explicit input rather than an inference from an empty string is
// that these five situations must stay told apart. Two renderers each spelling
// their own "(loading…)" would be two places for one of them to quietly start
// collapsing a pair.
const (
	previewNoSelectionText = "(no session selected)"
	previewVanishedText    = "(session not running)"
	previewLoadingText     = "(loading…)"
	previewEmptyText       = "(no output)"
	previewErrorPrefix     = "error: "
)

// InspectorContent names what the output half of the inspector is showing. It
// is decided by the caller, which is the only place that knows whether a read
// has been issued, has landed, or has failed.
type InspectorContent int

const (
	// InspectorNoSelection means the cursor is not on a session at all — an empty
	// list, or a synthetic row that owns no conversation. The metadata block is
	// blank rather than a rendering of the zero session.
	InspectorNoSelection InspectorContent = iota
	// InspectorLoading means a read is outstanding and nothing has come back for
	// this target yet. It is not the same as an empty buffer, which is why the
	// caller says so rather than the renderer guessing from empty text.
	InspectorLoading
	// InspectorReady means a read landed. Output may still be empty, and an empty
	// Output under this state is the legitimate "this session has printed
	// nothing" — a real answer, not a missing one.
	InspectorReady
	// InspectorFailed means the read itself failed; Error names it.
	InspectorFailed
	// InspectorVanished means the selection is a session whose live window is
	// gone. Its metadata is still drawn — who it was is exactly what the operator
	// is looking at the pane to find out — and only the output is unavailable.
	InspectorVanished
)

// String names the content state for diagnostics and test failure messages.
func (c InspectorContent) String() string {
	switch c {
	case InspectorLoading:
		return "loading"
	case InspectorReady:
		return "ready"
	case InspectorFailed:
		return "failed"
	case InspectorVanished:
		return "vanished"
	default:
		return "no-selection"
	}
}

// InspectorInput is every fact the inspector is drawn from.
//
// It is a struct of explicit inputs rather than a model reference for the reason
// every other renderer in this package takes one: the pane has to be assertable
// from a test with no dashboard behind it, and a renderer holding a model could
// reach past its inputs for a fact — a wall clock, a fresh Kitty read — that the
// test then cannot pin.
type InspectorInput struct {
	Width, Height int

	// Content decides which of the five situations the output half is in.
	Content InspectorContent

	// Session is the selected conversation. Read for every state except
	// InspectorNoSelection, including InspectorVanished.
	Session session.RunningSession

	Cfg *config.Config
	// LastActivity and IdleThreshold are StateGroup's inputs. The bucket is
	// derived here rather than passed in, unlike the queue row's: the inspector
	// draws no group heading, so there is no second answer for it to disagree
	// with, and deriving it through the same helper is what keeps the status
	// sentence identical to the one on the row above.
	LastActivity  map[int]time.Time
	IdleThreshold time.Duration
	// Watchdog is the selected window's entry, already looked up by the caller.
	Watchdog protocol.WatchdogState
	// Now is the clock every elapsed time is measured against.
	Now time.Time

	// Supervision is the marker bound to this session when one was positively
	// identified, and "" when nothing supervises it or nothing could say which
	// run does. A guessed association would put a wrong run's state beside a
	// session, so an unidentified one gets the daemon's own loop bit or nothing.
	Supervision string

	// Output is the newest snapshot, one entry per line, already split by
	// SplitOutput. Empty under InspectorReady is a legitimately empty buffer.
	Output []string
	// Error is the failed read's message, read only under InspectorFailed.
	Error string
	// Viewport is where the output pane is looking. Its zero value follows the
	// tail, which is what a freshly selected session shows.
	Viewport OutputViewport
}

// RenderInspector returns exactly Height lines (no trailing newline), the same
// contract RenderProjectCard and the preview pane it replaces keep, so the split
// view can swap one for another without re-measuring the pane.
//
// A pane too short for the chrome draws as much of the metadata block as fits
// and no output at all. That ordering is the priority: an inspector that cannot
// say which session it is describing is worse than one that cannot show its
// output, because the output is the half that means nothing without the header.
func RenderInspector(in InspectorInput) string {
	if in.Width <= 0 || in.Height <= 0 {
		return ""
	}
	lines := inspectorMetaLines(in)
	if in.Height <= len(lines) {
		return strings.Join(lines[:in.Height], "\n")
	}
	lines = append(lines, inspectorDivider(in))
	lines = append(lines, inspectorOutputLines(in, in.Height-len(lines))...)
	return strings.Join(lines, "\n")
}

// inspectorMetaLines builds exactly InspectorMetaLines lines, each already
// clamped to the pane's width.
//
// The order is the order the questions are asked in. Which session is this;
// what is it doing and for how long; what is running it; which checkout it is
// in and whether that checkout is dirty; where that is on disk; and who else is
// touching it. The first two are the headline and carry the pane's only color;
// the last four are metadata and wear the label column.
func inspectorMetaLines(in InspectorInput) []string {
	lines := make([]string, InspectorMetaLines)
	if in.Content == InspectorNoSelection {
		return lines
	}
	s := in.Session
	group := StateGroup(s, in.LastActivity, in.IdleThreshold)

	lines[inspectorTitleRow] = layout.StyledTruncate(inspectorTitleLine(in, s), in.Width, "…")
	lines[inspectorStatusRow] = layout.StyledTruncate(inspectorStatusLine(in, s, group), in.Width, "…")
	lines[inspectorAgentLine] = inspectorLabelled(in.Width, "Agent",
		joinDetailTokens(harnessLabel(s.Harness), s.EffectiveProfile()))
	lines[inspectorBranchLine] = inspectorLabelled(in.Width, "Branch", branchText(s))
	lines[inspectorDirLine] = inspectorDirectoryLine(in.Width, s.Directory)
	lines[inspectorWatchLine] = inspectorLabelled(in.Width, "Watch", inspectorWatchText(in, s))
	return lines
}

// inspectorTitleLine draws the session's name in its project's identity color,
// with the fixed two-cell title-icon column so an emoji-prefixed project aligns
// here with the queue row it was selected from.
func inspectorTitleLine(in InspectorInput, s session.RunningSession) string {
	title := layout.RenderTitleIcon(SessionDisplayTitle(s))
	return accountStyle(ProjectIdentityColor(s, ProfileColorMap(in.Cfg), in.Cfg)).Render(title)
}

// inspectorStatusLine is the headline fact and its age: the same sentence the
// queue row says, refined by the same blocked reason, drawn in the same
// emphasis, beside the same clock.
//
// It leads with the unread notice when there is one. That is the one fact the
// status sentence deliberately does not carry — `Done` is true whether or not
// anyone has looked — and it is exactly why an unread completion sits in Needs
// you, so the pane describing the selection has to say it somewhere.
func inspectorStatusLine(in InspectorInput, s session.RunningSession, group int) string {
	var line string
	if unreadStop(s) {
		line = styleUnreadNotice.Render(UnreadNoticeGlyph) + " "
	}
	status := StatusText(s, group)
	if status == "" {
		line += styleDim.Render(inspectorUnknown)
	} else {
		line += emphasisStyle(StatusEmphasis(s, group)).Render(status)
	}
	return line + styleDim.Render(metaSeparator+ElapsedText(s, group, in.Now))
}

// inspectorLabelled draws one labelled metadata line: a dim label in its fixed
// column, then the value in ordinary weight.
//
// The label is the dim half and the value is not, which is the reverse of how
// the retired detail strip drew the same facts. There the whole line was
// secondary text beneath a list; here the facts ARE the pane, and a block where
// every cell was faint would have nothing on it to read.
func inspectorLabelled(width int, label, value string) string {
	if width <= 0 {
		return ""
	}
	head := styleDim.Render(layout.PadRight(label, inspectorLabelWidth))
	if value == "" {
		return layout.StyledTruncate(head+styleDim.Render(inspectorUnknown), width, "…")
	}
	return layout.StyledTruncate(head+value, width, "…")
}

// inspectorDirectoryLine is the directory, truncated from the LEFT so the tail
// that names the checkout survives and the shared prefix is what goes — the
// same priority the project card and the retired strip kept for the same path.
func inspectorDirectoryLine(width int, dir string) string {
	if dir == "" {
		return inspectorLabelled(width, "Dir", "")
	}
	room := width - inspectorLabelWidth
	if room < 2 {
		return inspectorLabelled(width, "Dir", dir)
	}
	return inspectorLabelled(width, "Dir", layout.TruncateLeft(dir, room, "…"))
}

// inspectorWatchText is the last metadata line: who is driving this session and
// how it may be acted on.
//
// Four facts share one line because they answer one question. A supervision
// marker says an agent-session-loop run is holding it; the lock and mute bits
// say the operator has taken something away from it; and the watchdog's age says
// how long it has been since the harness said anything at all. None of them is a
// property of the current turn, which is why none of them is on the status line.
func inspectorWatchText(in InspectorInput, s session.RunningSession) string {
	var tokens []string
	switch {
	case in.Supervision != "":
		tokens = append(tokens, in.Supervision)
	case s.Looping:
		tokens = append(tokens, loopMarkerGlyph)
	}
	if s.Locked {
		tokens = append(tokens, palette.LockIcon())
	}
	if s.Muted {
		tokens = append(tokens, palette.MuteIcon())
	}
	if age := inspectorWatchdogToken(in.Watchdog, in.Now); age != "" {
		tokens = append(tokens, age)
	}
	return joinDetailTokens(tokens...)
}

// inspectorWatchdogToken reports how long the daemon has gone without a hook
// event for this window, and "" when no action has ever been recorded — an
// unheard-from window and a window with no watchdog entry are the same silence,
// and neither is worth a number.
//
// The staleness glyph is the watchdog's own 80%-of-threshold rule, shared with
// the activity column rather than restated, so the two surfaces cannot start
// warning at different moments about the same window.
func inspectorWatchdogToken(ws protocol.WatchdogState, now time.Time) string {
	if ws.LastAction == "" {
		return ""
	}
	text := "last event " + FormatAge(ws, now)
	if watchdogStale(ws, now) {
		text += " " + ageWarningGlyph
	}
	return text
}

// inspectorDivider draws the rule between the metadata block and the output,
// and carries the Paused indication when the pane is holding a snapshot.
//
// Paused lives on the divider rather than on a line of its own because it is a
// property of the boundary — everything below this rule is no longer live — and
// because a row that appeared and disappeared would move the output under the
// reader at exactly the moment they asked for it to stop moving.
func inspectorDivider(in InspectorInput) string {
	if !in.Viewport.Paused() {
		return styleDim.Render(strings.Repeat("─", in.Width))
	}
	// Two cells of rule, a space either side of the label, and at least one cell
	// of rule after it. Below that the label would be the whole divider, so the
	// plain rule is drawn instead and the pane keeps its geometry.
	label := inspectorPausedLabel
	if in.Width < 2+1+layout.Width(label)+1+1 {
		return styleDim.Render(strings.Repeat("─", in.Width))
	}
	trailing := in.Width - 2 - 1 - layout.Width(label) - 1
	return styleDim.Render("──") + " " + styleAttentionRow.Render(label) + " " +
		styleDim.Render(strings.Repeat("─", trailing))
}

// inspectorOutputLines returns exactly height lines of output region.
//
// The hold is checked before the content state, and the ordering is the
// contract. A pause is a promise that the text on screen stays where it is; a
// refresh that fails, or one still in flight, must not blank out the snapshot
// the operator paused in order to read. What a paused pane cannot do is hide
// that it is stale, and the divider above it says so on every frame.
func inspectorOutputLines(in InspectorInput, height int) []string {
	if height <= 0 {
		return nil
	}
	var lines []string
	switch {
	case in.Content == InspectorNoSelection, in.Content == InspectorVanished:
		// Both outrank the hold: a pane with no session, and a pane whose window
		// is gone, have nothing to keep holding a snapshot of.
		text, _ := previewPlaceholder(in.Content, in.Output, in.Error)
		lines = []string{text}
	case in.Viewport.Paused():
		lines = in.Viewport.Window(in.Output, height)
	default:
		if text, ok := previewPlaceholder(in.Content, in.Output, in.Error); ok {
			lines = []string{text}
		} else {
			lines = in.Viewport.Window(in.Output, height)
		}
	}

	out := make([]string, height)
	for i := range out {
		if i < len(lines) {
			// StyledTruncate rather than Truncate: a capture that carries escape
			// sequences must be measured by the cells it paints, not by its bytes,
			// and a sequence cut in half would leak into the rest of the pane.
			out[i] = layout.StyledTruncate(lines[i], in.Width, "…")
		}
	}
	return out
}

// previewPlaceholder returns the sentence that stands in for terminal output,
// and false when the output itself is what should be drawn.
//
// Four of the five situations are decided by the content state alone; the fifth
// — a read that landed on an empty buffer — is the one that needs the output
// too, and it is exactly the one an inference from emptiness would confuse with
// a read that has not landed. Reporting a bool rather than "" for "draw the
// output" is what keeps a legitimately empty buffer from falling through to a
// blank pane with no sentence in it.
func previewPlaceholder(content InspectorContent, output []string, errText string) (string, bool) {
	switch {
	case content == InspectorNoSelection:
		return styleDim.Render(previewNoSelectionText), true
	case content == InspectorVanished:
		return styleDim.Render(previewVanishedText), true
	case content == InspectorFailed:
		return stylePreviewError.Render(previewErrorPrefix + errText), true
	case content == InspectorLoading:
		return styleDim.Render(previewLoadingText), true
	case len(output) == 0:
		return styleDim.Render(previewEmptyText), true
	default:
		return "", false
	}
}
