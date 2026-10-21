//go:build tuitest

// This file is what a rendered frame IS to a scenario: the normalized screen,
// the modal box located inside it, the layout issues a frame can exhibit, and
// the observation record a single action leaves behind.

package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/sglavoie/agentmux/tui/layout"
)

// parseUITestSize parses a "WIDTHxHEIGHT" terminal size spec (e.g. "80x24").
// The separator is case-insensitive so "80X24" is accepted too. Both
// dimensions must be positive integers.
func parseUITestSize(spec string) (UITestSize, error) {
	w, h, ok := strings.Cut(strings.ToLower(strings.TrimSpace(spec)), "x")
	if !ok {
		return UITestSize{}, fmt.Errorf("uitest: size %q must look like WIDTHxHEIGHT", spec)
	}
	width, err := strconv.Atoi(strings.TrimSpace(w))
	if err != nil || width <= 0 {
		return UITestSize{}, fmt.Errorf("uitest: invalid width in size %q", spec)
	}
	height, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || height <= 0 {
		return UITestSize{}, fmt.Errorf("uitest: invalid height in size %q", spec)
	}
	return UITestSize{Width: width, Height: height}, nil
}

// normalizeUITestScreen turns a raw rendered view into a normalized, JSON-ready
// observation. It strips ANSI escape sequences, measures each line by display
// cells (so emoji, wide glyphs, and combining marks match production rendering
// via layout.Width), flags invalid UTF-8, and records root-level width/height
// overflow for the declared terminal size.
//
// It deliberately does not inspect modal geometry: screen-wide width and height
// checks are necessary but not sufficient, and model-aware modal fit detection
// is layered on top of these observations in a later session.
func normalizeUITestScreen(raw string, size UITestSize) UITestScreen {
	// Normalize CRLF and drop a single trailing newline so a view that ends in
	// "\n" does not report a phantom empty final line. Split before stripping
	// ANSI so invalid-UTF-8 detection sees the original bytes; ansi.Strip
	// sanitizes stray bytes, which would otherwise mask the problem.
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.TrimSuffix(normalized, "\n")

	rawLines := strings.Split(normalized, "\n")
	lines := make([]string, len(rawLines))

	screen := UITestScreen{
		Size:   size,
		Lines:  lines,
		Height: len(rawLines),
	}

	for i, rawLine := range rawLines {
		if !utf8.ValidString(rawLine) {
			screen.Issues = append(screen.Issues, UITestLayoutIssue{
				Type:   UITestIssueInvalidUTF8,
				Line:   i,
				Detail: "line contains invalid UTF-8",
			})
		}
		line := ansi.Strip(rawLine)
		lines[i] = line
		w := layout.Width(line)
		if w > screen.Width {
			screen.Width = w
		}
		if size.Width > 0 && w > size.Width {
			screen.Issues = append(screen.Issues, UITestLayoutIssue{
				Type:   UITestIssueLineWidthOverflow,
				Line:   i,
				Actual: w,
				Limit:  size.Width,
			})
		}
	}

	if size.Height > 0 && len(lines) > size.Height {
		screen.Issues = append(screen.Issues, UITestLayoutIssue{
			Type:   UITestIssueScreenHeightOverflow,
			Actual: len(lines),
			Limit:  size.Height,
			Detail: "rendered line count exceeds terminal height",
		})
	}

	return screen
}

// AMX-specific modal-fit detection. The screen-level checks in uitest_screen.go
// (line width, invalid UTF-8, screen height) are necessary but not sufficient:
// model_preview.go's render() composites overlay popups into the dashboard /
// session-search body via layout.CenterOverlay, which right-clips wide popup columns
// and drops popup rows past the body area. A modal whose natural content is
// wider or taller than the space it is given therefore renders as a *clean*
// root screen even though content was squeezed, reflowed, or dropped. That is
// exactly the class of bug the profile-picker and actions-overlay sizing fixes
// (1f08e24, 26d9545) addressed.
//
// detectModalFitIssues re-renders the active modal with production's own box
// renderers — once at an unconstrained viewport (to recover the width the modal
// naturally wants, before its own m.width-4 clamp fires) and once at the live
// viewport (to measure the width, height, and placement that actually compose
// into the body area) — and reports the fit failures composition would hide.

// uitestModalNaturalSize is the viewport the natural-size probe renders at. It
// is far larger than any real terminal so the box renderers' own m.width-4
// clamp never fires and we observe the width the modal would use with unlimited
// room.
const uitestModalNaturalSize = 10000

