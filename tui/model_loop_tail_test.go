package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

type fakeLoopTailClient struct {
	overlays   []kitty.LaunchOpts
	overlayErr error
}

func (f *fakeLoopTailClient) ListWindows() ([]kitty.OSWindow, error) { return nil, nil }
func (f *fakeLoopTailClient) LaunchTab(kitty.LaunchOpts) error       { return nil }
func (f *fakeLoopTailClient) LaunchOverlay(opts kitty.LaunchOpts) error {
	f.overlays = append(f.overlays, opts)
	return f.overlayErr
}
func (f *fakeLoopTailClient) CloseTab(string) error                                    { return nil }
func (f *fakeLoopTailClient) MoveTab(string, int) error                                { return nil }
func (f *fakeLoopTailClient) SetTabColor(string, string, string, string, string) error { return nil }
func (f *fakeLoopTailClient) SetTabTitle(string, string) error                         { return nil }
func (f *fakeLoopTailClient) SetUserVars(string, map[string]string) error              { return nil }
func (f *fakeLoopTailClient) FocusTab(string) error                                    { return nil }
func (f *fakeLoopTailClient) FocusWindow(string) error                                 { return nil }
func (f *fakeLoopTailClient) GetText(string, string) (string, error)                   { return "", nil }
func (f *fakeLoopTailClient) LaunchOverlayPager(string) error                          { return nil }

func stubLoopTailDiscover(t *testing.T, candidates []agentloop.Candidate, err error) {
	t.Helper()
	old := loopTailDiscover
	loopTailDiscover = func(agentloop.DiscoverOptions) ([]agentloop.Candidate, error) {
		return candidates, err
	}
	t.Cleanup(func() { loopTailDiscover = old })
}

func stubLoopTailLookPath(t *testing.T, path string, err error) {
	t.Helper()
	old := loopTailLookPath
	loopTailLookPath = func(name string) (string, error) {
		if name != agentSessionLoopBinary {
			t.Fatalf("look path name = %q, want %q", name, agentSessionLoopBinary)
		}
		return path, err
	}
	t.Cleanup(func() { loopTailLookPath = old })
}

func stubLoopTailFallbackPaths(t *testing.T, paths ...string) {
	t.Helper()
	old := loopTailFallbackPaths
	loopTailFallbackPaths = func() []string {
		return append([]string(nil), paths...)
	}
	t.Cleanup(func() { loopTailFallbackPaths = old })
}

func pathListIndex(pathValue, want string) int {
	for i, entry := range filepath.SplitList(pathValue) {
		if entry == want {
			return i
		}
	}
	return -1
}

func pathListCount(pathValue, want string) int {
	count := 0
	for _, entry := range filepath.SplitList(pathValue) {
		if entry == want {
			count++
		}
	}
	return count
}

func runLoopTailSelection(t *testing.T, m Model) loopTailSelectionMsg {
	t.Helper()
	cmd := m.loopTailSelectionCmd()
	if cmd == nil {
		t.Fatal("expected loop tail selection cmd")
	}
	msg, ok := cmd().(loopTailSelectionMsg)
	if !ok {
		t.Fatalf("expected loopTailSelectionMsg, got %T", msg)
	}
	return msg
}

func TestModeListTKeyDispatchesLoopTailSelection(t *testing.T) {
	candidate := agentloop.Candidate{PlanDir: "/plans/api", CWD: "/work/api"}
	stubLoopTailDiscover(t, []agentloop.Candidate{candidate}, nil)

	m := loadSessions(testModel(), []session.RunningSession{{Directory: candidate.CWD}})
	m.actionErr = errors.New("stale action error")

	updated, cmd := m.Update(keyPress("t"))
	got := updated.(Model)
	if got.mode != ModeList {
		t.Fatalf("mode = %v, want ModeList", got.mode)
	}
	if got.actionErr != nil {
		t.Fatalf("actionErr after dispatch = %v, want nil", got.actionErr)
	}
	if cmd == nil {
		t.Fatal("expected loop-tail selection command from ModeList t")
	}

	msg, ok := cmd().(loopTailSelectionMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want loopTailSelectionMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("selection err = %v, want nil", msg.err)
	}
	if msg.selection.Kind != agentloop.SelectionLaunch {
		t.Fatalf("selection kind = %v, want launch", msg.selection.Kind)
	}
	launch, ok := msg.selection.LaunchCandidate()
	if !ok {
		t.Fatal("selection did not contain one launch candidate")
	}
	if launch.PlanDir != candidate.PlanDir {
		t.Fatalf("launch plan = %q, want %q", launch.PlanDir, candidate.PlanDir)
	}
}

