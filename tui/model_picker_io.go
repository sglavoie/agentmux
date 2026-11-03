// This file is the launcher's filesystem work, moved off Update: checking that
// each project's directory is usable, completing a typed directory, and saving
// pins and one-time profile choices. Each runs as a tea.Cmd and reports back
// with a message the model applies only while it still describes the launcher
// the work was started for, so a slow disk never stalls a keypress and a late
// answer never lands on a list that has since changed.

package tui

import (
	"fmt"
	"log/slog"
	"maps"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/tui/launch"
)

// pickerDirProblemsMsg carries the directory checks for one generation of the
// launcher's project list. dirs is what each index was checked against, so a
// directory the operator overrode meanwhile keeps its own, newer answer.
type pickerDirProblemsMsg struct {
	gen      int
	dirs     []string
	problems map[int]string
}

// pickerDirCompletedMsg is a tab completion of the directory prompt.
type pickerDirCompletedMsg struct {
	gen       int
	idx       int
	typed     string
	completed string
}

// pickerStateSavedMsg reports a write of the launcher's saved state. pin is
// set when the write was a pin toggle, which reports its outcome.
type pickerStateSavedMsg struct {
	err error
	pin *pickerPinChange
}

type pickerPinChange struct {
	origIdx int
	title   string
	pinned  bool
}

// pickerStateWriter serializes saves and keeps ordered changes until they are
// written. Commands can run out of order; the later one carries earlier edits.
type pickerStateWriter struct {
	mu      sync.Mutex
	written uint64
	pending []pickerPendingChange
}

type pickerPendingChange struct {
	seq    uint64
	change launch.StateChange
}

// checkPickerDirectoriesCmd starts a new generation of the launcher's project
// list and checks every project's effective directory off the update loop.
func (m *Model) checkPickerDirectoriesCmd() tea.Cmd {
	m.pickerGen++
	gen := m.pickerGen
	in := m.pickerInput()
	dirs := make([]string, len(m.pickerSessions))
	for i := range dirs {
		dirs[i] = in.EffectiveDirectory(i)
	}
	problem := actions.ProjectDirectoryProblem
	return func() tea.Msg {
		problems := make(map[int]string)
		for i, dir := range dirs {
			if p := problem(dir); p != "" {
				problems[i] = p
			}
		}
		return pickerDirProblemsMsg{gen: gen, dirs: dirs, problems: problems}
	}
}

func (m Model) handlePickerDirProblems(msg pickerDirProblemsMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.pickerGen {
		return m, nil
	}
	in := m.pickerInput()
	next := make(map[int]string, len(msg.problems))
	for i, dir := range msg.dirs {
		if i >= len(m.pickerSessions) {
			break
		}
		if in.EffectiveDirectory(i) != dir {
			// Overridden since the check started; refreshPickerDirProblem
			// already answered for the new directory.
			if p := m.pickerDirProblems[i]; p != "" {
				next[i] = p
			}
			continue
		}
		if p := msg.problems[i]; p != "" {
			next[i] = p
		}
	}
	m.pickerDirProblems = next
	return m, nil
}

// completePickerDirCmd completes the directory prompt's value. Completion reads
// the directory being typed into, which is filesystem work.
func (m Model) completePickerDirCmd() tea.Cmd {
	idx := m.pickerDirIdx
	if idx < 0 || idx >= len(m.pickerSessions) {
		return nil
	}
	gen, typed, configured := m.pickerGen, m.pickerDirValue, m.pickerSessions[idx].Directory
	return func() tea.Msg {
		return pickerDirCompletedMsg{gen: gen, idx: idx, typed: typed, completed: launch.CompleteDirectory(configured, typed)}
	}
}

// handlePickerDirCompleted applies a completion only to the prompt it was asked
// for: same list, same project, and nothing typed since.
func (m Model) handlePickerDirCompleted(msg pickerDirCompletedMsg) (tea.Model, tea.Cmd) {
	if m.mode != ModePicker || !m.pickerDirMode || msg.gen != m.pickerGen ||
		msg.idx != m.pickerDirIdx || msg.typed != m.pickerDirValue {
		return m, nil
	}
	m.pickerDirValue = msg.completed
	m.pickerDirError = ""
	return m, nil
}

// savePickerStateCmd writes a change off the update loop while retaining all
// earlier changes from this dashboard that may still be waiting to run.
func (m *Model) savePickerStateCmd(change launch.StateChange, pin *pickerPinChange) tea.Cmd {
	if m.pickerStateWriter == nil {
		m.pickerStateWriter = &pickerStateWriter{}
	}
	m.pickerStateSeq++
	seq, writer, in := m.pickerStateSeq, m.pickerStateWriter, m.pickerInput()
	writer.mu.Lock()
	writer.pending = append(writer.pending, pickerPendingChange{seq: seq, change: change})
	writer.mu.Unlock()
	return func() tea.Msg {
		writer.mu.Lock()
		defer writer.mu.Unlock()
		if seq <= writer.written {
			// A newer save already included this change.
			return pickerStateSavedMsg{pin: pin}
		}
		changes := make([]launch.StateChange, 0, len(writer.pending))
		for _, pending := range writer.pending {
			if pending.seq > writer.written && pending.seq <= seq {
				changes = append(changes, pending.change)
			}
		}
		err := in.SaveChanges(changes)
		if err == nil {
			writer.written = seq
			remaining := writer.pending[:0]
			for _, pending := range writer.pending {
				if pending.seq > seq {
					remaining = append(remaining, pending)
				}
			}
			writer.pending = remaining
		}
		return pickerStateSavedMsg{err: err, pin: pin}
	}
}

// handlePickerStateSaved reports a save. A profile choice that failed to save is
// still applied to this launch, as it always was, and only logged; a pin that
// failed is taken back, since the pin is the saved state.
func (m Model) handlePickerStateSaved(msg pickerStateSavedMsg) (tea.Model, tea.Cmd) {
	if msg.pin == nil {
		if msg.err != nil {
			slog.Debug("overrides: save error", "err", msg.err)
		}
		return m, nil
	}
	change := *msg.pin
	if msg.err == nil {
		action := "Unpinned"
		if change.pinned {
			action = "Pinned"
		}
		return m, m.pushToast(Toast{Severity: toastSuccess, Text: fmt.Sprintf("%s %s", action, change.title)})
	}
	if m.mode == ModePicker && m.pickerPinned[change.origIdx] == change.pinned {
		next := maps.Clone(m.pickerPinned)
		if change.pinned {
			delete(next, change.origIdx)
		} else {
			if next == nil {
				next = make(map[int]bool)
			}
			next[change.origIdx] = true
		}
		m.applyPickerPins(next, change.origIdx)
	}
	cmds := []tea.Cmd{m.pushToast(Toast{
		Severity: toastError,
		Text:     fmt.Sprintf("Could not update pin for %s: %s", change.title, msg.err),
	})}
	if m.mode == ModePicker {
		cmds = append(cmds, m.refreshPreviewForPickerCursor())
	}
	return m, tea.Batch(cmds...)
}
