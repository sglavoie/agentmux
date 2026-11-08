package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
	"github.com/sglavoie/agentmux/tui/launch"
)

const previewNoticeTTL = 2 * time.Second

// pushPreviewNotice explains why the preview could not open. It is a warning
// with its own short deadline, not an error: nothing failed, the terminal is
// just too small, and the notice must clear on its own.
func (m *Model) pushPreviewNotice(text string) tea.Cmd {
	return m.replaceToastByID(previewNoticeToastID, Toast{
		Severity: toastWarning,
		Text:     text,
		Expires:  modelNow(*m).Add(previewNoticeTTL),
	})
}

func (m *Model) clearPreviewNotice() {
	m.clearToastByID(previewNoticeToastID)
}

// retargetPreview points the pane at a new target and opens a new generation for
// it. Every path that changes what the preview is showing goes through here, so
// no path can move the target without also invalidating the read that is still
// in flight for the previous one. The periodic tick is not such a path: it
// re-reads the current target under the current generation.
//
// It moves the target only. The read in flight keeps its request identity and
// its slot, because that read is still going to answer and the controller still
// has to hear it; what the retarget takes away is its permission to paint. The
// reset of the operator's scroll hold hangs off the target change for the same
// reason it is written here rather than in the controller: a coalesced
// re-request of the SAME target must not drop a hold, and only a genuine move to
// another buffer may.
func (m *Model) retargetPreview(windowID int) {
	m.previewWindowID = windowID
	m.previewTargetSeq++
	m.previewText = ""
	m.previewError = ""
	m.previewLoaded = false
	// A new target is a new buffer, so the hold is dropped with it. Keeping a
	// scroll position across a retarget would show one session's snapshot under
	// another session's metadata for as long as the read takes to land.
	m.inspectorViewport = m.inspectorViewport.Resume()
}

// reshowPreview puts a pane that is on screen again back into service, without
// deciding on its own that the operator's place in the output has gone stale.
//
// It is the counterpart of showInspector for a pane nobody asked to move. The `i`
// key is a statement about what the operator wants to look at, so it retargets
// unconditionally; a pane that reappeared because the terminal changed shape is
// not, and treating a geometry event as a new selection would resume a paused
// inspector and throw away the snapshot the operator was reading. So the retarget
// is conditional on the target having actually MOVED: an unchanged dashboard
// target keeps its held snapshot, its scroll position, and the text already on
// it, while a selection that changed while the pane was gone still goes through
// retargetPreview and resets to the tail.
//
// The read and the poll restart either way. They are not the hold — the chain
// died the first time a tick found the pane hidden, and a held viewport ignores
// every snapshot that lands under it, so reviving the chain costs the hold
// nothing and is what makes the pane live again the moment the operator resumes.
func (m *Model) reshowPreview() tea.Cmd {
	if winID := m.currentPreviewWindowID(); winID != m.previewWindowID {
		m.retargetPreview(winID)
	}
	return tea.Batch(m.requestPreviewRead(), m.startPreviewPoll())
}

// dashboardPane is everything the dashboard's inspector is SHOWING: the target
// it is pointed at, the snapshot on it, what a read has said about that
// snapshot, and where in it the operator is looking.
//
// It is a type rather than five assignments because the launcher borrows the
// pane rather than owning one. The two surfaces share a target, a read
// controller, and a polling chain deliberately — one read at a time across the
// whole program — but they do not share content: the launcher's preview answers
// "what is on the row under the cursor", which is a different question from the
// one the inspector is open on. Setting this aside for the length of the visit
// is what lets the borrow be given back.
type dashboardPane struct {
	windowID int
	text     string
	err      string
	loaded   bool
	viewport dashboard.OutputViewport
}