func TestModeListTKeyLaunchesUniqueMatchThroughFullUpdatePath(t *testing.T) {
	client := &fakeLoopTailClient{}
	candidate := agentloop.Candidate{PlanDir: "/plans/api", CWD: "/work/api"}
	stubLoopTailDiscover(t, []agentloop.Candidate{candidate}, nil)
	stubLoopTailLookPath(t, "/bin/agent-session-loop", nil)

	m := loadSessions(testModel(), []session.RunningSession{{Directory: candidate.CWD}})
	m.client = client

	updated, cmd := m.Update(keyPress("t"))
	if cmd == nil {
		t.Fatal("ModeList t returned nil cmd; expected discovery")
	}
	selection, ok := cmd().(loopTailSelectionMsg)
	if !ok {
		t.Fatalf("discovery cmd returned %T, want loopTailSelectionMsg", selection)
	}

	updated, cmd = updated.(Model).Update(selection)
	got := updated.(Model)
	if got.mode != ModeList {
		t.Fatalf("mode after launch selection = %v, want ModeList", got.mode)
	}
	if got.actionErr != nil {
		t.Fatalf("actionErr after launch selection = %v, want nil", got.actionErr)
	}
	if cmd == nil {
		t.Fatal("launch selection returned nil cmd; expected overlay launch")
	}

	done, ok := cmd().(loopTailDoneMsg)
	if !ok {
		t.Fatalf("launch cmd returned %T, want loopTailDoneMsg", done)
	}
	if done.err != nil {
		t.Fatalf("launch err = %v, want nil", done.err)
	}
	if len(client.overlays) != 1 {
		t.Fatalf("overlay calls = %d, want 1", len(client.overlays))
	}
	wantCmdline := []string{"/bin/agent-session-loop", "tail", candidate.PlanDir}
	if !reflect.DeepEqual(client.overlays[0].Cmdline, wantCmdline) {
		t.Fatalf("overlay Cmdline = %#v, want %#v", client.overlays[0].Cmdline, wantCmdline)
	}
}

func TestModeListTKeyPickerLaunchesSelectedCandidateThroughFullUpdatePath(t *testing.T) {
	client := &fakeLoopTailClient{}
	candidates := []agentloop.Candidate{
		{PlanDir: "/plans/backend-a", CWD: "/work/backend"},
		{PlanDir: "/plans/backend-b", CWD: "/work/backend"},
	}
	stubLoopTailDiscover(t, candidates, nil)
	stubLoopTailLookPath(t, "/bin/agent-session-loop", nil)

	m := loadSessions(testModel(), []session.RunningSession{{Directory: "/work/backend"}})
	m.client = client

	updated, cmd := m.Update(keyPress("t"))
	if cmd == nil {
		t.Fatal("ModeList t returned nil cmd; expected discovery")
	}
	selection, ok := cmd().(loopTailSelectionMsg)
	if !ok {
		t.Fatalf("discovery cmd returned %T, want loopTailSelectionMsg", selection)
	}

	updated, cmd = updated.(Model).Update(selection)
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("picker selection returned cmd %v, want nil before enter", cmd)
	}
	if got.mode != ModeLoopTailPicker {
		t.Fatalf("mode after picker selection = %v, want ModeLoopTailPicker", got.mode)
	}
	if !reflect.DeepEqual(got.loopTailCandidates, candidates) {
		t.Fatalf("picker candidates = %#v, want %#v", got.loopTailCandidates, candidates)
	}

	updated, _ = got.Update(keyPress("j"))
	got = updated.(Model)
	if got.loopTailCursor != 1 {
		t.Fatalf("cursor after j = %d, want 1", got.loopTailCursor)
	}

	updated, cmd = got.Update(keyPress("enter"))
	got = updated.(Model)
	if got.mode != ModeList {
		t.Fatalf("mode after picker enter = %v, want ModeList", got.mode)
	}
	if len(got.loopTailCandidates) != 0 {
		t.Fatalf("picker candidates after launch = %#v, want empty", got.loopTailCandidates)
	}
	if cmd == nil {
		t.Fatal("picker enter returned nil cmd; expected overlay launch")
	}

	done, ok := cmd().(loopTailDoneMsg)
	if !ok {
		t.Fatalf("launch cmd returned %T, want loopTailDoneMsg", done)
	}
	if done.err != nil {
		t.Fatalf("launch err = %v, want nil", done.err)
	}
	if len(client.overlays) != 1 {
		t.Fatalf("overlay calls = %d, want 1", len(client.overlays))
	}
	wantCmdline := []string{"/bin/agent-session-loop", "tail", "/plans/backend-b"}
	if !reflect.DeepEqual(client.overlays[0].Cmdline, wantCmdline) {
		t.Fatalf("overlay Cmdline = %#v, want %#v", client.overlays[0].Cmdline, wantCmdline)
	}
}

