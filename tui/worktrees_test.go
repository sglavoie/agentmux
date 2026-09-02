package tui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/layout"
	tuiworktrees "github.com/sglavoie/agentmux/tui/worktrees"
)

func worktreeTestModel() Model {
	m := testModelWithSessions()
	m.width, m.height = 120, 30
	m.backgroundCommands.worktrees = func(_ Model, seq int) tea.Cmd {
		entries := worktreesFixture().Worktrees
		return func() tea.Msg { return worktreesLoadedMsg{seq: seq, entries: entries} }
	}
	return m
}

func enterLoadedWorktrees(t *testing.T, m Model) Model {
	t.Helper()
	updated, cmd := m.enterWorktrees()
	m = updated.(Model)
	if cmd == nil || !m.worktreeLoading {
		t.Fatal("enterWorktrees did not start an async load")
	}
	updated, _ = m.Update(cmd())
	return updated.(Model)
}

func TestWorktreesEntryRefreshStaleSuppressionAndCachedRows(t *testing.T) {
	m := worktreeTestModel()
	updated, first := m.enterWorktrees()
	m = updated.(Model)
	if m.mode != ModeWorktrees || !m.worktreeLoading || first == nil {
		t.Fatalf("entry = mode %v loading=%v cmd=%T", m.mode, m.worktreeLoading, first)
	}

	updated, _ = m.Update(first())
	m = updated.(Model)
	if m.worktreeLoading || len(m.worktreeEntries) != 6 {
		t.Fatalf("loaded = loading=%v entries=%d", m.worktreeLoading, len(m.worktreeEntries))
	}
	m.worktreeCursor = 2
	m.captureWorktreeSelection()
	selected := m.worktreeSelectedID

	updated, second := m.startWorktreeRefresh()
	m = updated.(Model)
	if !m.worktreeLoading || len(m.worktreeEntries) != 6 {
		t.Fatalf("refresh blanked cache: loading=%v entries=%d", m.worktreeLoading, len(m.worktreeEntries))
	}
	updated, third := m.startWorktreeRefresh()
	m = updated.(Model)
	if second == nil || third == nil || m.worktreeRefreshSeq != 3 {
		t.Fatalf("refresh sequence = %d cmds %T/%T", m.worktreeRefreshSeq, second, third)
	}

	stale := second().(worktreesLoadedMsg)
	stale.entries = nil
	updated, _ = m.Update(stale)
	m = updated.(Model)
	if len(m.worktreeEntries) != 6 || !m.worktreeLoading {
		t.Fatalf("stale response changed cache: loading=%v entries=%d", m.worktreeLoading, len(m.worktreeEntries))
	}

	updated, _ = m.Update(third())
	m = updated.(Model)
	if m.worktreeLoading || m.worktreeSelectedID != selected || m.worktreeFiltered[m.worktreeCursor].ID != selected {
		t.Fatalf("fresh response lost selection %q: cursor=%d selected=%q", selected, m.worktreeCursor, m.worktreeSelectedID)
	}
}

func TestWorktreesDropsResponsesAfterExitAndRetainsCacheOnError(t *testing.T) {
	m := enterLoadedWorktrees(t, worktreeTestModel())
	updated, cmd := m.startWorktreeRefresh()
	m = updated.(Model)
	m.mode = ModeList
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.mode != ModeList || !m.worktreeLoading {
		t.Fatalf("response after exit was not ignored: mode=%v loading=%v", m.mode, m.worktreeLoading)
	}

	m.mode = ModeWorktrees
	m.worktreeRefreshSeq++
	updated, _ = m.Update(worktreesLoadedMsg{seq: m.worktreeRefreshSeq, err: errors.New("git unavailable")})
	m = updated.(Model)
	if m.worktreeErr == nil || len(m.worktreeEntries) != 6 || m.worktreeLoading {
		t.Fatalf("error state = err=%v entries=%d loading=%v", m.worktreeErr, len(m.worktreeEntries), m.worktreeLoading)
	}
}

func TestWorktreeRefreshSelectsNearestRowWhenSelectedEntryDisappears(t *testing.T) {
	m := enterLoadedWorktrees(t, worktreeTestModel())
	m.worktreeFilter = ""
	m.worktreeCursor = 3
	m.captureWorktreeSelection()
	removedID := m.worktreeSelectedID
	entries := append([]actions.WorktreeView(nil), m.worktreeEntries[:3]...)
	entries = append(entries, m.worktreeEntries[4:]...)
	m.worktreeRefreshSeq++
	updated, _ := m.Update(worktreesLoadedMsg{seq: m.worktreeRefreshSeq, entries: entries})
	m = updated.(Model)
	if len(m.worktreeFiltered) != len(entries) || m.worktreeCursor != 3 {
		t.Fatalf("refresh after removing %q = cursor %d rows %d", removedID, m.worktreeCursor, len(m.worktreeFiltered))
	}
	if m.worktreeFiltered[m.worktreeCursor].ID == removedID {
		t.Fatalf("removed row %q remained selected", removedID)
	}
}

