package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/transcript"
)

// enterDeleteChoice stashes the target row and switches to the three-way
// prompt. The caller is responsible for bounds-checking sessionCursor.
func (m Model) enterDeleteChoice(meta transcript.SessionMeta) (tea.Model, tea.Cmd) {
	target := meta
	m.deleteTarget = &target
	m.mode = ModeDeleteChoice
	return m, nil
}

// exitDeleteChoice returns to ModeSessionSearch without touching the index.
// Chip filters and the text query are preserved (same invariant as S7's
// enter/exit pair).
func (m *Model) exitDeleteChoice() {
	m.deleteTarget = nil
	m.mode = ModeSessionSearch
}

// handleDeleteChoiceKey is the keypress dispatcher for ModeDeleteChoice.
// Three branches: `i` hides the entry from the index (file untouched),
// `f` removes the entry and the on-disk transcript, `esc` cancels.
// `ctrl+c`/`q` always quit (mirrors handleConfirmKey).
func (m Model) handleDeleteChoiceKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		return m, tea.Quit
	case keyQ:
		return m, tea.Quit
	case keyEsc:
		m.exitDeleteChoice()
		return m, nil
	case "i":
		return m.applyDeleteChoice(false)
	case keyF:
		return m.applyDeleteChoice(true)
	}
	return m, nil
}

// applyDeleteChoice performs the deletion on the index and dispatches a
// fresh search so the list re-renders without the removed row. alsoFile
// gates whether the on-disk transcript is removed alongside the entry:
//   - alsoFile=false uses SetHidden (entry preserved with HiddenFromIndex=true)
//     so Refresh does not resurrect the row from disk on the next rescan.
//   - alsoFile=true uses Delete which removes the entry and `os.Remove`s the
//     underlying JSONL file.
func (m Model) applyDeleteChoice(alsoFile bool) (tea.Model, tea.Cmd) {
	target := m.deleteTarget
	if target == nil || m.transcriptIndex == nil {
		m.exitDeleteChoice()
		return m, nil
	}
	key := transcript.IndexKey(target.Harness, target.ID)

	var (
		err     error
		summary string
		sev     toastSeverity
	)
	if alsoFile {
		err = m.transcriptIndex.Delete(key, true)
		if err == nil {
			summary = fmt.Sprintf("Deleted transcript file for %s", deleteTargetLabel(*target))
			sev = toastSuccess
		}
	} else {
		err = m.transcriptIndex.SetHidden(key, true)
		if err == nil {
			summary = fmt.Sprintf("Hid %s from index", deleteTargetLabel(*target))
			sev = toastSuccess
		}
	}

	m.exitDeleteChoice()

	// Clamp the cursor synchronously: the async sessionSearchDoneMsg handler
	// also clamps, but a stale offset between now and that message can scroll
	// past the new tail. Mirror the pattern in case sessionSearchDoneMsg.
	if m.sessionCursor >= len(m.sessionResults)-1 && m.sessionCursor > 0 {
		m.sessionCursor--
	}
	scrollSessionSearchToCursor(&m)

	// Drop any stale viewer state so a re-entry can't accidentally point at
	// the just-deleted row (S8 zeros viewer state in exitSessionView, but the
	// delete flow does not pass through that helper).
	m.viewerSession = nil
	m.viewerMessages = nil
	m.viewerMatchLines = nil

	var cmds []tea.Cmd
	if err != nil {
		if c := m.pushToast(Toast{
			Severity: toastError,
			Text:     "Delete failed: " + err.Error(),
		}); c != nil {
			cmds = append(cmds, c)
		}
	} else if summary != "" {
		if c := m.pushToast(Toast{
			Severity: sev,
			Text:     summary,
		}); c != nil {
			cmds = append(cmds, c)
		}
	}

	// Re-dispatch the search so the list refreshes without the deleted row.
	// Bump seq first so any in-flight debounce ticks with the prior seq are
	// invalidated by their stale-drop guards.
	m.sessionSearchSeq++
	m.sessionLoading = true
	m.sessionSearchErr = nil
	cmds = append(cmds, runSessionSearchCmd(m.sessionSearchSeq, m.transcriptIndex, m.transcriptReaders, m.currentSessionQuery()))
	return m, tea.Batch(cmds...)
}

// renderDeleteChoice draws the three-way prompt. The "also delete file"
// option is rendered in styleError because the on-disk JSONL removal is
// irreversible.
func renderDeleteChoice(m Model) string {
	var sb strings.Builder
	sb.WriteString("  ")
	if m.deleteTarget == nil {
		sb.WriteString(styleHeader.Render("Delete transcript?"))
		sb.WriteByte('\n')
		return sb.String()
	}
	t := m.deleteTarget
	sb.WriteString(styleHeader.Render(fmt.Sprintf("Delete %s?", deleteTargetLabel(*t))))
	sb.WriteByte('\n')
	sb.WriteString("  ")
	sb.WriteString(styleDim.Render(fmt.Sprintf("%s · %s · %s", t.Harness, t.Profile, t.Path)))
	sb.WriteByte('\n')
	sb.WriteByte('\n')
	sb.WriteString("  ")
	sb.WriteString("[i] index only — hide from search; file kept on disk")
	sb.WriteByte('\n')
	sb.WriteString("  ")
	sb.WriteString(styleError.Render("[f] also delete file — irreversible, removes the JSONL"))
	sb.WriteByte('\n')
	sb.WriteString("  ")
	sb.WriteString(styleDim.Render("[esc] cancel"))
	sb.WriteByte('\n')
	return sb.String()
}

// renderDeleteChoiceBox wraps renderDeleteChoice in styleOverlayBox so the
// prompt can be composited as a centered overlay on top of the session
// search backdrop. The inner width is capped at 70 columns (enough room for
// the longest `[f] also delete file …` line) and clamped to m.width-4 on
// narrow terminals.
func renderDeleteChoiceBox(m Model) string {
	innerWidth := 70
	if avail := m.width - 4; avail < innerWidth {
		innerWidth = avail
	}
	if innerWidth < 1 {
		innerWidth = 1
	}
	return styleOverlayBox.Width(innerWidth).Render(strings.TrimRight(renderDeleteChoice(m), "\n"))
}

// deleteTargetLabel produces a short, scannable description of the
// transcript for the header + toast text. Falls back to the path's basename
// when no project title was resolved.
func deleteTargetLabel(t transcript.SessionMeta) string {
	proj := t.Project
	if proj == "" {
		proj = t.ID
	}
	return fmt.Sprintf("%q", proj)
}
