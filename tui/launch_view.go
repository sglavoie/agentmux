package tui

import (
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
)

// This file is the model's seam onto tui/launch. The launcher's vocabulary —
// how a project row is painted, how the filtered view is windowed and scrolled,
// which projects a launch or open would target, what the persisted launcher
// preferences are, and how the profile-override overlay reads — lives in the
// leaf and takes explicit input values. What a launcher keypress DOES stays
// here: the model mutations, the tea.Cmds, and the toast list.

// pickerInput projects the model onto the launcher's input value. The pane
// height is charged here rather than in the leaf because only the root knows
// how many lines the reflowed footer and the sticky status bar took.
func (m Model) pickerInput() launch.PickerInput {
	return launch.PickerInput{
		Sessions:        m.pickerSessions,
		Running:         m.pickerRunning,
		DirProblems:     m.pickerDirProblems,
		Selected:        m.pickerSelected,
		Pinned:          m.pickerPinned,
		ProfileOverride: m.pickerProfileOverride,
		DirOverride:     m.pickerDirOverride,
		FilteredIdx:     m.pickerFilteredIdx,
		Cursor:          m.pickerCursor,
		Offset:          m.pickerOffset,
		Filter:          m.pickerFilter,
		ProfileFilter:   m.pickerProfileFilter,
		SearchMode:      m.pickerSearchMode,
		DirMode:         m.pickerDirMode,
		DirValue:        m.pickerDirValue,
		DirError:        m.pickerDirError,
		PreviewEnabled:  m.pickerPreviewEnabled,
		PreviewText:     m.previewText,
		PreviewError:    m.previewError,
		Width:           m.width,
		Height:          m.height,
		PaneHeight:      pickerPaneHeight(m),
		Cfg:             m.cfg,
	}
}

func renderPicker(m Model) string { return m.pickerInput().Render() }

func renderPickerList(m Model, height int) string {
	return m.pickerInput().RenderList(height)
}

func renderPickerPreview(m Model, origIdx, width, height int) string {
	return m.pickerInput().RenderPreview(origIdx, width, height)
}

// pickerPaneHeight is the launcher's body budget. It is the root body height —
// the launcher composes into the same footer and status row every other root
// view does — and says so rather than restating the subtraction.
func pickerPaneHeight(m Model) int {
	return rootBodyHeight(m)
}

// pickerFooterLineCount is the launcher's footer term in the shared vertical
// budget, taken from the trimmed rows the composition will actually spend.
func pickerFooterLineCount(m Model) int {
	return len(rootFooterLines(m))
}

func pickerVisibleHeight(m Model) int { return m.pickerInput().VisibleHeight() }

func clampPickerOffset(m *Model) {
	in := m.pickerInput()
	in.ClampOffset()
	m.pickerOffset = in.Offset
}

func scrollPickerToCursor(m *Model) {
	in := m.pickerInput()
	in.ScrollToCursor()
	m.pickerCursor = in.Cursor
	m.pickerOffset = in.Offset
}

func (m Model) pickerFilteredIndices(query string) []int {
	return m.pickerInput().FilteredIndices(query)
}

func (m Model) pickerEffectiveProfile(origIdx int) string {
	return m.pickerInput().EffectiveProfile(origIdx)
}

func (m Model) pickerSessionWithResolvedProfile(origIdx int) config.Session {
	return m.pickerInput().SessionWithResolvedProfile(origIdx)
}

func (m Model) pickerOpenTargets() []config.Session {
	return m.pickerInput().OpenTargets()
}

func (m Model) savePickerState(profileOverrides map[int]string, pinned map[int]bool) error {
	return m.pickerInput().SaveState(profileOverrides, pinned)
}

// activeConfigPickerPurpose resolves why the profile-override overlay is open.
// The fallback reads the mode the overlay will return to, which is root state,
// so the resolution stays here even though the vocabulary is the leaf's.
func (m Model) activeConfigPickerPurpose() launch.ConfigPickerPurpose {
	if m.configPickerPurpose != launch.ConfigPickerPurposeUnspecified {
		return m.configPickerPurpose
	}
	if m.configPickerReturnMode == ModePicker {
		return launch.ConfigPickerLauncherOverride
	}
	return launch.ConfigPickerRestartProfile
}

// configPickerInput projects the model onto the profile-override overlay's
// input value. Which name the overlay is titled after depends on the mode it
// was opened from, so the root resolves the title before handing it over.
func (m Model) configPickerInput() launch.ProfileInput {
	purpose := m.activeConfigPickerPurpose()
	title := dashboard.SessionDisplayTitle(m.targetSession)
	if purpose == launch.ConfigPickerWorktreeReopen && m.worktreeActionTarget != nil {
		title = worktreeConfirmationTitle(m.worktreeActionTarget)
	}
	return launch.ProfileInput{
		Accounts:       m.configPickerAccounts,
		Cursor:         m.configPickerCursor,
		Current:        m.configPickerCurrent,
		CurrentHarness: m.targetSession.Harness,
		Purpose:        purpose,
		Title:          title,
		Width:          m.width,
		Height:         rootBodyHeight(m),
		Cfg:            m.cfg,
	}
}

func renderConfigPicker(m Model) string { return m.configPickerInput().Render() }

func renderConfigPickerBox(m Model) string { return m.configPickerInput().RenderBox() }
