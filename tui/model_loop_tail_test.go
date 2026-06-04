package tui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

type fakeLoopTailKittyClient struct {
	overlays []kitty.LaunchOpts
}

func (f *fakeLoopTailKittyClient) ListWindows() ([]kitty.OSWindow, error) { return nil, nil }
func (f *fakeLoopTailKittyClient) LaunchTab(kitty.LaunchOpts) error       { return nil }
func (f *fakeLoopTailKittyClient) LaunchOverlay(opts kitty.LaunchOpts) error {
	f.overlays = append(f.overlays, opts)
	return nil
}
func (f *fakeLoopTailKittyClient) CloseTab(string) error { return nil }
func (f *fakeLoopTailKittyClient) MoveTab(string, int) error {
	return nil
}
func (f *fakeLoopTailKittyClient) SetTabColor(string, string, string, string, string) error {
	return nil
}
func (f *fakeLoopTailKittyClient) SetTabTitle(string, string) error            { return nil }
func (f *fakeLoopTailKittyClient) SetUserVars(string, map[string]string) error { return nil }
func (f *fakeLoopTailKittyClient) FocusTab(string) error                       { return nil }
func (f *fakeLoopTailKittyClient) FocusWindow(string) error                    { return nil }
func (f *fakeLoopTailKittyClient) GetText(string, string) (string, error)      { return "", nil }
func (f *fakeLoopTailKittyClient) LaunchOverlayPager(string) error             { return nil }

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

func stubLoopTailExecProcess(t *testing.T, runErr error, calls *[]*exec.Cmd) {
	t.Helper()
	old := loopTailExecProcess
	loopTailExecProcess = func(cmd *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
		*calls = append(*calls, cmd)
		return func() tea.Msg {
			if fn == nil {
				return nil
			}
			return fn(runErr)
		}
	}
	t.Cleanup(func() { loopTailExecProcess = old })
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

func envValue(environ []string, key string) (string, bool) {
	prefix := key + "="
	for _, entry := range environ {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix), true
		}
	}
	return "", false
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
	candidate := agentloop.Candidate{PlanDir: "/plans/api", CWD: "/work/api"}
	stubLoopTailDiscover(t, []agentloop.Candidate{candidate}, nil)
	stubLoopTailLookPath(t, "/bin/agent-session-loop", nil)
	var execCalls []*exec.Cmd
	stubLoopTailExecProcess(t, nil, &execCalls)
	client := &fakeLoopTailKittyClient{}

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
		t.Fatal("launch selection returned nil cmd; expected exec command")
	}

	done, ok := cmd().(loopTailDoneMsg)
	if !ok {
		t.Fatalf("launch cmd returned %T, want loopTailDoneMsg", done)
	}
	if done.err != nil {
		t.Fatalf("launch err = %v, want nil", done.err)
	}
	if len(execCalls) != 1 {
		t.Fatalf("exec calls = %d, want 1", len(execCalls))
	}
	wantCmdline := []string{"/bin/agent-session-loop", "tail", candidate.PlanDir}
	if !reflect.DeepEqual(execCalls[0].Args, wantCmdline) {
		t.Fatalf("exec Args = %#v, want %#v", execCalls[0].Args, wantCmdline)
	}
	if execCalls[0].Dir != candidate.CWD {
		t.Fatalf("exec Dir = %q, want cwd %q", execCalls[0].Dir, candidate.CWD)
	}
	if len(client.overlays) != 0 {
		t.Fatalf("kitty overlay calls = %d, want 0", len(client.overlays))
	}
}

