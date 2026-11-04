package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// ---- Picker open directory tests ----

func pickerOpenModel(t *testing.T) Model {
	t.Helper()
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", Color: "invalid"},
			{Name: "personal", Harness: "claude", Color: "invalid"},
			{Name: "alternate", Harness: "codex", Color: "invalid"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/projects/api", Account: "work"},
			{Title: "backend", Directory: "/projects/backend", Account: "work"},
			{Title: "dotfiles", Directory: "/projects/dotfiles", Account: "personal"},
		},
	}
	m := loadSessions(NewModel(cfg, nil), nil)
	m = pressKey(m, "l")
	if m.mode != ModePicker {
		t.Fatalf("setup: mode = %v, want ModePicker", m.mode)
	}
	return m
}

func pickerProjectIndex(t *testing.T, m Model, title string) int {
	t.Helper()
	for i, sess := range m.pickerSessions {
		if sess.Title == title {
			return i
		}
	}
	t.Fatalf("picker project %q not found", title)
	return -1
}

func TestPickerOKeyEmitsCmd(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l")
	updated, cmd := m.Update(keyPress("o"))
	if updated.(Model).mode != ModePicker {
		t.Fatalf("expected ModePicker after o (stays in picker), got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after o in picker")
	}
}

// TestPickerOKeyOpensNothingWhenTheFilterHidesEveryCheckedProject pins the
// reported case: tick api and backend, filter down to dotfiles, press o. No
// pane opens for the hidden projects, the highlighted dotfiles is not opened
// in their place, and the launcher stays open saying why.
func TestPickerOKeyOpensNothingWhenTheFilterHidesEveryCheckedProject(t *testing.T) {
	t.Parallel()

	m := pickerOpenModel(t)
	m.pickerSelected[pickerProjectIndex(t, m, "api")] = true
	m.pickerSelected[pickerProjectIndex(t, m, "backend")] = true
	m.pickerFilteredIdx = []int{pickerProjectIndex(t, m, "dotfiles")}
	m.pickerCursor = 0

	if targets, hidden := m.pickerOpenTargets(); len(targets) != 0 || hidden != 2 {
		t.Fatalf("open targets = %+v (hidden %d), want none and 2 hidden", targets, hidden)
	}
	updated, cmd := m.Update(keyPress("o"))
	got := updated.(Model)
	if got.mode != ModePicker {
		t.Fatalf("mode after o = %v, want ModePicker", got.mode)
	}
	if n := len(got.toasts); n == 0 || got.toasts[n-1].Severity != toastWarning ||
		got.toasts[n-1].Text != "Nothing to open: the filter hides 2 checked" {
		t.Errorf("expected a 'Nothing to open' warning toast, got %+v", got.toasts)
	}
	if cmd != nil {
		if _, ok := cmd().(commands.PickerOpenDoneMsg); ok {
			t.Fatal("o opened something with every checked project hidden")
		}
	}
}

func pickerOpenDone(t *testing.T, cmd tea.Cmd) commands.PickerOpenDoneMsg {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c == nil {
				continue
			}
			if done, ok := c().(commands.PickerOpenDoneMsg); ok {
				return done
			}
		}
		t.Fatal("o batch carried no PickerOpenDoneMsg")
	}
	done, ok := msg.(commands.PickerOpenDoneMsg)
	if !ok {
		t.Fatalf("o cmd returned %T, want PickerOpenDoneMsg", msg)
	}
	return done
}

func TestPickerOKeyEmptyStaysWithWarning(t *testing.T) {
	t.Parallel()

	m := pressKey(testModel(), "l")
	updated, _ := m.Update(keyPress("o"))
	got := updated.(Model)
	if got.mode != ModePicker {
		t.Fatalf("expected ModePicker after o on empty picker, got %d", got.mode)
	}
	if n := len(got.toasts); n == 0 || got.toasts[n-1].Text != "Nothing to open: no project matches" {
		t.Errorf("expected a 'Nothing to open' warning toast, got %+v", got.toasts)
	}
}