func TestModeListTKeyDispatchesWhilePreviewEnabled(t *testing.T) {
	candidate := agentloop.Candidate{PlanDir: "/plans/api", CWD: "/work/api"}
	stubLoopTailDiscover(t, []agentloop.Candidate{candidate}, nil)

	m := loadSessions(testModel(), []session.RunningSession{{Directory: candidate.CWD}})
	m.previewEnabled = true

	updated, cmd := m.Update(keyPress("t"))
	got := updated.(Model)
	if !got.previewEnabled {
		t.Fatal("previewEnabled should remain true after dispatch")
	}
	if got.mode != ModeList {
		t.Fatalf("mode = %v, want ModeList", got.mode)
	}
	if cmd == nil {
		t.Fatal("expected loop-tail selection command while preview is enabled")
	}
}

func TestModeListTKeyEmptyListNoActiveLoopSetsNonFatalError(t *testing.T) {
	stubLoopTailDiscover(t, nil, nil)

	m := loadSessions(testModel(), nil)
	updated, cmd := m.Update(keyPress("t"))
	got := updated.(Model)
	if got.mode != ModeList {
		t.Fatalf("mode after dispatch = %v, want ModeList", got.mode)
	}
	if cmd == nil {
		t.Fatal("expected loop-tail selection command on empty list")
	}

	msg, ok := cmd().(loopTailSelectionMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want loopTailSelectionMsg", msg)
	}
	updated, cmd = got.Update(msg)
	got = updated.(Model)
	if cmd != nil {
		t.Fatal("no-active-loop selection should not dispatch follow-up command")
	}
	if got.mode != ModeList {
		t.Fatalf("mode after no-active-loop response = %v, want ModeList", got.mode)
	}
	if !errors.Is(got.actionErr, errNoActiveLoop) {
		t.Fatalf("actionErr = %v, want %v", got.actionErr, errNoActiveLoop)
	}
}

func TestModeListTKeySelectedProjectWithoutActiveLoopSetsNonFatalError(t *testing.T) {
	client := &fakeLoopTailClient{}
	candidate := agentloop.Candidate{PlanDir: "/plans/other", CWD: "/work/other"}
	stubLoopTailDiscover(t, []agentloop.Candidate{candidate}, nil)

	m := loadSessions(testModel(), []session.RunningSession{{Directory: "/work/amx"}})
	m.client = client

	updated, cmd := m.Update(keyPress("t"))
	if cmd == nil {
		t.Fatal("ModeList t returned nil cmd; expected discovery")
	}
	selection, ok := cmd().(loopTailSelectionMsg)
	if !ok {
		t.Fatalf("discovery cmd returned %T, want loopTailSelectionMsg", selection)
	}

	updated, cmd = updated.(Model).Update(selection)
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("no matching loop returned cmd %v, want nil", cmd)
	}
	if !errors.Is(got.actionErr, errNoActiveLoop) {
		t.Fatalf("actionErr = %v, want %v", got.actionErr, errNoActiveLoop)
	}
	if len(client.overlays) != 0 {
		t.Fatalf("overlay calls = %d, want 0", len(client.overlays))
	}
}