func TestWorktreeAttentionOrderingAndWhitespaceANDFilter(t *testing.T) {
	m := enterLoadedWorktrees(t, worktreeTestModel())
	classes := make([]string, len(m.worktreeEntries))
	for i, entry := range m.worktreeEntries {
		classes[i] = tuiworktrees.StateLabels(entry)[0]
	}
	if got := strings.Join(classes, ","); got != "metadata_invalid,missing,valid,valid,valid,valid" {
		t.Fatalf("attention classes = %s", got)
	}
	ids := make([]string, len(m.worktreeEntries))
	for i, entry := range m.worktreeEntries {
		ids[i] = entry.ID
	}
	if got := strings.Join(ids, ","); got != "wt-invalid,wt-missing,wt-live,wt-loop,wt-dirty,wt-clean" {
		t.Fatalf("attention order = %s", got)
	}

	for _, tc := range []struct {
		query string
		id    string
	}{
		{"API DIRTY", "wt-dirty"},
		{"backend live claude", "wt-live"},
		{"worker looping", "wt-loop"},
		{"agentmux merged clean", "wt-clean"},
		{"broken warning invalid", "wt-invalid"},
	} {
		m.worktreeFilter = tc.query
		m.reapplyWorktreeFilter()
		if len(m.worktreeFiltered) != 1 || m.worktreeFiltered[0].ID != tc.id {
			t.Errorf("filter %q = %+v, want %s", tc.query, m.worktreeFiltered, tc.id)
		}
	}
	m.worktreeFilter = "api missing"
	m.reapplyWorktreeFilter()
	if len(m.worktreeFiltered) != 0 {
		t.Fatalf("AND filter accepted partial match: %+v", m.worktreeFiltered)
	}
}