func TestPickerOpenEditorKeyEmitsCmd(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), makeSessions())
	m = pressKey(m, "l")
	updated, cmd := m.Update(keyPress("O"))
	if updated.(Model).mode != ModePicker {
		t.Fatalf("expected ModePicker after O (stays in picker), got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after O in picker")
	}
}

func TestPickerOpenEditorKeyEmptyIsNoOp(t *testing.T) {
	t.Parallel()

	m := pressKey(testModel(), "l")
	updated, cmd := m.Update(keyPress("O"))
	if updated.(Model).mode != ModePicker {
		t.Fatalf("expected ModePicker after O on empty picker, got %d", updated.(Model).mode)
	}
	if cmd != nil {
		t.Fatal("expected nil cmd after O on empty picker")
	}
}

func TestPickerOpenDoneMsgStaysOnSuccess(t *testing.T) {
	stubUnreachableDaemon(t)
	m := loadSessions(testModel(), nil)
	_, cmd := m.Update(commands.PickerOpenDoneMsg{})
	if cmd == nil {
		t.Fatal("opened view did not request a session refresh")
	}
	if _, ok := cmd().(tea.QuitMsg); ok {
		t.Fatal("opened view quit the dashboard")
	}
}

func TestPickerOpenDoneMsgStaysOpenOnError(t *testing.T) {
	stubUnreachableDaemon(t)
	// The startup refresh has to be settled first: a post-action refresh
	// requested while one is already in flight coalesces and returns no
	// command, which is a different (also correct) outcome than this test is
	// about.
	m := loadSessions(testModel(), nil)
	updated, cmd := m.Update(commands.PickerOpenDoneMsg{Err: errors.New("tmux server unavailable")})
	if updated.(Model).actionErr == nil {
		t.Fatal("expected actionErr set on error")
	}
	if cmd == nil {
		t.Fatal("expected a non-nil refresh cmd on error")
	}
	// The refresh emits commands.SessionsMsg or commands.ErrMsg; it must not be tea.Quit.
	// The error refresh is safe to invoke, so we assert by type
	// of cmd indirectly: invoke and confirm we did NOT get a QuitMsg.
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); ok {
		t.Fatal("expected non-QuitMsg on error, got tea.QuitMsg")
	}
}

func TestFooterPickerOpenEditorHint(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModel(), makeSessions())
	m.mode = ModePicker
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "O open in editor") {
		t.Errorf("picker footer missing open in editor hint, got:\n%s", footer)
	}
	if !strings.Contains(footer, "o open dirs") {
		t.Errorf("picker footer missing open dir hint, got:\n%s", footer)
	}
}

// ---- Peek mode tests ----

func TestPeekActionReturnsPeekCmd(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModel(), makeSessions())
	// Peek reads a live pane, so the overlay offers it only on a working row.
	// Order after sort: backend(bolt) first.
	_, cmd := runRowAction(t, m, keyE)
	if cmd == nil {
		t.Fatal("expected non-nil cmd from the peek action")
	}
}

// `e` is retired at the list: peek is an overlay entry now, and the bare key
// dispatches nothing whether or not there is a row to peek at.
func TestRetiredPeekKeyIsInertAtTheList(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		sessions []session.RunningSession
	}{
		{name: "with sessions", sessions: makeSessions()},
		{name: "empty list", sessions: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := loadSessions(testModel(), tc.sessions)
			updated, cmd := m.Update(keyPress("e"))
			if cmd != nil {
				t.Fatal("e still dispatched a command from the list")
			}
			if updated.(Model).mode != ModeList {
				t.Fatalf("e left ModeList for mode=%d", updated.(Model).mode)
			}
		})
	}
}

func TestPeekDoneMsgErrorSetsActionErr(t *testing.T) {
	t.Parallel()

	m := testModel()
	updated, _ := m.Update(commands.PeekDoneMsg{Err: errors.New("window gone")})
	if updated.(Model).actionErr == nil {
		t.Fatal("expected actionErr set after commands.PeekDoneMsg with error")
	}
}