func TestLoopTailSelectionCmdAppliesSelectionRules(t *testing.T) {
	a := agentloop.Candidate{PlanDir: "/plans/a", CWD: "/work/api"}
	b := agentloop.Candidate{PlanDir: "/plans/b", CWD: "/work/backend"}
	c := agentloop.Candidate{PlanDir: "/plans/c", CWD: "/work/backend"}

	tests := []struct {
		name       string
		selected   string
		candidates []agentloop.Candidate
		wantKind   agentloop.SelectionKind
		wantPlans  []string
	}{
		{
			name:       "unique selected match",
			selected:   "/work/api",
			candidates: []agentloop.Candidate{a, b},
			wantKind:   agentloop.SelectionLaunch,
			wantPlans:  []string{"/plans/a"},
		},
		{
			name:       "multiple selected matches",
			selected:   "/work/backend",
			candidates: []agentloop.Candidate{a, b, c},
			wantKind:   agentloop.SelectionPicker,
			wantPlans:  []string{"/plans/b", "/plans/c"},
		},
		{
			name:       "selected row without matching loop",
			selected:   "/work/missing",
			candidates: []agentloop.Candidate{a},
			wantKind:   agentloop.SelectionNone,
			wantPlans:  []string{},
		},
		{
			name:       "selected row without matching loop ignores multiple globals",
			selected:   "/work/missing",
			candidates: []agentloop.Candidate{a, b},
			wantKind:   agentloop.SelectionNone,
			wantPlans:  []string{},
		},
		{
			name:       "no candidates",
			selected:   "/work/api",
			candidates: nil,
			wantKind:   agentloop.SelectionNone,
			wantPlans:  []string{},
		},
		{
			name:       "empty dashboard list falls back globally",
			candidates: []agentloop.Candidate{a},
			wantKind:   agentloop.SelectionLaunch,
			wantPlans:  []string{"/plans/a"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stubLoopTailDiscover(t, tc.candidates, nil)
			m := testModel()
			if tc.selected != "" {
				m = loadSessions(m, []session.RunningSession{{Directory: tc.selected}})
			}

			msg := runLoopTailSelection(t, m)
			if msg.err != nil {
				t.Fatalf("selection err = %v", msg.err)
			}
			if msg.selection.Kind != tc.wantKind {
				t.Fatalf("selection kind = %v, want %v", msg.selection.Kind, tc.wantKind)
			}
			gotPlans := make([]string, 0, len(msg.selection.Candidates))
			for _, candidate := range msg.selection.Candidates {
				gotPlans = append(gotPlans, candidate.PlanDir)
			}
			if !reflect.DeepEqual(gotPlans, tc.wantPlans) {
				t.Fatalf("selected plans = %#v, want %#v", gotPlans, tc.wantPlans)
			}
		})
	}
}

func TestLoopTailSelectionCmdSurfacesDiscoveryError(t *testing.T) {
	discoveryErr := errors.New("state root unreadable")
	stubLoopTailDiscover(t, nil, discoveryErr)

	msg := runLoopTailSelection(t, testModel())
	if !errors.Is(msg.err, discoveryErr) {
		t.Fatalf("selection err = %v, want %v", msg.err, discoveryErr)
	}
}

func TestHandleLoopTailSelectionPickerStoresCandidates(t *testing.T) {
	candidates := []agentloop.Candidate{
		{PlanDir: "/plans/a", CWD: "/work/a"},
		{PlanDir: "/plans/b", CWD: "/work/b"},
	}

	updated, cmd := testModel().Update(loopTailSelectionMsg{
		selection: agentloop.Selection{
			Kind:       agentloop.SelectionPicker,
			Candidates: candidates,
		},
	})
	m := updated.(Model)
	if cmd != nil {
		t.Fatal("picker selection should not launch immediately")
	}
	if m.mode != ModeLoopTailPicker {
		t.Fatalf("mode = %v, want ModeLoopTailPicker", m.mode)
	}
	if !reflect.DeepEqual(m.loopTailCandidates, candidates) {
		t.Fatalf("loopTailCandidates = %#v, want %#v", m.loopTailCandidates, candidates)
	}
	if m.loopTailCursor != 0 {
		t.Fatalf("loopTailCursor = %d, want 0", m.loopTailCursor)
	}
	if m.actionErr != nil {
		t.Fatalf("actionErr = %v, want nil", m.actionErr)
	}
}

