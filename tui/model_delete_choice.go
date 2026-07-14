package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/transcript"
)

// transcriptDeleteDoneMsg returns the result of a delete-prompt mutation to
// the Bubble Tea update loop. The potentially blocking advisory-lock wait
// happens inside the tea.Cmd that produces this message, never in Update.
type transcriptDeleteDoneMsg struct {
	target   transcript.SessionMeta
	alsoFile bool
	err      error
}

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

// applyDeleteChoice dispatches the deletion as a tea.Cmd so waiting for the
// cache advisory lock cannot block input or rendering. alsoFile
// gates whether the on-disk transcript is removed alongside the entry:
//   - alsoFile=false uses SetHidden (entry preserved with HiddenFromIndex=true)
//     so Refresh does not resurrect the row from disk on the next rescan.
//   - alsoFile=true uses Delete which removes the entry and `os.Remove`s the
//     underlying JSONL file.
func (m Model) applyDeleteChoice(alsoFile bool) (tea.Model, tea.Cmd) {
	if m.deleteTarget == nil || m.transcriptIndex == nil {
		m.exitDeleteChoice()
		return m, nil
	}
	target := *m.deleteTarget
	key := transcript.IndexKey(target.Harness, target.ID)
	idx := m.transcriptIndex

	m.exitDeleteChoice()
	m.viewerSession = nil
	m.viewerMessages = nil
	m.viewerMatchLines = nil

	// Invalidate searches and warm-refresh results dispatched before the
	// mutation. Completion bumps the sequence again so searches performed
	// while the mutation was waiting on the lock cannot restore stale rows.
	m.sessionSearchSeq++
	m.sessionLoading = true
	m.sessionSearchErr = nil

	return m, func() tea.Msg {
		var err error
		if alsoFile {
			err = idx.Delete(key, true)
		} else {
			err = idx.SetHidden(key, true)
		}
		return transcriptDeleteDoneMsg{target: target, alsoFile: alsoFile, err: err}
	}
}

// handleTranscriptDeleteDone publishes the mutation outcome and launches a
// fresh search against the current query. A SourceDeletedError is a partial
// success: the irreversible file deletion happened even though the cache
// metadata could not be updated.
func (m Model) handleTranscriptDeleteDone(msg transcriptDeleteDoneMsg) (tea.Model, tea.Cmd) {
	var toast Toast
	if msg.err == nil {
		if msg.alsoFile {
			toast = Toast{Severity: toastSuccess, Text: fmt.Sprintf("Deleted transcript file for %s", deleteTargetLabel(msg.target))}
		} else {
			toast = Toast{Severity: toastSuccess, Text: fmt.Sprintf("Hid %s from index", deleteTargetLabel(msg.target))}
		}
	} else {
		var partial *transcript.SourceDeletedError
		if errors.As(msg.err, &partial) {
			toast = Toast{
				Severity: toastWarning,
				Text:     fmt.Sprintf("Deleted transcript file for %s, but index update failed: %v", deleteTargetLabel(msg.target), partial.Err),
			}
		} else {
			toast = Toast{Severity: toastError, Text: "Delete failed: " + msg.err.Error()}
		}
	}

	// Clamp the cursor before the async sessionSearchDoneMsg handler runs: it
	// also clamps, but a stale offset between now and that message can scroll
	// past the new tail. Mirror the pattern in case sessionSearchDoneMsg.
	if m.sessionCursor >= len(m.sessionResults)-1 && m.sessionCursor > 0 {
		m.sessionCursor--
	}
	scrollSessionSearchToCursor(&m)

	var cmds []tea.Cmd
	if c := m.pushToast(toast); c != nil {
		cmds = append(cmds, c)
	}

	// Re-dispatch the search so the list refreshes without the deleted row.
	// Bump seq again so work started while the mutation was blocked cannot win.
	m.sessionSearchSeq++
	m.sessionLoading = true
	m.sessionSearchErr = nil
	if m.transcriptIndex != nil {
		cmds = append(cmds, m.runSessionSearchCmd(m.sessionSearchSeq, m.transcriptIndex, m.transcriptReaders, m.currentSessionQuery()))
	} else {
		m.sessionLoading = false
	}
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
	sb.WriteString(styleDim.Render(deleteTargetDetail(*t)))
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
	return fmt.Sprintf("%q", displayRenderTitleIcon(proj))
}

func deleteTargetDetail(t transcript.SessionMeta) string {
	parts := make([]string, 0, 3)
	if t.Harness != "" {
		parts = append(parts, t.Harness)
	}
	if profile := sessionProfileLabel(t); profile != "" {
		parts = append(parts, profile)
	}
	if t.Path != "" {
		parts = append(parts, t.Path)
	}
	return strings.Join(parts, " · ")
}
