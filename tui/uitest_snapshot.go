package tui

// This file assembles a full harness snapshot of the live model: the normalized
// screen (uitest_screen.go) with AMX-specific modal-fit issues (uitest_modal.go)
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
	screen := NormalizeUITestScreen(m.render(), size)
	if issues := detectModalFitIssues(m); len(issues) > 0 {
		screen.Issues = append(screen.Issues, issues...)
	}
	return screen
}