// modalBoxRenderer returns the production renderer for a bounded-popup modal
// mode, or nil when the mode is not composited as a clipped overlay box. The
// covered modes match model_preview.go's render() dispatch: every mode there
// whose popup is spliced into a base view via layout.CenterOverlay or
// layout.TopCenterOverlay. ModeMessageDetail is intentionally excluded — it renders as
// a full-height view (renderMessageDetail), not a bounded box, so its overflow
// is already caught by the screen-level height/width checks.
func modalBoxRenderer(mode Mode) func(Model) string {
	switch mode {
	case ModeActions:
		return renderActionsBox
	case ModeConfirm:
		return renderConfirmBox
	case ModeConfigPicker:
		return renderConfigPickerBox
	case ModeHelp:
		return renderHelpBox
	case ModeDetail:
		return renderDetailBox
	case ModeDeleteChoice:
		return renderDeleteChoiceBox
	case ModeChipPicker:
		return renderChipPickerBox
	case ModeCommandPalette:
		return renderCommandPaletteBox
	case ModeLiveSessionPicker:
		return renderLiveSessionPickerBox
	case ModeASLResponse:
		return renderASLResponseBox
	case ModeSend:
		return renderSendBox
	case ModePlanRun:
		return renderPlanRunBox
	case ModeProjectSwitcher:
		return renderProjectSwitcherBox
	case ModeFeedback:
		return renderFeedbackBox
	case ModeHealth:
		return renderHealthBox
	default:
		return nil
	}
}

// modalIsTopAnchored reports whether a modal mode is placed one row from the top
// of the body area (layout.TopCenterOverlay) rather than vertically centered
// (layout.CenterOverlay). Only the config picker is top-anchored; see model_preview.go.
func modalIsTopAnchored(mode Mode) bool {
	return mode == ModeConfigPicker
}

// detectModalFitIssues reports the modal-specific layout problems root
// composition would hide for the model's active mode. It returns nil for
// non-modal modes and for modals that fit.
//
// Width is judged by rendering the box twice: the modal is squeezed when its
// unconstrained (natural) width exceeds the width it was actually rendered at,
// which is the box's own m.width-4 clamp firing. Height and placement come from
// the live render because those are what compose into — and get clipped by —
// the body area (rootBodyHeight).
func detectModalFitIssues(m Model) []UITestLayoutIssue {
	render := modalBoxRenderer(m.mode)
	if render == nil || m.width <= 0 {
		return nil
	}

	// Natural width: render at an unconstrained viewport so the box renderer's
	// own m.width-4 clamp never fires. Only the width is taken from this probe;
	// its height would reflect the unlimited room, not the live layout.
	natural := m
	natural.width = uitestModalNaturalSize
	natural.height = uitestModalNaturalSize
	naturalW := layout.PopupWidth(render(natural))

	// Live geometry: exactly what layout.CenterOverlay splices into the body area.
	live := render(m)
	realW := layout.PopupWidth(live)
	realH := popupLineCount(live)

	mode := m.mode.String()
	var issues []UITestLayoutIssue

	if naturalW > realW && !modalUsesResponsiveWidth(m.mode) {
		issues = append(issues, UITestLayoutIssue{
			Type:   UITestIssueModalWidthOverflow,
			Actual: naturalW,
			Limit:  realW,
			Detail: fmt.Sprintf("%s modal wants %d cols, box clamped to %d", mode, naturalW, realW),
		})
	}

	availH := rootBodyHeight(m)
	if availH <= 0 {
		return issues
	}

	// Placement mirrors layout.CenterOverlay / layout.TopCenterOverlay in overlay.go so the
	// clip decision matches production compositing.
	startRow := (availH - realH) / 2
	if modalIsTopAnchored(m.mode) {
		if availH > 1 && realH < availH {
			startRow = 1
		} else {
			startRow = 0
		}
	}
	if startRow < 0 {
		startRow = 0
	}

	switch {
	case realH > availH:
		issues = append(issues, UITestLayoutIssue{
			Type:   UITestIssueModalHeightOverflow,
			Actual: realH,
			Limit:  availH,
			Detail: fmt.Sprintf("%s modal needs %d rows, body area offers %d", mode, realH, availH),
		})
	case startRow+realH > availH:
		issues = append(issues, UITestLayoutIssue{
			Type:   UITestIssueModalClippedByRoot,
			Actual: startRow + realH,
			Limit:  availH,
			Detail: fmt.Sprintf("%s modal placement drops %d row(s) past the body area", mode, startRow+realH-availH),
		})
	}

	return issues
}

// modalUsesResponsiveWidth reports modals whose narrow rendering is a complete
// alternate layout rather than a clipped version of their natural-width
// content. The command palette deliberately wraps descriptions and disabled
// reasons to the live content width, so its m.width-4 clamp is not overflow.
// The health details do the same with their diagnostic lines, and for a sharper
// reason: half a remedy or half an error is not a shorter reading of the issue,
// it is a different and wrong one.
// The actions menu is here for the same reason as the palette: it wraps the
// selected entry's description and its unavailability reason to the live
// content width and sheds its own chrome rather than rendering a fixed 72-column
// block and letting the box clip it. A narrow reading of it is a complete menu,
// not a truncated one.
func modalUsesResponsiveWidth(mode Mode) bool {
	return mode == ModeConfigPicker || mode == ModeCommandPalette || mode == ModeActions || mode == ModeLiveSessionPicker || mode == ModeASLResponse || mode == ModeFeedback || mode == ModeHealth || mode == ModeSend || mode == ModePlanRun
}

