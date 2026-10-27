package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// ---- Picker open directory tests ----

type fakePickerOpenClient struct {
	launched      []kitty.LaunchOpts
	launchErrorBy map[string]error
}

func (f *fakePickerOpenClient) ListWindows() ([]kitty.OSWindow, error) { return nil, nil }
func (f *fakePickerOpenClient) LaunchTab(opts kitty.LaunchOpts) error {
	f.launched = append(f.launched, opts)
	return f.launchErrorBy[opts.Directory]
}
func (f *fakePickerOpenClient) LaunchOverlay(kitty.LaunchOpts) error { return nil }
func (f *fakePickerOpenClient) CloseTab(string) error                { return nil }
func (f *fakePickerOpenClient) MoveTab(string, int) error            { return nil }
func (f *fakePickerOpenClient) SetTabColor(string, string, string, string, string) error {
	return nil
}
func (f *fakePickerOpenClient) SetTabTitle(string, string) error            { return nil }
func (f *fakePickerOpenClient) SetUserVars(string, map[string]string) error { return nil }
func (f *fakePickerOpenClient) FocusTab(string) error                       { return nil }
func (f *fakePickerOpenClient) FocusWindow(string) error                    { return nil }
func (f *fakePickerOpenClient) GetText(string, string) (string, error)      { return "", nil }
func (f *fakePickerOpenClient) LaunchOverlayPager(string) error             { return nil }

func pickerOpenModel(t *testing.T, client kitty.KittyClient) Model {
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
	m := loadSessions(NewModel(cfg, client), nil)
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

func TestPickerOKeyOpensSelectedProjectsTheFilterShows(t *testing.T) {
	t.Parallel()

	client := &fakePickerOpenClient{}
	m := pickerOpenModel(t, client)
	apiIdx := pickerProjectIndex(t, m, "api")
	backendIdx := pickerProjectIndex(t, m, "backend")
	dotfilesIdx := pickerProjectIndex(t, m, "dotfiles")
	m.pickerSelected[apiIdx] = true
	m.pickerSelected[backendIdx] = true
	m.pickerSelected[dotfilesIdx] = true
	m.pickerProfileOverride[backendIdx] = "alternate"

	// Hide dotfiles. Like Enter, o opens only the checked projects the filter
	// shows, and says the hidden one was skipped.
	m.pickerFilteredIdx = []int{apiIdx, backendIdx}
	m.pickerCursor = 0

	if targets, hidden := m.pickerOpenTargets(); len(targets) != 2 || hidden != 1 {
		t.Fatalf("open targets = %d (hidden %d), want 2 and 1 hidden", len(targets), hidden)
	}
	updated, cmd := m.Update(keyPress("o"))
	if updated.(Model).mode != ModePicker {
		t.Fatalf("mode after o = %v, want ModePicker", updated.(Model).mode)
	}
	if toasts := updated.(Model).toasts; len(toasts) == 0 || toasts[len(toasts)-1].Severity != toastWarning ||
		toasts[len(toasts)-1].Text != "Skipped 1 checked project hidden by the filter" {
		t.Errorf("expected a hidden-skipped warning toast, got %+v", toasts)
	}
	if cmd == nil {
		t.Fatal("o returned nil cmd for selected projects")
	}
	done := pickerOpenDone(t, cmd)
	if done.Err != nil {
		t.Fatalf("opening selected projects: %v", done.Err)
	}

	gotDirs := make([]string, 0, len(client.launched))
	for _, opts := range client.launched {
		gotDirs = append(gotDirs, opts.Directory)
	}
	wantDirs := make([]string, 0, 2)
	for i, sess := range m.pickerSessions {
		if m.pickerSelected[i] && i != dotfilesIdx {
			wantDirs = append(wantDirs, sess.Directory)
		}
	}
	if !slices.Equal(gotDirs, wantDirs) {
		t.Fatalf("opened directories = %v, want stable picker order %v", gotDirs, wantDirs)
	}
	for _, opts := range client.launched {
		if opts.Directory == "/projects/backend" && opts.Title != "backend" {
			t.Errorf("backend title = %q, want project title without a harness icon", opts.Title)
		}
		if opts.Directory == "/projects/api" && opts.Title != "api" {
			t.Errorf("api title = %q, want project title without a harness icon", opts.Title)
		}
		if opts.Directory == "/projects/dotfiles" {
			t.Error("opened dotfiles, which the filter hides")
		}
	}

	_, quitCmd := updated.(Model).Update(done)
	if quitCmd == nil {
		t.Fatal("successful selected-project batch returned nil completion cmd")
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Fatal("successful selected-project batch did not quit")
	}
}

// pickerOpenDone runs an o command, which may be batched with a warning
// toast's command, and returns its completion message.
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
		t.Fatal("o batch carried no commands.PickerOpenDoneMsg")
	}
	done, ok := msg.(commands.PickerOpenDoneMsg)
	if !ok {
		t.Fatalf("o cmd returned %T, want commands.PickerOpenDoneMsg", msg)
	}
	return done
}