func TestWorktreeNavigationFilterAndReturn(t *testing.T) {
	m := enterLoadedWorktrees(t, worktreeTestModel())
	for _, key := range []string{"j", "down", "k", "up", "G", "g", "ctrl+d", "ctrl+u", "pgdown", "pgup", "ctrl+f", "ctrl+b"} {
		updated, _ := m.Update(keyPress(key))
		m = updated.(Model)
		if m.worktreeCursor < 0 || m.worktreeCursor >= len(m.worktreeFiltered) {
			t.Fatalf("%s moved cursor out of bounds: %d", key, m.worktreeCursor)
		}
	}
	m = pressKey(m, "/")
	m = typeString(m, "dirty")
	if !m.worktreeFilterMode || len(m.worktreeFiltered) != 1 {
		t.Fatalf("live filter = mode %v results %d", m.worktreeFilterMode, len(m.worktreeFiltered))
	}
	m = pressKey(m, "enter")
	if m.worktreeFilterMode || m.mode != ModeWorktrees {
		t.Fatalf("enter did not close worktree filter: mode=%v input=%v", m.mode, m.worktreeFilterMode)
	}
	m = pressKey(m, "/")
	m = pressKey(m, "esc")
	if m.worktreeFilterMode || m.mode != ModeWorktrees || m.worktreeFilter != "dirty" || len(m.worktreeFiltered) != 1 {
		t.Fatalf("filter esc = mode=%v input=%v filter=%q results=%d", m.mode, m.worktreeFilterMode, m.worktreeFilter, len(m.worktreeFiltered))
	}
	m = pressKey(m, "esc")
	if m.mode != ModeWorktrees || m.worktreeFilter != "" || len(m.worktreeFiltered) != len(m.worktreeEntries) {
		t.Fatalf("navigation esc = mode=%v filter=%q results=%d entries=%d", m.mode, m.worktreeFilter, len(m.worktreeFiltered), len(m.worktreeEntries))
	}
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("second navigation esc = mode %v, want list", m.mode)
	}
	updated, cmd := m.Update(keyPress("W"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("re-entering Worktrees returned no refresh command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if m.mode != ModeWorktrees || m.worktreeFilter != "" || len(m.worktreeFiltered) != len(m.worktreeEntries) {
		t.Fatalf("re-entry = mode=%v filter=%q results=%d entries=%d", m.mode, m.worktreeFilter, len(m.worktreeFiltered), len(m.worktreeEntries))
	}
}

func TestWorktreeShellEditorAndCopyActions(t *testing.T) {
	fx := worktreesFixture()
	client := newUITestKittyClient(fx.Windows)
	m := NewModel(fx.Config, client)
	m.width, m.height = 120, 30
	m.mode = ModeWorktrees
	m.worktreeEntries = tuiworktrees.SortViews(fx.Worktrees)
	m.worktreeFiltered = append([]actions.WorktreeView(nil), m.worktreeEntries...)
	for i, entry := range m.worktreeFiltered {
		if entry.ID == "wt-clean" {
			m.worktreeCursor = i
		}
	}

	for _, key := range []string{"o", "O"} {
		updated, cmd := m.Update(keyPress(key))
		m = updated.(Model)
		if cmd == nil {
			t.Fatalf("%s returned no action command", key)
		}
		updated, _ = m.Update(cmd())
		m = updated.(Model)
		if m.actionErr != nil {
			t.Fatalf("%s action error: %v", key, m.actionErr)
		}
	}
	if got := len(client.CallsFor("LaunchTab")); got != 2 {
		t.Fatalf("LaunchTab calls = %d, want shell + editor", got)
	}

	oldCopy := commands.CopyToClipboard
	t.Cleanup(func() { commands.CopyToClipboard = oldCopy })
	var copied string
	commands.CopyToClipboard = func(value string) error { copied = value; return nil }
	updated, cmd := m.Update(keyPress("Y"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("Y returned no copy command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	if copied != selectedWorktreePath(m) || m.actionErr != nil {
		t.Fatalf("copied %q want %q err=%v", copied, selectedWorktreePath(m), m.actionErr)
	}
}

func TestWorktreeMissingRowDoesNotExposeOpenCommand(t *testing.T) {
	m := enterLoadedWorktrees(t, worktreeTestModel())
	for i, entry := range m.worktreeFiltered {
		if entry.ID == "wt-missing" {
			m.worktreeCursor = i
		}
	}
	for _, key := range []string{"o", "O"} {
		updated, cmd := m.Update(keyPress(key))
		m = updated.(Model)
		if cmd != nil {
			t.Fatalf("%s on missing row returned command %T", key, cmd)
		}
	}
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "o shell") || !strings.Contains(footer, "O editor") {
		t.Fatalf("footer omitted disabled open hints: %q", footer)
	}
}

func TestWorktreeRenderingStatusFooterHelpAndWidths(t *testing.T) {
	m := enterLoadedWorktrees(t, worktreeTestModel())
	for _, width := range []int{24, 60, 120, 180} {
		for _, height := range []int{8, 16, 36} {
			m.width, m.height = width, height
			scrollWorktreesToCursor(&m)
			out := stripAnsi(m.render())
			lines := strings.Split(out, "\n")
			if len(lines) != height {
				t.Errorf("%dx%d rendered %d lines", width, height, len(lines))
			}
			for i, line := range lines {
				if got := layout.Width(line); got > width {
					t.Errorf("%dx%d line %d width %d: %q", width, height, i, got, line)
				}
			}
		}
	}

	m.width, m.height = 160, 32
	out := stripAnsi(renderWorktrees(m))
	for _, want := range []string{"Worktrees", "worktree path", "missing", "invalid", "live", "looping", "dirty", "clean", "locked"} {
		if want == "locked" {
			continue // lock coverage is exercised by the formatter below.
		}
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
	locked := m.worktreeEntries[len(m.worktreeEntries)-1]
	locked.Locked = true
	if !strings.Contains(tuiworktrees.BadgeText(locked), "locked") {
		t.Fatal("locked badge missing")
	}
	if status := stripAnsi(renderStatusBar(m)); !strings.Contains(status, "Worktrees: 6 managed") {
		t.Fatalf("status = %q", status)
	}
	footer := stripAnsi(renderFooter(m))
	for _, want := range []string{"/ filter", "o shell", "O editor", "Y path", "r refresh", "? help", "esc back"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer missing %q: %q", want, footer)
		}
	}
	m.openHelp(ModeWorktrees)
	if base := helpBaseView(m); !strings.Contains(stripAnsi(base), "Worktrees") {
		t.Fatal("help did not retain Worktrees backdrop")
	}
	m.height = 0
	if help := stripAnsi(renderHelp(m)); !strings.Contains(help, "Worktrees") || !strings.Contains(help, "shell") {
		t.Fatalf("help missing Worktrees keymap:\n%s", help)
	}
}

func TestDashboardAndCommandPaletteEnterWorktreesWithLifecycleControls(t *testing.T) {
	m := loadSessions(worktreeTestModel(), makeSessions())
	updated, cmd := m.Update(keyPress("W"))
	m = updated.(Model)
	if m.mode != ModeWorktrees || cmd == nil {
		t.Fatalf("W = mode %v cmd %T", m.mode, cmd)
	}

	m = loadSessions(worktreeTestModel(), makeSessions())
	m.openCommandPalette()
	m.commandQuery = "manage worktrees"
	m.recomputeCommandPaletteResults()
	if len(m.commandResults) != 1 || m.commandResults[0].ID != commands.ManageWorktrees {
		t.Fatalf("palette results = %v", commandResultIDs(m.commandResults))
	}
	updated, cmd = m.Update(keyPress("enter"))
	m = updated.(Model)
	if m.mode != ModeWorktrees || cmd == nil {
		t.Fatalf("palette enter = mode %v cmd %T", m.mode, cmd)
	}
	footer := strings.ToLower(stripAnsi(renderFooter(m)))
	m.openHelp(ModeWorktrees)
	m.height = 0
	help := strings.ToLower(stripAnsi(renderHelp(m)))
	for _, control := range []string{"reopen", "remove", "delete branch", "forget"} {
		if !strings.Contains(help, control) {
			t.Fatalf("lifecycle control %q missing from help", control)
		}
	}
	if !strings.Contains(footer, "enter reopen") || !strings.Contains(footer, "d remove") {
		t.Fatalf("primary lifecycle controls missing from footer: %q", footer)
	}
}
