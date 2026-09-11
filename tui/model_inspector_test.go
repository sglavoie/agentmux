package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/dashboard"
)

// inspectorPane renders the preview pane the way the split frame does and
// returns it as plain text.
func inspectorPane(m Model) string {
	return stripAnsi(renderPreviewPane(m, 60, 16))
}

// TestInspectorTellsAPendingReadFromAnEmptyBuffer is the root half of the
// distinction the leaf is built around. Both states hold an empty previewText,
// so the pane can only tell them apart if the model records that a read has come
// back — which is what previewLoaded is for.
func TestInspectorTellsAPendingReadFromAnEmptyBuffer(t *testing.T) {
	m, _ := listViewModel(dashboard.ViewUrgency)
	m.previewEnabled = true
	m = selectLine(t, m, "session:api")
	m.retargetPreview(m.currentPreviewWindowID())

	if got := inspectorPane(m); !strings.Contains(got, "(loading…)") {
		t.Errorf("a freshly retargeted pane did not report the read as outstanding:\n%s", got)
	}

	updated, _ := m.handlePreviewContent(previewContentMsg{
		windowID: m.previewWindowID, targetSeq: m.previewTargetSeq, text: "",
	})
	m = updated.(Model)
	if !m.previewLoaded {
		t.Fatalf("a landed read did not mark the target loaded")
	}
	got := inspectorPane(m)
	if !strings.Contains(got, "(no output)") {
		t.Errorf("a session that has printed nothing still read as loading:\n%s", got)
	}
	if strings.Contains(got, "(loading…)") {
		t.Errorf("the pane reported both states at once:\n%s", got)
	}

	// A failure has answered the question too, and says which one it was.
	updated, _ = m.handlePreviewContent(previewContentMsg{
		windowID: m.previewWindowID, targetSeq: m.previewTargetSeq, err: errors.New("no such window"),
	})
	m = updated.(Model)
	if got := inspectorPane(m); !strings.Contains(got, "error: no such window") {
		t.Errorf("a failed read did not name its failure:\n%s", got)
	}
}

// TestRetargetResetsTheInspectorsReadState pins what a moved cursor clears. A
// scroll position or a loaded flag surviving a retarget would show one session's
// snapshot under another session's metadata.
func TestRetargetResetsTheInspectorsReadState(t *testing.T) {
	m, _ := listViewModel(dashboard.ViewUrgency)
	m.previewEnabled = true
	m = selectLine(t, m, "session:api")
	m.retargetPreview(m.currentPreviewWindowID())

	updated, _ := m.handlePreviewContent(previewContentMsg{
		windowID: m.previewWindowID, targetSeq: m.previewTargetSeq,
		text: strings.Repeat("line\n", 40),
	})
	m = updated.(Model)
	m.inspectorViewport = m.inspectorViewport.ScrollUp(dashboard.SplitOutput(m.previewText), 4, 2)
	if !m.inspectorViewport.Paused() {
		t.Fatalf("scrolling back did not pause the pane")
	}

	m = selectLine(t, m, "session:web")
	m.retargetPreview(m.currentPreviewWindowID())
	if m.inspectorViewport.Paused() {
		t.Errorf("a new target kept the previous session's hold")
	}
	if m.previewLoaded {
		t.Errorf("a new target kept the previous session's loaded flag")
	}
	if got := inspectorPane(m); !strings.Contains(got, "(loading…)") {
		t.Errorf("a new target did not report its own read as outstanding:\n%s", got)
	}
}

// TestInspectorPaneNamesTheSelectedSession checks that the root actually hands
// the leaf the facts the queue row stopped carrying.
func TestInspectorPaneNamesTheSelectedSession(t *testing.T) {
	m, _ := listViewModel(dashboard.ViewUrgency)
	m.previewEnabled = true
	m = selectLine(t, m, "session:api")
	m.retargetPreview(m.currentPreviewWindowID())

	got := inspectorPane(m)
	for _, want := range []string{"api", "claude", "main", "/repo/api"} {
		if !strings.Contains(got, want) {
			t.Errorf("the inspector never said %q:\n%s", want, got)
		}
	}
	if got, want := len(strings.Split(got, "\n")), 16; got != want {
		t.Errorf("the pane rendered %d lines, want exactly the %d it was given", got, want)
	}
}

// TestInspectorPaneOverAVanishedWindow separates "not running" from "nothing to
// read": the metadata is still the answer to who this session was.
func TestInspectorPaneOverAVanishedWindow(t *testing.T) {
	m, _ := listViewModel(dashboard.ViewUrgency)
	m.previewEnabled = true
	m = selectLine(t, m, "session:api")
	for i := range m.sessions {
		m.sessions[i].WindowID = 0
	}
	m = loadSessions(m, m.sessions)
	m = selectLine(t, m, "session:api")
	m.retargetPreview(m.currentPreviewWindowID())

	got := inspectorPane(m)
	if !strings.Contains(got, "(session not running)") {
		t.Errorf("a session with no live window did not say so:\n%s", got)
	}
	if !strings.Contains(got, "api") {
		t.Errorf("a session with no live window lost its metadata:\n%s", got)
	}
}
