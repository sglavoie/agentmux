package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/flightview"
	"github.com/sglavoie/agentmux/tui/layout"
)

// View renders the full dashboard.
func (m Model) View() tea.View {
	frame := renderRootFrame(m)
	v := tea.NewView(frame.text)
	v.AltScreen = true
	// The two surfaces that publish hit regions are the two that take the mouse.
	// Asking the frame rather than the mode would be the same answer today, but
	// this way a mode that grows targets has to say so here as well, and a mode
	// that has none cannot leave cell-motion reporting on for nothing.
	if m.mode == ModeList || m.mode == ModeProjectSwitcher {
		v.MouseMode = tea.MouseModeCellMotion
		v.OnMouse = frame.hitMap.handler()
	}
	return v
}

// render builds the string content for View. The scenario harness captures the
// screen through here, so it deliberately shares renderRootFrame with View
// rather than composing its own: a committed scenario that asserted a different
// composition from the one an operator sees would assert nothing.
func (m Model) render() string {
	return renderRootFrame(m).text
}

// rootFrame is one composed screen: the text the terminal will show and, when
// the live dashboard owns it, the mouse regions the body produced beside that
// text and clipped to the body it was composed into.
type rootFrame struct {
	text   string
	hitMap mouseHitMap
}

// renderRootFrame composes the whole screen once. The bottom-of-screen chrome
// is measured a single time and both halves of the composition read that one
// measurement — the rows the body is clipped to and the rows an overlay is
// centred in are then the same number by construction rather than two
// independent answers to the same question.
func renderRootFrame(m Model) rootFrame {
	chrome := newRootChrome(m)
	if m.mode == ModeList {
		body := renderDashboardFrame(m)
		return rootFrame{
			text:   chrome.compose(body.text),
			hitMap: body.hitMap.clipped(m.width, chrome.bodyHeight()),
		}
	}
	// The project switcher is the one overlay whose popup carries click targets,
	// so it is composed here rather than in renderRootBody's switch: the regions
	// are produced by the same call that places the popup, and the placement is
	// what they are measured from.
	if m.mode == ModeProjectSwitcher {
		body, hitMap := renderProjectSwitcherFrame(m, chrome)
		return rootFrame{
			text:   chrome.compose(body),
			hitMap: hitMap.clipped(m.width, chrome.bodyHeight()),
		}
	}
	return rootFrame{text: chrome.compose(renderRootBody(m, chrome))}
}