// lendPreviewToLauncher points the shared pane at one of the launcher's rows,
// setting the dashboard inspector's pane aside the first time this visit moves
// it. Every launcher retarget goes through here rather than calling
// retargetPreview directly, so "the launcher is currently borrowing the pane" is
// one fact the model carries rather than an invariant spread over four call
// sites.
//
// The capture is taken once, on the first move, and that is the whole point. A
// launcher that walks ten rows and toggles its preview twice has moved the pane
// a dozen times and borrowed it once; a capture per move would overwrite the
// dashboard's pane with the launcher's own and give back what it took.
func (m *Model) lendPreviewToLauncher(windowID int) {
	if m.launcherBorrowedPane == nil {
		m.launcherBorrowedPane = &dashboardPane{
			windowID: m.previewWindowID,
			text:     m.previewText,
			err:      m.previewError,
			loaded:   m.previewLoaded,
			viewport: m.inspectorViewport,
		}
	}
	m.retargetPreview(windowID)
}

// returnPreviewToDashboard is the way out of the launcher: it puts back whatever
// the launcher borrowed and hands the pane to the dashboard.
//
// Every key that takes the launcher off the screen calls it — esc, enter, and
// the `s` that opens Past Sessions for the row under the cursor. That last one
// does not land on the dashboard, and it is still an exit: the borrow is scoped
// to the launcher being the surface on screen, not to the dashboard being the
// next one. A route that skipped the restore because it was going somewhere else
// would leave the stash outstanding while the operator reaches the dashboard by
// another door, reads it, and resumes it — and the next launcher visit would
// hand that pre-visit snapshot back over everything they did.
//
// The restore comes first and is unconditional, because the borrow is undone
// whether or not the dashboard is going to draw the pane — the inspector's
// state outlives being off screen, and a hidden inspector that later reopens
// must not find the launcher's snapshot in it.
//
// What happens after the restore is reshowPreview's question, not this one's,
// and delegating it is what keeps the two ways back into the pane telling the
// same story. A selection that did not move keeps its held snapshot and its
// place in it; one that moved while the launcher was on screen — a topology
// refresh can reorder the queue under it — goes through retargetPreview and
// resets to the tail, because that is a different buffer. Either way the read
// and the poll restart: the chain died when the launcher took the pane, and a
// pane nothing is feeding would strand an operator who presses End.
func (m *Model) returnPreviewToDashboard() tea.Cmd {
	if borrowed := m.launcherBorrowedPane; borrowed != nil {
		m.launcherBorrowedPane = nil
		m.previewWindowID = borrowed.windowID
		m.previewText = borrowed.text
		m.previewError = borrowed.err
		m.previewLoaded = borrowed.loaded
		m.inspectorViewport = borrowed.viewport
	}
	if !m.inspectorActive() {
		return nil
	}
	return m.reshowPreview()
}

// launcherOwnsPreview reports that the shared pane currently belongs to the
// launcher rather than to the dashboard — the launcher itself and the overlays
// raised over it, which is exactly what activeRootMode answers.
//
// It gates the one path that writes the launcher's pane state without a
// launcher keypress behind it. A resize reaches disablePickerPreviewWithNotice
// on the strength of pickerPreviewEnabled alone, and that flag is the launcher's
// REMEMBERED visibility rather than a statement about what is on screen: it
// survives the visit that set it. Unasked, a shrink of the dashboard would let a
// preview that is three views away and invisible retarget the pane the operator
// is reading.
func launcherOwnsPreview(m Model) bool { return m.activeRootMode() == ModePicker }

// activePreviewEnabled reports whether the preview pane is enabled for the
// currently active mode. ModePicker has its own independent flag so that
// toggling preview inside the picker does not disturb the list's preview state.
// It asks through launcherOwnsPreview so a detour raised over the launcher —
// help, the profile-override picker — keeps the launcher's preview.
func (m Model) activePreviewEnabled() bool {
	if launcherOwnsPreview(m) {
		return m.pickerPreviewEnabled
	}
	return m.inspectorActive()
}

// currentPreviewWindowID returns the window ID the preview should track for
// the current cursor in the active mode. ModePicker resolves through the
// picker cursor (matching the highlighted config session to a running one);
// every other mode uses the dashboard cursor.
func (m Model) currentPreviewWindowID() int {
	if launcherOwnsPreview(m) {
		return m.pickerCurrentWindowID()
	}
	if s, ok := m.selectedSession(); ok {
		return s.WindowID
	}
	return 0
}