func TestHandleLoopTailSelectionNoneSetsNonFatalError(t *testing.T) {
	updated, cmd := testModel().Update(loopTailSelectionMsg{
		selection: agentloop.Selection{Kind: agentloop.SelectionNone},
	})
	m := updated.(Model)
	if cmd != nil {
		t.Fatal("no-candidate selection should not dispatch a command")
	}
	if !errors.Is(m.actionErr, errNoActiveLoop) {
		t.Fatalf("actionErr = %v, want %v", m.actionErr, errNoActiveLoop)
	}
}

func TestHandleLoopTailSelectionLaunchesOverlayWithDirectArgv(t *testing.T) {
	client := &fakeLoopTailClient{}
	stubLoopTailLookPath(t, "/usr/local/bin/agent-session-loop", nil)

	planDir := "/plans/api"
	candidate := agentloop.Candidate{
		PlanDir: planDir,
		CWD:     "/work/api",
	}
	m := testModel()
	m.client = client

	updated, cmd := m.Update(loopTailSelectionMsg{
		selection: agentloop.Selection{
			Kind:       agentloop.SelectionLaunch,
			Candidates: []agentloop.Candidate{candidate},
		},
	})
	if updated.(Model).actionErr != nil {
		t.Fatalf("actionErr before launch = %v, want nil", updated.(Model).actionErr)
	}
	if cmd == nil {
		t.Fatal("expected launch command")
	}
	done, ok := cmd().(loopTailDoneMsg)
	if !ok {
		t.Fatalf("expected loopTailDoneMsg, got %T", done)
	}
	if done.err != nil {
		t.Fatalf("launch err = %v, want nil", done.err)
	}
	if len(client.overlays) != 1 {
		t.Fatalf("overlay calls = %d, want 1", len(client.overlays))
	}

	got := client.overlays[0]
	wantCmdline := []string{"/usr/local/bin/agent-session-loop", "tail", planDir}
	if !reflect.DeepEqual(got.Cmdline, wantCmdline) {
		t.Fatalf("Cmdline = %#v, want %#v", got.Cmdline, wantCmdline)
	}
	if got.Directory != candidate.CWD {
		t.Fatalf("Directory = %q, want cwd %q", got.Directory, candidate.CWD)
	}
	if !got.Hold {
		t.Fatal("expected Hold=true")
	}
	if got.Title != "tail: api" {
		t.Fatalf("Title = %q, want tail: api", got.Title)
	}
}

func TestLoopTailOverlayPathPrependsEnvPythonDirs(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	localBin := filepath.Join(home, ".local", "bin")
	currentPath := strings.Join([]string{"/usr/bin", "/bin", "/opt/homebrew/bin"}, string(os.PathListSeparator))

	got := loopTailOverlayPath(filepath.Join(localBin, agentSessionLoopBinary), currentPath)
	if idx := pathListIndex(got, localBin); idx != 0 {
		t.Fatalf("local bin index = %d, want 0 in PATH %q", idx, got)
	}
	homebrewIdx := pathListIndex(got, "/opt/homebrew/bin")
	usrIdx := pathListIndex(got, "/usr/bin")
	if homebrewIdx < 0 || usrIdx < 0 || homebrewIdx >= usrIdx {
		t.Fatalf("expected /opt/homebrew/bin before /usr/bin in PATH %q", got)
	}
	if count := pathListCount(got, "/opt/homebrew/bin"); count != 1 {
		t.Fatalf("/opt/homebrew/bin count = %d, want 1 in PATH %q", count, got)
	}

	opts := loopTailOverlayOpts(filepath.Join(localBin, agentSessionLoopBinary), agentloop.Candidate{PlanDir: "/plans/api"})
	if opts.Env["PATH"] == "" {
		t.Fatal("overlay opts should set PATH for env-based shebangs")
	}
}