// renderRootBody draws the body of whichever non-ModeList screen owns the
// terminal, including the overlay modes, which composite their popup over the
// backdrop the mode they were raised from owns.
func renderRootBody(m Model, chrome rootChrome) string {
	var sb strings.Builder

	switch m.mode {
	case ModeFlightLog:
		sb.WriteString(flightview.Render(m.flightInput(), m.width, chrome.bodyHeight()))
	case ModePicker:
		// Picker replaces both header and list.
		sb.WriteString(renderPicker(m))
	case ModeSessionSearch:
		// Session search owns its full layout (header + chips + query +
		// results) so the dashboard list is not rendered below.
		sb.WriteString(renderSessionSearch(m))
	case ModeWorktrees:
		sb.WriteString(renderWorktrees(m))
	case ModeASLRuns:
		sb.WriteString(renderASLRuns(m))
	case ModeLiveSessionPicker:
		base := rootBaseView(m)
		popup := renderLiveSessionPickerBox(m)
		sb.WriteString(layout.CenterOverlay(base, popup, m.width, chrome.bodyHeight(), true))
	case ModePlanSearch:
		// Plan search owns its full layout — like ModeSessionSearch, the
		// dashboard list must not render underneath.
		sb.WriteString(renderPlanSearch(m))
	case ModePlanView:
		// Plan viewer owns its full layout — like ModeSessionView, the
		// dashboard list must not render underneath.
		sb.WriteString(renderPlanView(m))
	case ModeSessionView:
		// Viewer owns its full layout — like ModeSessionSearch, the
		// dashboard list must not render underneath.
		sb.WriteString(renderSessionView(m))
	case ModeMessageDetail:
		sb.WriteString(renderMessageDetail(m))
	case ModeDeleteChoice, ModeChipPicker:
		// Modal overlays on top of its owning search view. The base view is
		// rendered then dimmed; the bordered popup is composited via
		// line-splicing so the underlying list stays visible around it.
		// See tui/overlay.go for the splicing algorithm.
		//
		// These two resolve their owning view themselves rather than through
		// activeRootMode — the chip picker's is recorded on the picker, not on
		// the mode — but the view is drawn through the same dispatch as every
		// other backdrop.
		source := ModeSessionSearch
		if m.mode == ModeChipPicker {
			source = chipPickerSourceMode(m)
		}
		base := rootBaseViewFor(m, source)
		var popup string
		if m.mode == ModeChipPicker {
			popup = renderChipPickerBox(m)
		} else {
			popup = renderDeleteChoiceBox(m)
		}
		sb.WriteString(layout.CenterOverlay(base, popup, m.width, chrome.bodyHeight(), true))
	case ModeActions, ModeConfirm, ModeConfigPicker, ModeHelp, ModeDetail, ModeCommandPalette, ModeASLResponse, ModeFeedback, ModeHealth, ModeSend, ModeReviewNote, ModePlanRun:
		// Every one of these raises a popup over the view esc returns to, and
		// activeRootMode is the one place that names that view — including for
		// help, whose backdrop is its own return mode, and for the config
		// picker raised from the launcher.
		base := rootBaseView(m)
		var popup string
		switch m.mode {
		case ModeActions:
			popup = renderActionsBox(m)
		case ModeConfirm:
			popup = renderConfirmBox(m)
		case ModeConfigPicker:
			popup = renderConfigPickerBox(m)
		case ModeHelp:
			popup = renderHelpBox(m)
		case ModeDetail:
			popup = renderDetailBox(m)
		case ModeCommandPalette:
			popup = renderCommandPaletteBox(m)
		case ModeASLResponse:
			popup = renderASLResponseBox(m)
		case ModeReviewNote:
			popup = renderReviewNoteBox(m)
		case ModeSend:
			popup = renderSendBox(m)
		case ModePlanRun:
			popup = renderPlanRunBox(m)
		case ModeFeedback:
			popup = renderFeedbackBox(m)
		case ModeHealth:
			popup = renderHealthBox(m)
		}
		if m.mode == ModeConfigPicker {
			// Top-anchored so the modal doesn't jump when the harness-switch
			// warning is appended/removed.
			sb.WriteString(layout.TopCenterOverlay(base, popup, m.width, chrome.bodyHeight(), true))
		} else {
			sb.WriteString(layout.CenterOverlay(base, popup, m.width, chrome.bodyHeight(), true))
		}
	default:
		// Header area varies by mode; list is always shown below. ModeList never
		// arrives here — renderRootFrame routes it to the canonical dashboard
		// frame, which is where the chrome between the header and the list is
		// drawn.
		switch m.mode {
		case ModeSearch:
			sb.WriteString(renderSearch(m))
		default:
			sb.WriteString(renderHeader(m))
		}
		sb.WriteString(renderList(m))
	}

	return sb.String()
}

// rootBaseView renders the full-layout view that currently owns the screen, so
// an overlay raised from Worktrees or Runs dims that view rather than the
// dashboard the operator is not looking at.
func rootBaseView(m Model) string {
	return rootBaseViewFor(m, m.activeRootMode())
}

// rootBaseViewFor is the one place that answers "which view owns the screen
// behind this overlay". Both callers reach it: the overlay composition through
// activeRootMode, and the help overlay through its own recorded return mode.
// They used to be separate switches over overlapping sets of modes, so a
// backdrop could be correct under esc-from-help and wrong under esc-from-
// confirm for the very same screen.
func rootBaseViewFor(m Model, mode Mode) string {
	switch mode {
	case ModePicker:
		return renderPicker(m)
	case ModeSessionSearch:
		return renderSessionSearch(m)
	case ModeSessionView:
		return renderSessionView(m)
	case ModePlanSearch:
		return renderPlanSearch(m)
	case ModePlanView:
		return renderPlanView(m)
	case ModeWorktrees:
		return renderWorktrees(m)
	case ModeASLRuns:
		return renderASLRuns(m)
	default:
		return renderDashboard(m)
	}
}

