package tui

// AMX-specific modal-fit detection. The screen-level checks in uitest_screen.go
// (line width, invalid UTF-8, screen height) are necessary but not sufficient:
// model_preview.go's render() composites overlay popups into the dashboard /
// session-search body via composeOverlay, which right-clips wide popup columns
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

import (
	"fmt"
	"strings"
)

// uitestModalNaturalSize is the viewport the natural-size probe renders at. It
// is far larger than any real terminal so the box renderers' own m.width-4
// clamp never fires and we observe the width the modal would use with unlimited
// room.
const uitestModalNaturalSize = 10000

// modalBoxRenderer returns the production renderer for a bounded-popup modal
// mode, or nil when the mode is not composited as a clipped overlay box. The
// covered modes match model_preview.go's render() dispatch: every mode there
// whose popup is spliced into a base view via centerOverlay or
// topCenterOverlay. ModeMessageDetail is intentionally excluded — it renders as
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
	default:
		return nil
	}
}

// modalIsTopAnchored reports whether a modal mode is placed one row from the top
// of the body area (topCenterOverlay) rather than vertically centered
// (centerOverlay). Only the config picker is top-anchored; see model_preview.go.
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
	naturalW := popupWidth(render(natural))

	// Live geometry: exactly what composeOverlay splices into the body area.
	live := render(m)
	realW := popupWidth(live)
	realH := popupLineCount(live)

	mode := uitestModeName(m.mode)
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

	// Placement mirrors centerOverlay / topCenterOverlay in overlay.go so the
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
func modalUsesResponsiveWidth(mode Mode) bool {
	return mode == ModeCommandPalette
}

// popupLineCount returns the number of rows a popup box occupies, matching the
// popup height centerOverlay/topCenterOverlay compute (strings.Count + 1 for a
// non-empty popup). An empty popup is zero rows.
func popupLineCount(popup string) int {
	if popup == "" {
		return 0
	}
	return strings.Count(popup, "\n") + 1
}
