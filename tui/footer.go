// This file is the root footer: the hint line under every mode. It holds the
// entry vocabulary a palette is written in, the binding lookup that resolves an
// entry's key display, and the wrapping that fits the chosen palette into the
// terminal's width without ever splitting a binding across lines.

package tui

import (
	"strings"

	"github.com/sglavoie/agentmux/tui/chrome"
)

func renderFooter(m Model) string {
	var lines []string
	switch m.mode {
	case ModeFlightLog:
		lines = nonEmptyFooterLines(footerLine(m.width, " · ", "1/2/3 views", "p period", "f project", "h harness", "a profile", "j/k move", "enter scorecard", "e events", "s conversation", "r refresh", "esc back"))
	case ModeSearch:
		lines = nonEmptyFooterLines(
			footerLine(m.width, " · ", "filter: @profile", "#project", "%harness", "!state (!attention)", "word", "\\@ \\# \\% \\! literal"),
			footerStyledLine(m.width, " · ", chrome.Binding("enter", "apply"), chrome.Binding("esc", "cancel")),
		)
	case ModeConfirm:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, "   ",
			chrome.BindingWithLabel("[y]", "", "es", styleFooter),
			chrome.BindingWithLabel("[n]", "", "o", styleFooter),
			chrome.Binding("[esc]", "cancel"),
		))
	case ModePicker:
		if m.pickerDirMode {
			lines = nonEmptyFooterLines(
				footerStyledLine(m.width, " · ",
					chrome.Binding("enter", "apply"), chrome.Binding("tab", "complete"),
					chrome.Binding("ctrl+u", "clear"), chrome.Binding("esc", "cancel"),
				),
				footerLine(m.width, " · ", "launch directory", "relative paths start at the project's directory", "empty restores it"),
			)
			break
		}
		if m.pickerSearchMode {
			lines = nonEmptyFooterLines(
				footerStyledLine(m.width, " · ", chrome.Binding("enter", "apply"), chrome.Binding("esc", "cancel")),
				footerStyledLine(m.width, " · ", styleFooter.Render("DSL: @profile, word")),
			)
			break
		}
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ",
				chrome.Binding("space", "toggle"), chrome.Binding("enter", "launch"),
				chrome.Binding("ctrl+e", "settings"),
				chrome.Binding("f", "profile"), chrome.Binding("P", "pin"),
				chrome.Binding("s", "sessions"), chrome.Binding("C", "override"),
				chrome.Binding("d", "directory"),
				chrome.Binding("o", "open dirs"), chrome.Binding("O", "open in editor"),
				chrome.Binding("p", "preview"), chrome.Binding("esc", "clear/back"),
				chrome.Binding("?", "help"),
			),
			footerStyledLine(m.width, " · ", styleFooter.Render("DSL: @profile, word"), chrome.Binding("j/k", "up/down"), chrome.Binding("g/G", "top/bottom")),
		)
	case ModeConfigPicker:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ", chrome.Binding("enter", "select"), chrome.Binding("esc/q", "cancel")),
			footerStyledLine(m.width, " · ", chrome.Binding("j/k", "up/down")),
		)
	case ModeLiveSessionPicker:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ", chrome.Binding("enter", "focus"), chrome.Binding("esc/q", "cancel")),
			footerStyledLine(m.width, " · ", chrome.Binding("j/k", "up/down")),
		)
	case ModeHelp:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, " · ", chrome.Binding("j/k", "up/down"), chrome.Binding("g/G", "top/bottom"), chrome.Binding("esc/?/q", "close")))
	case ModeActions:
		// The menu carries its own hint line — select, run, the letters, and how
		// to close — so this row said the same three things a second time, in a
		// second wording ("back" for the same key the menu called "close"). It is
		// blank for the reason ModeDetail's is: the modal is the thing being read,
		// and the outer chrome repeating it is not a second reminder but a second
		// source of truth. The menu's hint is now the only statement of its keys,
		// which is why the shed ladder gives up the header before it.
		lines = nil
	case ModeCommandPalette:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, " · ", chrome.Binding("↑/↓ or ctrl+j/k or ctrl+n/p", "up/down"), chrome.Binding("enter", "run"), chrome.Binding("esc", "close"), chrome.Binding("ctrl+c", "quit")))
	case ModeDetail:
		// Inner box footer already carries the scroll position and detail
		// keymap; the outer hint area stays blank so the dimmed dashboard
		// footer doesn't surface irrelevant row-action keys.
		lines = nil
	case ModeChipPicker:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ", chrome.Binding("enter", "select"), chrome.Binding("esc/q", "cancel")),
			footerStyledLine(m.width, " · ", chrome.Binding("/", "search"), chrome.Binding("j/k", "up/down"), chrome.Binding("g/G", "top/bottom"), chrome.Binding("ctrl+f/b", "page")),
		)
	case ModeProjectSwitcher:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ", chrome.Binding("enter", "apply scope"), chrome.Binding("esc/q/p", "cancel")),
			footerStyledLine(m.width, " · ", chrome.Binding("/", "search"), chrome.Binding("j/k", "up/down"), chrome.Binding("g/G", "top/bottom"), chrome.Binding("ctrl+f/b", "page")),
		)
	case ModeFeedback:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ", chrome.Binding("tab", "next notice"), chrome.Binding("j/k", "scroll"), chrome.Binding("enter", "inspect"), chrome.Binding("u", "recover")),
			footerStyledLine(m.width, " · ", chrome.Binding("y/Y", "copy/all"), chrome.Binding("d/D", "dismiss/all"), chrome.Binding("h", "health"), chrome.Binding("esc/q", "close")),
		)
	case ModeHealth:
		lines = nonEmptyFooterLines(
			footerStyledLine(m.width, " · ", chrome.Binding("j/k", "up/down"), chrome.Binding("g/G", "top/bottom"), chrome.Binding("ctrl+f/b", "page")),
			footerStyledLine(m.width, " · ", healthDismissFooterBinding(m), chrome.Binding("esc/q", "close")),
		)
	case ModeSessionSearch:
		if m.sessionSearchMode {
			lines = footerHintLines(m, queryTypingFooterPalette, footerHintMaxLines)
			break
		}
		lines = footerHintLines(m, sessionSearchFooter(m), footerHintMaxLines)
	case ModeSessionView:
		lines = footerHintLines(m, sessionViewFooterPalette, footerHintMaxLines)
	case ModePlanSearch:
		if m.planSearchMode {
			lines = footerHintLines(m, queryTypingFooterPalette, footerHintMaxLines)
			break
		}
		lines = footerHintLines(m, planSearchFooter(m), footerHintMaxLines)
	case ModePlanView:
		lines = footerHintLines(m, planViewFooterPalette, footerHintMaxLines)
	case ModeWorktrees:
		if m.worktreeFilterMode {
			lines = footerHintLines(m, queryTypingFooterPalette, footerHintMaxLines)
			break
		}
		lines = footerHintLines(m, worktreesFooter(m), footerHintMaxLines)
	case ModeASLRuns:
		lines = footerHintLines(m, aslRunsFooterPalette, footerHintMaxLines)
	case ModeASLResponse:
		lines = footerHintLines(m, aslResponseFooterPalette, footerHintMaxLines)
	case ModeSend:
		lines = footerHintLines(m, sendFooterPalette, footerHintMaxLines)
	case ModePlanRun:
		lines = footerHintLines(m, planRunFooterPalette, footerHintMaxLines)
	case ModeMessageDetail:
		lines = nonEmptyFooterLines(footerStyledLine(m.width, " · ", chrome.Binding("j/k", "scroll"), chrome.Binding("ctrl+d/u", "half"), chrome.Binding("g/G", "top/bottom"), chrome.Binding("y", "copy"), chrome.Binding("esc/q/enter", "close")))
	case ModeDeleteChoice:
		lines = nonEmptyFooterLines(footerLineFromTokens(m.width, " · ",
			footerBindingToken("[i]", "index only"),
			footerCustomBindingToken("[f]", " ", "also delete file", styleError),
			footerBindingToken("[esc]", "cancel"),
		))
	default: // ModeList
		position := ""
		if !m.inspectorFullScreenActive() {
			position = listPositionFragment(m)
		}
		lines = listModeFooterLines(m, position)
	}

	var sb strings.Builder
	if len(lines) > 0 {
		// Every root-level footer shares the same alternating background so
		// navigation hints remain visually consistent as the user moves
		// between dashboard views and transient overlays.
		lines = zebraFooterLines(lines, m.width)
		sb.WriteString(strings.Join(lines, "\n"))
	}
	return sb.String()
}
