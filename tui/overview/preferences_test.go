package overview

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/tui/commands"
)

func TestCompactToggleKeepsSelectionAndSuspendsCaptures(t *testing.T) {
	m, r := atSize(t, viewSessions(30), 17, 160, 45)
	outstanding := inFlightRequests(r, m)
	m = only(t, m, key(t, "c"))
	if !m.compact || m.selected != 17 || len(m.visibleTiles()) != 0 || len(m.previews) != 0 {
		t.Fatalf("compact toggle lost selection or retained previews: selected=%d compact=%v", m.selected, m.compact)
	}
	if got := MeasureMode(160, 45, true).Capacity(); got != 30 {
		t.Fatalf("compact capacity=%d, want 30 (normal 9)", got)
	}
	frame := frameOf(m)
	for _, s := range m.sessions {
		if !strings.Contains(frame, s.Project) {
			t.Fatalf("compact frame missing %s", s.Project)
		}
	}
	for _, req := range outstanding {
		m = reply(t, m, req, []string{"hidden output"}, nil)
	}
	before := len(r.captures)
	m = only(t, m, commands.CaptureTickMsg{})
	if len(r.captures) != before || strings.Contains(frameOf(m), "hidden output") {
		t.Fatal("compact mode captured or displayed terminal output")
	}
	m = only(t, m, key(t, "pgdown"))
	if m.selected != 29 {
		t.Fatalf("navigation used normal capacity: selected=%d", m.selected)
	}
	m = only(t, m, key(t, "c"))
	if m.compact || m.selected != 29 || len(r.captures) <= before {
		t.Fatal("normal mode did not preserve selection and resume previews")
	}
}

func TestPreferenceWritesCoalesceAndCloseWaitsForLatest(t *testing.T) {
	m, _ := newTestModel(0)
	var writes []bool
	m.deps.preferences = func(value *bool) tea.Cmd {
		writes = append(writes, *value)
		return nil // deliver acknowledgements explicitly
	}
	m = only(t, m, key(t, "c"))
	m = only(t, m, commands.OverviewPreferencesMsg{Read: true, Explicit: true, Compact: false})
	if !m.compact {
		t.Fatal("late startup load undid the toggle")
	}
	m = only(t, m, key(t, "c"))
	if len(writes) != 1 || !writes[0] {
		t.Fatalf("writes were not serialized: %v", writes)
	}
	m, cmd := send(t, m, key(t, "q"))
	if cmd != nil || !m.quitAfterPreference {
		t.Fatal("dismissal raced the preference save")
	}
	m = only(t, m, commands.OverviewPreferencesMsg{Compact: true})
	if len(writes) != 2 || writes[1] {
		t.Fatalf("latest choice was not saved: %v", writes)
	}
	m, cmd = send(t, m, commands.OverviewPreferencesMsg{Compact: false})
	if cmd == nil {
		t.Fatal("save completion did not dismiss")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("save completion did not return Quit")
	}
}

func TestPreferencesRestoreOnOpenAndReportFailure(t *testing.T) {
	m, _ := newTestModel(0)
	m = only(t, m, commands.OverviewPreferencesMsg{Read: true, Explicit: true, Compact: true})
	if !m.compact {
		t.Fatal("reopened overview did not restore compact mode")
	}
	m.preferenceSaving = true
	m.preferenceSavingValue = true
	m = only(t, m, commands.OverviewPreferencesMsg{Err: errors.New("offline")})
	if !m.compact || !strings.Contains(m.feedback(), "offline") || m.preferenceSaving {
		t.Fatal("failed save must keep local mode, report the error, and settle")
	}
}

func TestCompactFramesHaveTwoLineHeadersAtEverySize(t *testing.T) {
	for _, size := range [][2]int{{1, 1}, {4, 7}, {20, 12}, {80, 24}, {120, 30}, {160, 45}} {
		in := viewInput(size[0], size[1], viewSessions(35))
		in.Compact = true
		renderLines(t, in) // exact rows and terminal cell widths, including narrow clipping
	}
	in := viewInput(160, 45, viewSessions(1))
	in.Compact = true
	lines := plain(renderLines(t, in))
	if !strings.Contains(lines[3], "proj01") || !strings.Contains(lines[3], "Working") || !strings.Contains(lines[3], "0:42") {
		t.Fatalf("first content row must combine identity and status: %q", lines[3])
	}
	if !strings.Contains(lines[4], "claude") || !strings.Contains(lines[4], "work") {
		t.Fatalf("second content row must show harness/profile: %q", lines[4])
	}

	// A viewport with a three-row remainder must leave it below the cards,
	// not stretch alternating rows into taller boxes.
	in = viewInput(120, 22, viewSessions(8))
	in.Compact = true
	lines = plain(renderLines(t, in))
	for row := 0; row < 4; row++ {
		y := gridTop + row*CompactTileMinHeight
		top, bottom := "╭", "╰"
		if row == 0 {
			top, bottom = "┏", "┗"
		}
		if !strings.HasPrefix(lines[y], top) || !strings.HasPrefix(lines[y+3], bottom) {
			t.Errorf("card row %d does not have exactly two content lines: %q .. %q", row, lines[y], lines[y+3])
		}
	}
	for y := 18; y < 21; y++ {
		if strings.TrimSpace(lines[y]) != "" {
			t.Errorf("spare row %d contains card content: %q", y, lines[y])
		}
	}
}