// TestPickerOKeyOpensNothingWhenTheFilterHidesEveryCheckedProject pins the
// reported case: tick api and backend, filter down to dotfiles, press o. No
// tab opens for the hidden projects, the highlighted dotfiles is not opened
// in their place, and the launcher stays open saying why.
func TestPickerOKeyOpensNothingWhenTheFilterHidesEveryCheckedProject(t *testing.T) {
	t.Parallel()

	client := &fakePickerOpenClient{}
	m := pickerOpenModel(t, client)
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
	if len(client.launched) != 0 {
		t.Fatalf("opened tabs = %+v, want none", client.launched)
	}
}

func TestPickerOKeyWithoutSelectionOpensHighlightedProject(t *testing.T) {
	t.Parallel()

	client := &fakePickerOpenClient{}
	m := pickerOpenModel(t, client)
	backendIdx := pickerProjectIndex(t, m, "backend")
	for cursor, idx := range m.pickerFilteredIdx {
		if idx == backendIdx {
			m.pickerCursor = cursor
			break
		}
	}

	_, cmd := m.Update(keyPress("o"))
	if cmd == nil {
		t.Fatal("o returned nil cmd for highlighted fallback")
	}
	done := pickerOpenDone(t, cmd)
	if done.Err != nil {
		t.Fatalf("opening highlighted project: %v", done.Err)
	}
	if len(client.launched) != 1 || client.launched[0].Directory != "/projects/backend" {
		t.Fatalf("opened tabs = %+v, want only highlighted backend", client.launched)
	}
}

func TestPickerOKeyBatchContinuesAfterPartialFailureAndAggregatesErrors(t *testing.T) {
	t.Parallel()

	client := &fakePickerOpenClient{launchErrorBy: map[string]error{
		"/projects/api":     errors.New("api launch failed"),
		"/projects/backend": errors.New("backend launch failed"),
	}}
	m := pickerOpenModel(t, client)
	apiIdx := pickerProjectIndex(t, m, "api")
	backendIdx := pickerProjectIndex(t, m, "backend")
	dotfilesIdx := pickerProjectIndex(t, m, "dotfiles")
	m.pickerSelected[apiIdx] = true
	m.pickerSelected[backendIdx] = true
	m.pickerSelected[dotfilesIdx] = true

	updated, cmd := m.Update(keyPress("o"))
	if cmd == nil {
		t.Fatal("o returned nil cmd for selected projects")
	}
	done, ok := cmd().(commands.PickerOpenDoneMsg)
	if !ok {
		t.Fatalf("o cmd returned %T, want commands.PickerOpenDoneMsg", done)
	}
	if done.Err == nil {
		t.Fatal("batch error = nil, want aggregate error")
	}
	for _, want := range []string{"api: ", "api launch failed", "backend: ", "backend launch failed"} {
		if !strings.Contains(done.Err.Error(), want) {
			t.Errorf("batch error %q missing %q", done.Err, want)
		}
	}
	if got := len(client.launched); got != 3 {
		t.Fatalf("launch attempts = %d, want all 3 despite two failures", got)
	}

	afterDone, refreshCmd := updated.(Model).Update(done)
	got := afterDone.(Model)
	if got.mode != ModePicker {
		t.Fatalf("mode after batch failure = %v, want ModePicker", got.mode)
	}
	if got.actionErr == nil {
		t.Fatal("batch failure did not populate actionErr")
	}
	if refreshCmd == nil {
		t.Fatal("batch failure did not request a refresh")
	}
	if _, ok := refreshCmd().(tea.QuitMsg); ok {
		t.Fatal("batch failure unexpectedly quit")
	}
}

// TestPickerOKeyEmptyStaysWithWarning mirrors Enter on an empty launcher:
// nothing opens, and the launcher stays open and says why.
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

func TestPickerOpenDoneMsgQuitsOnSuccess(t *testing.T) {
	t.Parallel()

	m := testModel()
	_, cmd := m.Update(commands.PickerOpenDoneMsg{Err: nil})
	if cmd == nil {
		t.Fatal("expected non-nil cmd on success")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg on success, got %T", msg)
	}
}

func TestPickerOpenDoneMsgStaysOpenOnError(t *testing.T) {
	stubUnreachableDaemon(t)
	// The startup refresh has to be settled first: a post-action refresh
	// requested while one is already in flight coalesces and returns no
	// command, which is a different (also correct) outcome than this test is
	// about.
	m := loadSessions(testModel(), nil)
	updated, cmd := m.Update(commands.PickerOpenDoneMsg{Err: errors.New("kitten exploded")})
	if updated.(Model).actionErr == nil {
		t.Fatal("expected actionErr set on error")
	}
	if cmd == nil {
		t.Fatal("expected a non-nil refresh cmd on error")
	}
	// The refresh emits commands.SessionsMsg or commands.ErrMsg; it must not be tea.Quit.
	// Invoking it requires a live Kitty client, so we only assert by type
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