// pickerCurrentWindowID returns the WindowID of the running session matching
// the picker's highlighted config row, or 0 when nothing matches. When multiple
// instances share a key, use the last match in the current session ordering,
// which tracks the newest same-key tab after sorted insertion.
func (m Model) pickerCurrentWindowID() int {
	if len(m.pickerFilteredIdx) == 0 || m.pickerCursor >= len(m.pickerFilteredIdx) {
		return 0
	}
	origIdx := m.pickerFilteredIdx[m.pickerCursor]
	sess := m.pickerSessionWithResolvedProfile(origIdx)
	key := session.SessionKey(sess.EffectiveProfile(), sess.Title)
	winID := 0
	for _, rs := range m.sessions {
		if launch.RunningSessionKey(rs) == key {
			winID = rs.WindowID
		}
	}
	return winID
}

// refreshPreviewForPickerCursor returns an immediate fetch cmd when the
// picker cursor now points at a different running session (or none).
// Returns nil when preview is off or the window hasn't changed.
func (m *Model) refreshPreviewForPickerCursor() tea.Cmd {
	if !m.pickerPreviewEnabled {
		return nil
	}
	winID := m.pickerCurrentWindowID()
	if winID == m.previewWindowID {
		return nil
	}
	m.lendPreviewToLauncher(winID)
	return m.requestPreviewRead()
}

// refreshPreviewForCursor returns an immediate fetch cmd when the cursor
// has moved to a session backed by a different window. Returns nil when
// preview is off or the selection still points at the same window.
func (m *Model) refreshPreviewForCursor() tea.Cmd {
	if !m.inspectorActive() {
		return nil
	}
	// A selection that is not a session tracks window 0, which fetchPreviewCmd
	// short-circuits into the "(session not running)" placeholder rather than a
	// terminal capture.
	var winID int
	if s, ok := m.selectedSession(); ok {
		winID = s.WindowID
	}
	if winID == m.previewWindowID {
		return nil
	}
	m.retargetPreview(winID)
	return m.requestPreviewRead()
}

// togglePickerPreview flips the picker's own preview pane on/off, independent
// of m.previewEnabled (the list's preview flag).
//
// Both directions borrow rather than retarget outright. Turning the pane OFF
// looks like it is giving something back, but it is not: it points the shared
// target at nothing, and the dashboard inspector behind the launcher owns that
// target too. The launcher's `p` is a statement about the launcher's pane and
// may not decide anything about the inspector the operator will return to.
func (m Model) togglePickerPreview() (tea.Model, tea.Cmd) {
	if m.pickerPreviewEnabled {
		m.pickerPreviewEnabled = false
		m.lendPreviewToLauncher(0)
		scrollPickerToCursor(&m)
		return m, nil
	}
	cfg := m.cfg.Defaults.Preview
	if !previewFits(m.width, m.height, footerBudgetLines(m), cfg.MinCols, cfg.MinRows, cfg.Direction) {
		return m, m.pushPreviewNotice("preview unavailable: terminal too small")
	}
	m.pickerPreviewEnabled = true
	m.lendPreviewToLauncher(m.pickerCurrentWindowID())
	m.clearPreviewNotice()
	scrollPickerToCursor(&m)
	// The launcher's pane shares the dashboard's controller, target, and chain —
	// only the visibility flag is its own — so enabling it is the same two steps
	// and inherits the same one-read-at-a-time bound.
	read := m.requestPreviewRead()
	return m, tea.Batch(read, m.startPreviewPoll())
}

// The dashboard has no counterpart to disablePickerPreviewWithNotice on
// purpose. A resize that stops clearing the inspector's floors does not disable
// anything: the intent stays where the operator left it and the split simply
// stops being drawn until the terminal can hold one again. A notice would be
// announcing a decision nothing made.

// disablePickerPreviewWithNotice is the resize's version of turning the
// launcher's pane off, and it borrows for the same reason the key does. Its
// caller establishes that the launcher owns the pane — see launcherOwnsPreview —
// because the flag it fires on is remembered across visits and would otherwise
// let a dashboard resize reach in here.
func (m *Model) disablePickerPreviewWithNotice(msg string) tea.Cmd {
	m.pickerPreviewEnabled = false
	m.lendPreviewToLauncher(0)
	return m.pushPreviewNotice(msg)
}