func TestLoopTailLaunchFallsBackToPlanDirForOverlayCWD(t *testing.T) {
	client := &fakeLoopTailClient{}
	stubLoopTailLookPath(t, "/bin/agent-session-loop", nil)

	candidate := agentloop.Candidate{PlanDir: "/plans/no-cwd"}
	done := loopTailLaunchCmd(client, candidate)().(loopTailDoneMsg)
	if done.err != nil {
		t.Fatalf("launch err = %v, want nil", done.err)
	}
	if len(client.overlays) != 1 {
		t.Fatalf("overlay calls = %d, want 1", len(client.overlays))
	}
	if client.overlays[0].Directory != candidate.PlanDir {
		t.Fatalf("Directory = %q, want plan dir %q", client.overlays[0].Directory, candidate.PlanDir)
	}
}

func TestLoopTailLaunchUsesFallbackBinaryWhenPathMisses(t *testing.T) {
	client := &fakeLoopTailClient{}
	stubLoopTailLookPath(t, "", exec.ErrNotFound)

	fallback := filepath.Join(t.TempDir(), agentSessionLoopBinary)
	if err := os.WriteFile(fallback, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fallback binary: %v", err)
	}
	stubLoopTailFallbackPaths(t, fallback)

	candidate := agentloop.Candidate{PlanDir: "/plans/api", CWD: "/work/api"}
	done := loopTailLaunchCmd(client, candidate)().(loopTailDoneMsg)
	if done.err != nil {
		t.Fatalf("launch err = %v, want nil", done.err)
	}
	if len(client.overlays) != 1 {
		t.Fatalf("overlay calls = %d, want 1", len(client.overlays))
	}
	wantCmdline := []string{fallback, "tail", candidate.PlanDir}
	if !reflect.DeepEqual(client.overlays[0].Cmdline, wantCmdline) {
		t.Fatalf("overlay Cmdline = %#v, want %#v", client.overlays[0].Cmdline, wantCmdline)
	}
}

func TestLoopTailLaunchMissingBinaryDoesNotCallKitty(t *testing.T) {
	client := &fakeLoopTailClient{}
	stubLoopTailLookPath(t, "", exec.ErrNotFound)
	stubLoopTailFallbackPaths(t, filepath.Join(t.TempDir(), agentSessionLoopBinary))

	done := loopTailLaunchCmd(client, agentloop.Candidate{PlanDir: "/plans/api"})().(loopTailDoneMsg)
	if !errors.Is(done.err, exec.ErrNotFound) {
		t.Fatalf("launch err = %v, want exec.ErrNotFound", done.err)
	}
	if len(client.overlays) != 0 {
		t.Fatalf("overlay calls = %d, want 0", len(client.overlays))
	}
}

func TestLoopTailLaunchNonExecutableFallbackDoesNotCallKitty(t *testing.T) {
	client := &fakeLoopTailClient{}
	stubLoopTailLookPath(t, "", exec.ErrNotFound)

	fallback := filepath.Join(t.TempDir(), agentSessionLoopBinary)
	if err := os.WriteFile(fallback, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write fallback binary: %v", err)
	}
	stubLoopTailFallbackPaths(t, fallback)

	done := loopTailLaunchCmd(client, agentloop.Candidate{PlanDir: "/plans/api"})().(loopTailDoneMsg)
	if !errors.Is(done.err, exec.ErrNotFound) {
		t.Fatalf("launch err = %v, want exec.ErrNotFound", done.err)
	}
	if !strings.Contains(done.err.Error(), "fallback locations") {
		t.Fatalf("launch err = %q, want fallback locations context", done.err)
	}
	if len(client.overlays) != 0 {
		t.Fatalf("overlay calls = %d, want 0", len(client.overlays))
	}
}

func TestLoopTailDoneMsgSetsActionErr(t *testing.T) {
	launchErr := errors.New("kitty launch failed")
	updated, _ := testModel().Update(loopTailDoneMsg{err: launchErr})
	if !errors.Is(updated.(Model).actionErr, launchErr) {
		t.Fatalf("actionErr = %v, want %v", updated.(Model).actionErr, launchErr)
	}
}