// The dashboard footer no longer names peek: it names `a`, and the overlay `a`
// opens is where peek is offered or dimmed. The picker footer below still names
// its own keys, because the picker keeps its direct keymap.
func TestFooterListPointsAtTheOverlayRatherThanPeek(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModel(), makeSessions())
	// Order after sort: backend(bolt) first — the state that used to switch the
	// footer into its "active" branch.
	footer := stripAnsi(renderFooter(m))
	if strings.Contains(footer, "e peek") {
		t.Errorf("list footer still advertises the retired peek key, got:\n%s", footer)
	}
	if !strings.Contains(footer, "a actions") {
		t.Errorf("list footer missing the route to peek, got:\n%s", footer)
	}
}

func TestFooterPickerOpenDirHint(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModel(), makeSessions())
	m.mode = ModePicker
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "o open dirs") {
		t.Errorf("picker footer missing open dir hint, got:\n%s", footer)
	}
}

// The two tests below swap the process's portable terminal set, so they do
// not run in parallel.

// TestPickerOKeyOpensEveryTargetInTheWorkspace: hosted by the desktop
// workspace, each checked project opens as a shell view of its own. Only the first used to open.
func TestPickerOKeyOpensEveryTargetInTheWorkspace(t *testing.T) {
	previous := commands.Portable
	t.Cleanup(func() { commands.Portable = previous })
	var shown []protocol.WorkspaceViewRequest
	commands.Portable = commands.PortableTerminals{Desktop: func(request protocol.WorkspaceViewRequest) error {
		shown = append(shown, request)
		return nil
	}}

	m := pickerOpenModel(t)
	m.pickerSelected[pickerProjectIndex(t, m, "api")] = true
	m.pickerSelected[pickerProjectIndex(t, m, "backend")] = true
	updated, cmd := m.Update(keyPress("o"))
	if toasts := updated.(Model).toasts; len(toasts) != 0 {
		t.Fatalf("opening in the workspace raised %+v", toasts)
	}
	done := pickerOpenDone(t, cmd)
	if done.Err != nil {
		t.Fatalf("open done = %+v, want no error and the dashboard kept", done)
	}
	var dirs []string
	for _, request := range shown {
		if request.Kind != protocol.WorkspaceViewShell {
			t.Errorf("workspace was asked for a %q view, want a shell", request.Kind)
		}
		dirs = append(dirs, request.Directory)
	}
	if want := []string{"/projects/api", "/projects/backend"}; !slices.Equal(dirs, want) {
		t.Fatalf("workspace shells = %v, want %v", dirs, want)
	}
}

// TestPickerOKeyInAPlainTerminalSaysWhatItLeftUnopened: outside the
// workspace only one shell can run in this terminal. The first project opens
// there and the rest are named in a warning rather than silently dropped.
func TestPickerOKeyInAPlainTerminalSaysWhatItLeftUnopened(t *testing.T) {
	previous := commands.Portable
	t.Cleanup(func() { commands.Portable = previous })
	commands.Portable = commands.PortableTerminals{}

	m := pickerOpenModel(t)
	m.pickerSelected[pickerProjectIndex(t, m, "api")] = true
	m.pickerSelected[pickerProjectIndex(t, m, "backend")] = true
	m.pickerSelected[pickerProjectIndex(t, m, "dotfiles")] = true
	targets, _ := m.pickerOpenTargets()
	if len(targets) != 3 {
		t.Fatalf("open targets = %d, want 3", len(targets))
	}
	updated, cmd := m.Update(keyPress("o"))
	if cmd == nil {
		t.Fatal("o opened nothing in a plain terminal")
	}
	toasts := updated.(Model).toasts
	want := "Opened " + targets[0].Title + " here; 2 more projects need a desktop workspace to open"
	if len(toasts) == 0 || toasts[len(toasts)-1].Severity != toastWarning || toasts[len(toasts)-1].Text != want {
		t.Fatalf("toasts = %+v, want the warning %q", toasts, want)
	}
}
