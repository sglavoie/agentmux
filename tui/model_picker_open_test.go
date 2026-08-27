package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
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

func TestPickerOKeyOpensAllSelectedProjectsIncludingFilteredRows(t *testing.T) {
	client := &fakePickerOpenClient{}
	m := pickerOpenModel(t, client)
	apiIdx := pickerProjectIndex(t, m, "api")
	backendIdx := pickerProjectIndex(t, m, "backend")
	m.pickerSelected[apiIdx] = true
	m.pickerSelected[backendIdx] = true
	m.pickerProfileOverride[backendIdx] = "alternate"

	// Keep only api visible. The selected backend must still be opened, while
	// the unselected dotfiles row must not be included.
	m.pickerFilter = "api"
	m.pickerFilteredIdx = m.pickerFilteredIndices(m.pickerFilter)
	m.pickerCursor = 0

	updated, cmd := m.Update(keyPress("o"))
	if updated.(Model).mode != ModePicker {
		t.Fatalf("mode after o = %v, want ModePicker", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("o returned nil cmd for selected projects")
	}
	done, ok := cmd().(pickerOpenDoneMsg)
	if !ok {
		t.Fatalf("o cmd returned %T, want pickerOpenDoneMsg", done)
	}
	if done.err != nil {
		t.Fatalf("opening selected projects: %v", done.err)
	}

	gotDirs := make([]string, 0, len(client.launched))
	for _, opts := range client.launched {
		gotDirs = append(gotDirs, opts.Directory)
	}
	wantDirs := make([]string, 0, 2)
	for i, sess := range m.pickerSessions {
		if m.pickerSelected[i] {
			wantDirs = append(wantDirs, sess.Directory)
		}
	}
	if !slices.Equal(gotDirs, wantDirs) {
		t.Fatalf("opened directories = %v, want stable picker order %v", gotDirs, wantDirs)
	}
	for _, opts := range client.launched {
		if opts.Directory == "/projects/backend" && opts.Title != "🤖backend" {
			t.Errorf("backend title = %q, want alternate-profile title %q", opts.Title, "🤖backend")
		}
		if opts.Directory == "/projects/dotfiles" {
			t.Error("opened unselected dotfiles project")
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

func TestPickerOKeyWithoutSelectionOpensHighlightedProject(t *testing.T) {
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
	done, ok := cmd().(pickerOpenDoneMsg)
	if !ok {
		t.Fatalf("o cmd returned %T, want pickerOpenDoneMsg", done)
	}
	if done.err != nil {
		t.Fatalf("opening highlighted project: %v", done.err)
	}
	if len(client.launched) != 1 || client.launched[0].Directory != "/projects/backend" {
		t.Fatalf("opened tabs = %+v, want only highlighted backend", client.launched)
	}
}

func TestPickerOKeyBatchContinuesAfterPartialFailureAndAggregatesErrors(t *testing.T) {
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
	done, ok := cmd().(pickerOpenDoneMsg)
	if !ok {
		t.Fatalf("o cmd returned %T, want pickerOpenDoneMsg", done)
	}
	if done.err == nil {
		t.Fatal("batch error = nil, want aggregate error")
	}
	for _, want := range []string{"api: ", "api launch failed", "backend: ", "backend launch failed"} {
		if !strings.Contains(done.err.Error(), want) {
			t.Errorf("batch error %q missing %q", done.err, want)
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

func TestPickerOKeyEmptyIsNoOp(t *testing.T) {
	m := pressKey(testModel(), "l")
	updated, cmd := m.Update(keyPress("o"))
	if updated.(Model).mode != ModePicker {
		t.Fatalf("expected ModePicker after o on empty picker, got %d", updated.(Model).mode)
	}
	if cmd != nil {
		t.Fatal("expected nil cmd after o on empty picker")
	}
}

func TestPickerOpenEditorKeyEmitsCmd(t *testing.T) {
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
	m := testModel()
	_, cmd := m.Update(pickerOpenDoneMsg{err: nil})
	if cmd == nil {
		t.Fatal("expected non-nil cmd on success")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("expected tea.QuitMsg on success, got %T", msg)
	}
}

func TestPickerOpenDoneMsgStaysOpenOnError(t *testing.T) {
	m := testModel()
	updated, cmd := m.Update(pickerOpenDoneMsg{err: errors.New("kitten exploded")})
	if updated.(Model).actionErr == nil {
		t.Fatal("expected actionErr set on error")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd (fetchCmd) on error")
	}
	// fetchCmd would emit sessionsMsg or errMsg; it must not be tea.Quit.
	// Invoking it requires a live Kitty client, so we only assert by type
	// of cmd indirectly: invoke and confirm we did NOT get a QuitMsg.
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); ok {
		t.Fatal("expected non-QuitMsg on error, got tea.QuitMsg")
	}
}

func TestFooterPickerOpenEditorHint(t *testing.T) {
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

func TestEKeyReturnsPeekCmd(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	_, cmd := m.Update(keyPress("e"))
	if cmd == nil {
		t.Fatal("expected non-nil cmd after e with sessions")
	}
}

func TestEKeyEmptyListIsNoOp(t *testing.T) {
	m := loadSessions(testModel(), nil)
	_, cmd := m.Update(keyPress("e"))
	if cmd != nil {
		t.Fatal("expected nil cmd after e with no sessions")
	}
}

func TestPeekDoneMsgErrorSetsActionErr(t *testing.T) {
	m := testModel()
	updated, _ := m.Update(peekDoneMsg{err: errors.New("window gone")})
	if updated.(Model).actionErr == nil {
		t.Fatal("expected actionErr set after peekDoneMsg with error")
	}
}

func TestFooterListHintIncludesPeek(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	// Order after sort: api(""), backend(bolt), dotfiles("") — move
	// cursor onto the bolt session so the active footer branch fires.
	m = pressKey(m, "j")
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "e peek") {
		t.Errorf("list footer missing peek hint, got:\n%s", footer)
	}
}

func TestFooterPickerOpenDirHint(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModePicker
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "o open dirs") {
		t.Errorf("picker footer missing open dir hint, got:\n%s", footer)
	}
}