// popupLineCount returns the number of rows a popup box occupies, matching the
// popup height layout.CenterOverlay/layout.TopCenterOverlay compute (strings.Count + 1 for a
// non-empty popup). An empty popup is zero rows.
func popupLineCount(popup string) int {
	if popup == "" {
		return 0
	}
	return strings.Count(popup, "\n") + 1
}

// This file captures a normalized, JSON-ready observation of the live model.
// It bridges the pure contract in uitest_types.go (UITestScreen, UITestSize) to
// the real dashboard Model, so the in-process driver can record what the screen
// looked like after each scenario step without any terminal event loop.

// UITestObservation is a JSON-ready snapshot of the model after an action: the
// terminal size the model was rendered at, the active UI mode, the cursor
// position relevant to that mode, and the normalized screen with any layout
// issues attached — both the screen-level checks (line/height overflow, invalid
// UTF-8) and the model-aware modal-fit issues detectModalFitIssues reports.
//
// SelectedAction names the action the actions menu currently has under its
// cursor, by shared identity rather than by row number. A scenario that asserted
// "cursor is 3" would keep passing after the menu's grouping changed underneath
// it while selecting something else entirely; the identity is what the operator
// is about to run. It is empty outside ModeActions.
type UITestObservation struct {
	Size           UITestSize   `json:"size"`
	Mode           string       `json:"mode"`
	Cursor         int          `json:"cursor,omitempty"`
	SelectedAction string       `json:"selected_action,omitempty"`
	Screen         UITestScreen `json:"screen"`
}

// observeUITest renders m at the given size and normalizes the result into a
// stable observation. size is passed explicitly (rather than read from m.width /
// m.height) so callers can record the size they intended even if a resize is
// still settling. The screen carries both screen-level and modal-fit layout
// issues via captureUITestScreen.
func observeUITest(m Model, size UITestSize) UITestObservation {
	return UITestObservation{
		Size:           size,
		Mode:           m.mode.String(),
		Cursor:         uitestCursor(m),
		SelectedAction: uitestSelectedAction(m),
		Screen:         captureUITestScreen(m, size),
	}
}

// uitestSelectedAction reports the identity of the menu entry under the cursor,
// and nothing at all in any other mode. It reads the same input value the
// renderer draws from, so what a scenario asserts and what the operator sees
// selected cannot come apart.
func uitestSelectedAction(m Model) string {
	if m.mode != ModeActions {
		return ""
	}
	return string(actionsInput(m).SelectedID())
}

// uitestCursor reports the cursor position that matters for the active mode.
// Most modes track the dashboard list cursor; the picker and config picker keep
// their own so scenarios can assert on the row the user is actually navigating.
func uitestCursor(m Model) int {
	switch m.mode {
	case ModeActions:
		return m.actionsCursor
	case ModePicker:
		return m.pickerCursor
	case ModeConfigPicker:
		return m.configPickerCursor
	case ModeCommandPalette:
		return m.commandCursor
	case ModeWorktrees:
		return m.worktreeCursor
	case ModeLiveSessionPicker:
		return m.livePickerCursor
	case ModeASLRuns:
		return m.aslCursor
	case ModeASLResponse:
		return m.aslResponse.QuestionIdx
	case ModeProjectSwitcher:
		// Derived from the stored scope identity rather than stored as an index,
		// for the same reason the renderer derives it: a refresh that reorders or
		// drops scopes must not leave a scenario asserting on a row number that
		// now names a different project.
		return projectSwitcherCursor(m)
	default:
		return m.cursor
	}
}

// This file assembles a full harness snapshot of the live model: the normalized
// screen with AMX-specific modal-fit issues (detectModalFitIssues, below)
// layered on. Keeping the composition here lets observeUITest stay a thin bridge
// and gives the scenario runner a single entry point for "capture everything the
// harness knows about the current frame."

// captureUITestScreen renders m at size, normalizes the result, and augments it
// with modal-fit issues detected from the live model. Screen-level issues
// (line/height overflow, invalid UTF-8) come first, in render order; modal-fit
// issues are appended so a reviewer sees the low-level overflow before the
// model-aware diagnosis. The two never mask each other: a clean root screen can
// still carry modal issues, which is the whole point of the modal detector, and
// the no_layout_issues assertion reads this combined Screen.Issues slice.
func captureUITestScreen(m Model, size UITestSize) UITestScreen {
	screen := normalizeUITestScreen(m.render(), size)
	if issues := detectModalFitIssues(m); len(issues) > 0 {
		screen.Issues = append(screen.Issues, issues...)
	}
	return screen
}
