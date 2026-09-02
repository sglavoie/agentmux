package tui

// This file is the seam between the root Bubble Tea model and the
// tui/worktrees leaf. The leaf owns the managed-worktree display domain — the
// header, the filter bar, the inline notices, and the scrollable row list —
// and reads its facts from an explicit input value. The Model stays here, and
// the adapters below turn "what the model holds" into "what the leaf needs",
// then re-expose the leaf's answer under the name the rest of the root package
// already uses. Loading the list, moving the cursor through it, and running
// the lifecycle actions a row can trigger stay in the root.

import tuiworktrees "github.com/sglavoie/agentmux/tui/worktrees"

// worktreeListInput projects the model onto the Worktrees view's input value.
func worktreeListInput(m Model) tuiworktrees.ListInput {
	return tuiworktrees.ListInput{
		Width:           m.width,
		Height:          m.height,
		BodyHeight:      rootBodyHeight(m),
		PathRow:         renderPathRow("worktree path", selectedWorktreePath(m), m.width),
		Entries:         m.worktreeEntries,
		Filtered:        m.worktreeFiltered,
		Cursor:          m.worktreeCursor,
		Offset:          m.worktreeOffset,
		Filter:          m.worktreeFilter,
		FilterMode:      m.worktreeFilterMode,
		Loading:         m.worktreeLoading,
		ActionInFlight:  m.worktreeActionInFlight,
		RefreshErr:      m.worktreeErr,
		RefreshWarnings: m.worktreeWarnings,
	}
}

// worktreeVisibleHeight returns the number of worktree rows that fit in the
// root body area.
func worktreeVisibleHeight(m Model) int {
	return worktreeListInput(m).VisibleHeight()
}

// renderWorktrees renders the full ModeWorktrees layout.
func renderWorktrees(m Model) string {
	return worktreeListInput(m).Render()
}