func TestModeListTKeyPickerLaunchesSelectedCandidateThroughFullUpdatePath(t *testing.T) {
	candidates := []agentloop.Candidate{
		{PlanDir: "/plans/backend-a", CWD: "/work/backend"},
		{PlanDir: "/plans/backend-b", CWD: "/work/backend"},
	}
	stubLoopTailDiscover(t, candidates, nil)
	stubLoopTailLookPath(t, "/bin/agent-session-loop", nil)
	var execCalls []*exec.Cmd
	stubLoopTailExecProcess(t, nil, &execCalls)
	client := &fakeLoopTailKittyClient{}

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
		t.Fatal("picker enter returned nil cmd; expected exec command")
	}

	done, ok := cmd().(loopTailDoneMsg)
	if !ok {
		t.Fatalf("launch cmd returned %T, want loopTailDoneMsg", done)
	}
	if done.err != nil {
		t.Fatalf("launch err = %v, want nil", done.err)
	}
	if len(execCalls) != 1 {
		t.Fatalf("exec calls = %d, want 1", len(execCalls))
	}
	wantCmdline := []string{"/bin/agent-session-loop", "tail", "/plans/backend-b"}
	if !reflect.DeepEqual(execCalls[0].Args, wantCmdline) {
		t.Fatalf("exec Args = %#v, want %#v", execCalls[0].Args, wantCmdline)
	}
	if len(client.overlays) != 0 {
		t.Fatalf("kitty overlay calls = %d, want 0", len(client.overlays))
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
	candidate := agentloop.Candidate{PlanDir: "/plans/other", CWD: "/work/other"}
	stubLoopTailDiscover(t, []agentloop.Candidate{candidate}, nil)
	var execCalls []*exec.Cmd
	stubLoopTailExecProcess(t, nil, &execCalls)

	m := loadSessions(testModel(), []session.RunningSession{{Directory: "/work/amx"}})

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
	if len(execCalls) != 0 {
		t.Fatalf("exec calls = %d, want 0", len(execCalls))
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

func TestHandleLoopTailSelectionBuildsExecCommandWithDirectArgv(t *testing.T) {
	t.Setenv("AGENTMUX_LOOP_TAIL_KEEP", "1")
	stubLoopTailLookPath(t, "/usr/local/bin/agent-session-loop", nil)
	var execCalls []*exec.Cmd
	stubLoopTailExecProcess(t, nil, &execCalls)

	planDir := "/plans/api"
	candidate := agentloop.Candidate{
		PlanDir: planDir,
		CWD:     "/work/api",
	}
	m := testModel()

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
	if len(execCalls) != 1 {
		t.Fatalf("exec calls = %d, want 1", len(execCalls))
	}

	got := execCalls[0]
	wantCmdline := []string{"/usr/local/bin/agent-session-loop", "tail", planDir}
	if !reflect.DeepEqual(got.Args, wantCmdline) {
		t.Fatalf("Args = %#v, want %#v", got.Args, wantCmdline)
	}
	if got.Dir != candidate.CWD {
		t.Fatalf("Dir = %q, want cwd %q", got.Dir, candidate.CWD)
	}
	if len(got.Env) == 0 {
		t.Fatal("expected exec env")
	}
	if env, ok := envValue(got.Env, "AGENTMUX_LOOP_TAIL_KEEP"); !ok || env != "1" {
		t.Fatalf("exec env should preserve current environment, got %#v", got.Env)
	}
}

func TestLoopTailExecPathPrependsEnvPythonDirs(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	localBin := filepath.Join(home, ".local", "bin")
	currentPath := strings.Join([]string{"/usr/bin", "/bin", "/opt/homebrew/bin"}, string(os.PathListSeparator))

	got := loopTailExecPath(filepath.Join(localBin, agentSessionLoopBinary), currentPath)
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

	env := loopTailExecEnv(filepath.Join(localBin, agentSessionLoopBinary), []string{"FOO=bar"})
	if pathListIndex(strings.TrimPrefix(env[len(env)-1], "PATH="), localBin) != 0 {
		t.Fatalf("exec env should prepend local bin PATH, got %#v", env)
	}
}

func TestLoopTailLaunchFallsBackToPlanDirForExecCWD(t *testing.T) {
	stubLoopTailLookPath(t, "/bin/agent-session-loop", nil)
	var execCalls []*exec.Cmd
	stubLoopTailExecProcess(t, nil, &execCalls)

	candidate := agentloop.Candidate{PlanDir: "/plans/no-cwd"}
	done := loopTailLaunchCmd(candidate)().(loopTailDoneMsg)
	if done.err != nil {
		t.Fatalf("launch err = %v, want nil", done.err)
	}
	if len(execCalls) != 1 {
		t.Fatalf("exec calls = %d, want 1", len(execCalls))
	}
	if execCalls[0].Dir != candidate.PlanDir {
		t.Fatalf("Dir = %q, want plan dir %q", execCalls[0].Dir, candidate.PlanDir)
	}
}

func TestLoopTailLaunchUsesFallbackBinaryWhenPathMisses(t *testing.T) {
	stubLoopTailLookPath(t, "", exec.ErrNotFound)
	var execCalls []*exec.Cmd
	stubLoopTailExecProcess(t, nil, &execCalls)

	fallback := filepath.Join(t.TempDir(), agentSessionLoopBinary)
	if err := os.WriteFile(fallback, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write fallback binary: %v", err)
	}
	stubLoopTailFallbackPaths(t, fallback)

	candidate := agentloop.Candidate{PlanDir: "/plans/api", CWD: "/work/api"}
	done := loopTailLaunchCmd(candidate)().(loopTailDoneMsg)
	if done.err != nil {
		t.Fatalf("launch err = %v, want nil", done.err)
	}
	if len(execCalls) != 1 {
		t.Fatalf("exec calls = %d, want 1", len(execCalls))
	}
	wantCmdline := []string{fallback, "tail", candidate.PlanDir}
	if !reflect.DeepEqual(execCalls[0].Args, wantCmdline) {
		t.Fatalf("exec Args = %#v, want %#v", execCalls[0].Args, wantCmdline)
	}
}

func TestLoopTailLaunchMissingBinaryDoesNotExec(t *testing.T) {
	stubLoopTailLookPath(t, "", exec.ErrNotFound)
	stubLoopTailFallbackPaths(t, filepath.Join(t.TempDir(), agentSessionLoopBinary))
	var execCalls []*exec.Cmd
	stubLoopTailExecProcess(t, nil, &execCalls)

	done := loopTailLaunchCmd(agentloop.Candidate{PlanDir: "/plans/api"})().(loopTailDoneMsg)
	if !errors.Is(done.err, exec.ErrNotFound) {
		t.Fatalf("launch err = %v, want exec.ErrNotFound", done.err)
	}
	if len(execCalls) != 0 {
		t.Fatalf("exec calls = %d, want 0", len(execCalls))
	}
}

func TestLoopTailLaunchMissingPlanDirDoesNotExec(t *testing.T) {
	var execCalls []*exec.Cmd
	stubLoopTailExecProcess(t, nil, &execCalls)

	done := loopTailLaunchCmd(agentloop.Candidate{CWD: "/work/api"})().(loopTailDoneMsg)
	if done.err == nil {
		t.Fatal("launch err = nil, want missing plan directory error")
	}
	if !strings.Contains(done.err.Error(), "missing plan directory") {
		t.Fatalf("launch err = %q, want missing plan directory context", done.err)
	}
	if len(execCalls) != 0 {
		t.Fatalf("exec calls = %d, want 0", len(execCalls))
	}
}

func TestLoopTailLaunchNonExecutableFallbackDoesNotExec(t *testing.T) {
	stubLoopTailLookPath(t, "", exec.ErrNotFound)
	var execCalls []*exec.Cmd
	stubLoopTailExecProcess(t, nil, &execCalls)

	fallback := filepath.Join(t.TempDir(), agentSessionLoopBinary)
	if err := os.WriteFile(fallback, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write fallback binary: %v", err)
	}
	stubLoopTailFallbackPaths(t, fallback)

	done := loopTailLaunchCmd(agentloop.Candidate{PlanDir: "/plans/api"})().(loopTailDoneMsg)
	if !errors.Is(done.err, exec.ErrNotFound) {
		t.Fatalf("launch err = %v, want exec.ErrNotFound", done.err)
	}
	if !strings.Contains(done.err.Error(), "fallback locations") {
		t.Fatalf("launch err = %q, want fallback locations context", done.err)
	}
	if len(execCalls) != 0 {
		t.Fatalf("exec calls = %d, want 0", len(execCalls))
	}
}

func TestLoopTailExecErrorResumesDashboardWithActionErr(t *testing.T) {
	stubLoopTailLookPath(t, "/bin/agent-session-loop", nil)
	tailErr := errors.New("signal: interrupt")
	var execCalls []*exec.Cmd
	stubLoopTailExecProcess(t, tailErr, &execCalls)

	updated, cmd := testModel().Update(loopTailSelectionMsg{
		selection: agentloop.Selection{
			Kind: agentloop.SelectionLaunch,
			Candidates: []agentloop.Candidate{{
				PlanDir: "/plans/api",
				CWD:     "/work/api",
			}},
		},
	})
	got := updated.(Model)
	if got.mode != ModeList {
		t.Fatalf("mode before tail exits = %v, want ModeList", got.mode)
	}
	if cmd == nil {
		t.Fatal("expected launch command")
	}

	done, ok := cmd().(loopTailDoneMsg)
	if !ok {
		t.Fatalf("launch cmd returned %T, want loopTailDoneMsg", done)
	}
	if !errors.Is(done.err, tailErr) {
		t.Fatalf("tail err = %v, want %v", done.err, tailErr)
	}

	updated, cmd = got.Update(done)
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tail completion returned cmd %v, want nil", cmd)
	}
	if got.mode != ModeList {
		t.Fatalf("mode after tail exits = %v, want ModeList", got.mode)
	}
	if !errors.Is(got.actionErr, tailErr) {
		t.Fatalf("actionErr = %v, want %v", got.actionErr, tailErr)
	}
}