// rootChrome is the bottom-of-screen budget a composed screen is laid out
// against: the reflowed footer rows and the sticky status row, measured once.
//
// Measuring once is not only a cost argument, though the ModeList footer is the
// expensive thing on the frame — its position indicator is derived from the
// list height, which is derived from the footer's own row count, so measuring
// it iterates to a fixpoint and each iteration re-renders the chrome above the
// list. It is also a correctness one: the composition clips the body to the
// rows the footer left it, and an overlay is centred in the rows the footer
// left it, and those were two separate calculations over two separate
// measurements of the same footer.
type rootChrome struct {
	// footer holds the footer rows already trimmed to what the terminal can
	// actually show, so len(footer) is what the composition will spend rather
	// than what the footer asked for.
	footer []string
	status string
	// statusAbove draws the status row above the footer rather than under it.
	//
	// That is the dashboard's chrome, and the difference is what the row is for.
	// Under the footer it is a sticky status line the eye reads as part of the
	// frame; above it, resting directly on the list, it is a feedback slot —
	// blank while nothing has happened, and the first thing the eye lands on
	// when something has. Every other root keeps the sticky line, so the two
	// readings never appear on one screen.
	statusAbove bool
	// height is the terminal height the trim was resolved against. A
	// non-positive height means "not yet resized": nothing is clipped and the
	// body keeps its natural length.
	height int
}

func newRootChrome(m Model) rootChrome {
	c := rootChrome{
		footer:      layout.SectionLines(renderFooter(m)),
		status:      renderStatusBar(m),
		statusAbove: dashboardOwnsChrome(m),
		height:      m.height,
	}
	if c.height > 0 {
		budget := c.height - statusBarLines
		if budget < 0 {
			budget = 0
		}
		if len(c.footer) > budget {
			c.footer = c.footer[len(c.footer)-budget:]
		}
	}
	return c
}

// bodyHeight is the rows left for the body above the footer and status row.
func (c rootChrome) bodyHeight() int {
	if c.height <= 0 {
		return 0
	}
	h := c.height - len(c.footer) - statusBarLines
	if h < 0 {
		return 0
	}
	return h
}

// compose stacks body, padding, footer, and status into the final screen,
// clipping the body to bodyHeight and padding it out to the same when it is
// short. The result is exactly the terminal's height whenever there is one.
func (c rootChrome) compose(body string) string {
	bodyLines := layout.SectionLines(body)
	paddingLines := 1
	if c.height > 0 {
		budget := c.bodyHeight()
		if len(bodyLines) > budget {
			bodyLines = bodyLines[:budget]
		}
		paddingLines = budget - len(bodyLines)
	}

	lines := make([]string, 0, len(bodyLines)+paddingLines+len(c.footer)+statusBarLines)
	lines = append(lines, bodyLines...)
	for i := 0; i < paddingLines; i++ {
		lines = append(lines, "")
	}
	if c.statusAbove {
		lines = append(lines, c.status)
		lines = append(lines, c.footer...)
	} else {
		lines = append(lines, c.footer...)
		lines = append(lines, c.status)
	}
	return strings.Join(lines, "\n")
}

// dashboardOwnsChrome reports that the screen belongs to the dashboard — the
// list itself, its search input, the project switcher, and every overlay raised
// over any of them, including the full-screen inspector.
//
// It asks activeRootMode rather than m.mode so an overlay inherits the chrome of
// the view it was raised from. An overlay does not redraw the bottom rows, so
// opening the command palette over the dashboard and over Past Sessions would
// otherwise shuffle the two rows under it in one case and not the other, which
// is the frame moving for a reason that has nothing to do with the frame.
func dashboardOwnsChrome(m Model) bool {
	switch m.mode {
	case ModeActions, ModeDetail:
		// Neither is reachable from anywhere else and both esc back to the list,
		// so they are dashboard overlays that happen not to record a return mode.
		// They are named here rather than taught to activeRootMode because that
		// answer is also what decides which root gets re-rendered underneath, and
		// these two already answer it correctly.
		return true
	}
	switch m.activeRootMode() {
	case ModeList, ModeSearch, ModeProjectSwitcher:
		return true
	default:
		return false
	}
}

// rootBodyHeight is the body-row budget for callers that are sizing one surface
// rather than composing a whole frame. renderRootFrame does not use it: it
// holds a rootChrome already and asking again would measure the footer twice.
func rootBodyHeight(m Model) int {
	return newRootChrome(m).bodyHeight()
}

// rootFooterLines is the footer rows the composition will spend, for the same
// one-surface callers. Trimmed, because a row the terminal cannot show is not a
// row anything downstream should budget for.
func rootFooterLines(m Model) []string {
	return newRootChrome(m).footer
}
