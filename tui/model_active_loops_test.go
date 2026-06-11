package tui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
	"github.com/sglavoie/agentmux/internal/session"
)

type activeLoopTitleSetCall struct {
	match string
	title string
}

type fakeActiveLoopTitleSyncClient struct {
	windows   []kitty.OSWindow
	listErr   error
	setErr    error
	listCalls int
	setCalls  []activeLoopTitleSetCall
}

func (f *fakeActiveLoopTitleSyncClient) ListWindows() ([]kitty.OSWindow, error) {
	f.listCalls++
	return f.windows, f.listErr
}

func (f *fakeActiveLoopTitleSyncClient) LaunchTab(kitty.LaunchOpts) error     { return nil }
func (f *fakeActiveLoopTitleSyncClient) LaunchOverlay(kitty.LaunchOpts) error { return nil }
func (f *fakeActiveLoopTitleSyncClient) CloseTab(string) error                { return nil }
func (f *fakeActiveLoopTitleSyncClient) MoveTab(string, int) error            { return nil }
func (f *fakeActiveLoopTitleSyncClient) SetTabColor(string, string, string, string, string) error {
	return nil
}
func (f *fakeActiveLoopTitleSyncClient) SetTabTitle(match, title string) error {
	f.setCalls = append(f.setCalls, activeLoopTitleSetCall{match: match, title: title})
	return f.setErr
}
func (f *fakeActiveLoopTitleSyncClient) SetUserVars(string, map[string]string) error { return nil }
func (f *fakeActiveLoopTitleSyncClient) FocusTab(string) error                       { return nil }
func (f *fakeActiveLoopTitleSyncClient) FocusWindow(string) error                    { return nil }
func (f *fakeActiveLoopTitleSyncClient) GetText(string, string) (string, error)      { return "", nil }
func (f *fakeActiveLoopTitleSyncClient) LaunchOverlayPager(string) error             { return nil }

func topLevelBatchCmds(t *testing.T, cmd tea.Cmd) []tea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected batch command")
	}
	msg := cmd()
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice {
		t.Fatalf("expected batch message slice, got %T", msg)
	}
	cmds := make([]tea.Cmd, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		c, ok := v.Index(i).Interface().(tea.Cmd)
		if !ok || c == nil {
			t.Fatalf("batch element %d = %T, want tea.Cmd", i, v.Index(i).Interface())
		}
		cmds = append(cmds, c)
	}
	return cmds
}

func TestInitSchedulesActiveLoopRefresh(t *testing.T) {
	candidate := agentloop.Candidate{PlanDir: "/plans/api", CWD: "/work/api"}
	stubLoopTailDiscover(t, []agentloop.Candidate{candidate}, nil)

	m := testModel()
	cmds := topLevelBatchCmds(t, m.Init())
	if len(cmds) < 2 {
		t.Fatalf("init batch command count = %d, want at least 2", len(cmds))
	}
	msg, ok := cmds[1]().(activeLoopsMsg)
	if !ok {
		t.Fatalf("init command produced %T, want activeLoopsMsg", msg)
	}
	if msg.seq != m.activeLoopRefreshSeq {
		t.Fatalf("active loop seq = %d, want %d", msg.seq, m.activeLoopRefreshSeq)
	}

	updated, _ := m.Update(msg)
	got := updated.(Model)
	if !reflect.DeepEqual(got.activeLoopCandidates, []agentloop.Candidate{candidate}) {
		t.Fatalf("active loops = %#v, want candidate", got.activeLoopCandidates)
	}
}

func TestTickRefreshesActiveLoops(t *testing.T) {
	candidate := agentloop.Candidate{PlanDir: "/plans/backend", CWD: "/work/backend"}
	stubLoopTailDiscover(t, []agentloop.Candidate{candidate}, nil)

	m := testModel()
	m.activeLoopRefreshSeq = 7
	updated, cmd := m.Update(tickMsg{})
	got := updated.(Model)
	if got.activeLoopRefreshSeq != 8 {
		t.Fatalf("activeLoopRefreshSeq = %d, want 8", got.activeLoopRefreshSeq)
	}

	cmds := topLevelBatchCmds(t, cmd)
	if len(cmds) < 2 {
		t.Fatalf("tick batch command count = %d, want at least 2", len(cmds))
	}
	msg, ok := cmds[1]().(activeLoopsMsg)
	if !ok {
		t.Fatalf("tick command produced %T, want activeLoopsMsg", msg)
	}
	if msg.seq != got.activeLoopRefreshSeq {
		t.Fatalf("active loop seq = %d, want %d", msg.seq, got.activeLoopRefreshSeq)
	}

	updated, _ = got.Update(msg)
	got = updated.(Model)
	if !reflect.DeepEqual(got.activeLoopCandidates, []agentloop.Candidate{candidate}) {
		t.Fatalf("active loops = %#v, want candidate", got.activeLoopCandidates)
	}
}

func TestActiveLoopRefreshClearsStaleCandidates(t *testing.T) {
	stale := agentloop.Candidate{PlanDir: "/plans/stale", CWD: "/work/stale"}
	m := testModel()
	m.activeLoopRefreshSeq = 4
	m.activeLoopCandidates = []agentloop.Candidate{stale}

	updated, _ := m.Update(activeLoopsMsg{seq: 4})
	got := updated.(Model)
	if len(got.activeLoopCandidates) != 0 {
		t.Fatalf("active loops = %#v, want empty after refresh", got.activeLoopCandidates)
	}
}

func TestActiveLoopRefreshDropsStaleSequence(t *testing.T) {
	current := agentloop.Candidate{PlanDir: "/plans/current", CWD: "/work/current"}
	older := agentloop.Candidate{PlanDir: "/plans/older", CWD: "/work/older"}
	m := loadSessions(testModel(), []session.RunningSession{{WindowID: 101, Directory: current.CWD}})
	m.activeLoopRefreshSeq = 6
	m.activeLoopCandidates = []agentloop.Candidate{current}

	updated, cmd := m.Update(activeLoopsMsg{
		seq:        5,
		candidates: []agentloop.Candidate{older},
	})
	got := updated.(Model)
	if !reflect.DeepEqual(got.activeLoopCandidates, []agentloop.Candidate{current}) {
		t.Fatalf("active loops = %#v, want current candidate preserved", got.activeLoopCandidates)
	}
	if cmd != nil {
		t.Fatal("stale active loop refresh dispatched title sync command")
	}
}

func TestActiveLoopsForSelectedRowMatchesCanonicalCWD(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "project")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("mkdir real dir: %v", err)
	}
	linkDir := filepath.Join(root, "project-link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	match := agentloop.Candidate{PlanDir: "/plans/match", CWD: realDir}
	other := agentloop.Candidate{PlanDir: "/plans/other", CWD: filepath.Join(root, "other")}
	m := loadSessions(testModel(), []session.RunningSession{{Directory: linkDir}})
	m.activeLoopCandidates = []agentloop.Candidate{other, match}

	got := m.activeLoopsForSelectedRow()
	if !reflect.DeepEqual(got, []agentloop.Candidate{match}) {
		t.Fatalf("selected-row active loops = %#v, want match", got)
	}
}

func TestActiveLoopRefreshSyncsKittyTabLoopMarkers(t *testing.T) {
	root := t.TempDir()
	apiDir := mkdirTestDir(t, root, "api")
	backendDir := mkdirTestDir(t, root, "backend")
	workerDir := mkdirTestDir(t, root, "worker")
	apiLink := filepath.Join(root, "api-link")
	if err := os.Symlink(apiDir, apiLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	apiCurrent := palette.BuildTitleParts(palette.TitleParts{
		Base:    "api",
		State:   "bolt",
		Starred: true,
		Muted:   true,
	})
	backendCurrent := palette.BuildTitleParts(palette.TitleParts{
		Base:    "backend",
		Muted:   true,
		Looping: true,
	})
	workerCurrent := palette.BuildTitleParts(palette.TitleParts{
		Base:    "worker",
		State:   "key",
		Looping: true,
	})
	client := &fakeActiveLoopTitleSyncClient{
		windows: []kitty.OSWindow{{
			Tabs: []kitty.Tab{
				{ID: 10, Title: apiCurrent, Windows: []kitty.Window{{ID: 101}}},
				{ID: 20, Title: backendCurrent, Windows: []kitty.Window{{ID: 202}}},
				{ID: 30, Title: workerCurrent, Windows: []kitty.Window{{ID: 303}}},
			},
		}},
	}

	m := testModel()
	m.client = client
	m.activeLoopRefreshSeq = 9
	m = loadSessions(m, []session.RunningSession{
		{WindowID: 101, Base: "api", Directory: apiLink},
		{WindowID: 202, Base: "backend", Directory: backendDir},
		{WindowID: 303, Base: "worker", Directory: workerDir},
	})

	candidates := []agentloop.Candidate{
		{PlanDir: "/plans/api", CWD: apiDir},
		{PlanDir: "/plans/worker", CWD: workerDir},
	}
	updated, cmd := m.Update(activeLoopsMsg{seq: 9, candidates: candidates})
	got := updated.(Model)
	if !reflect.DeepEqual(got.activeLoopCandidates, candidates) {
		t.Fatalf("active loops = %#v, want refreshed candidates", got.activeLoopCandidates)
	}
	if cmd == nil {
		t.Fatal("accepted active loop refresh did not dispatch title sync")
	}
	msg, ok := cmd().(activeLoopTitleSyncMsg)
	if !ok {
		t.Fatalf("title sync cmd returned %T, want activeLoopTitleSyncMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("title sync err = %v, want nil", msg.err)
	}
	if client.listCalls != 1 {
		t.Fatalf("ListWindows calls = %d, want 1", client.listCalls)
	}

	wantCalls := []activeLoopTitleSetCall{
		{
			match: kitty.MatchTabByID(10),
			title: palette.BuildTitleParts(palette.TitleParts{
				Base:    "api",
				State:   "bolt",
				Starred: true,
				Muted:   true,
				Looping: true,
			}),
		},
		{
			match: kitty.MatchTabByID(20),
			title: palette.BuildTitleParts(palette.TitleParts{
				Base:  "backend",
				Muted: true,
			}),
		},
	}
	if !reflect.DeepEqual(client.setCalls, wantCalls) {
		t.Fatalf("SetTabTitle calls = %#v, want %#v", client.setCalls, wantCalls)
	}
}

func TestActiveLoopRefreshEmptyRefreshRemovesKittyTabLoopMarkers(t *testing.T) {
	root := t.TempDir()
	apiDir := mkdirTestDir(t, root, "api")
	apiCurrent := palette.BuildTitleParts(palette.TitleParts{
		Base:    "api",
		State:   "key",
		Starred: true,
		Muted:   true,
		Looping: true,
	})
	client := &fakeActiveLoopTitleSyncClient{
		windows: []kitty.OSWindow{{
			Tabs: []kitty.Tab{{
				ID:      10,
				Title:   apiCurrent,
				Windows: []kitty.Window{{ID: 101}},
			}},
		}},
	}

	m := testModel()
	m.client = client
	m.activeLoopRefreshSeq = 10
	m.activeLoopCandidates = []agentloop.Candidate{{PlanDir: "/plans/api", CWD: apiDir}}
	m = loadSessions(m, []session.RunningSession{{
		WindowID:  101,
		Base:      "api",
		Title:     "stale cached title",
		Directory: apiDir,
	}})

	updated, cmd := m.Update(activeLoopsMsg{seq: 10})
	got := updated.(Model)
	if len(got.activeLoopCandidates) != 0 {
		t.Fatalf("active loops = %#v, want empty after refresh", got.activeLoopCandidates)
	}
	if cmd == nil {
		t.Fatal("accepted empty active loop refresh did not dispatch title sync")
	}
	msg, ok := cmd().(activeLoopTitleSyncMsg)
	if !ok {
		t.Fatalf("title sync cmd returned %T, want activeLoopTitleSyncMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("title sync err = %v, want nil", msg.err)
	}

	wantCalls := []activeLoopTitleSetCall{{
		match: kitty.MatchTabByID(10),
		title: palette.BuildTitleParts(palette.TitleParts{
			Base:    "api",
			State:   "key",
			Starred: true,
			Muted:   true,
		}),
	}}
	if !reflect.DeepEqual(client.setCalls, wantCalls) {
		t.Fatalf("SetTabTitle calls = %#v, want %#v", client.setCalls, wantCalls)
	}
}

func TestActiveLoopTitleSyncLeavesNonmatchingProjectsUntouched(t *testing.T) {
	root := t.TempDir()
	apiDir := mkdirTestDir(t, root, "api")
	otherDir := mkdirTestDir(t, root, "other")
	client := &fakeActiveLoopTitleSyncClient{
		windows: []kitty.OSWindow{{
			Tabs: []kitty.Tab{
				{ID: 10, Title: "api", Windows: []kitty.Window{{ID: 101}}},
				{ID: 20, Title: "other", Windows: []kitty.Window{{ID: 202}}},
			},
		}},
	}

	m := testModel()
	m.client = client
	m.activeLoopRefreshSeq = 11
	m = loadSessions(m, []session.RunningSession{{WindowID: 101, Base: "api", Directory: apiDir}})

	updated, cmd := m.Update(activeLoopsMsg{
		seq:        11,
		candidates: []agentloop.Candidate{{PlanDir: "/plans/other", CWD: otherDir}},
	})
	got := updated.(Model)
	if len(got.activeLoopCandidates) != 1 {
		t.Fatalf("active loops = %#v, want accepted nonmatching candidate", got.activeLoopCandidates)
	}
	if cmd == nil {
		t.Fatal("accepted active loop refresh did not dispatch title sync")
	}
	msg, ok := cmd().(activeLoopTitleSyncMsg)
	if !ok {
		t.Fatalf("title sync cmd returned %T, want activeLoopTitleSyncMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("title sync err = %v, want nil", msg.err)
	}
	if len(client.setCalls) != 0 {
		t.Fatalf("SetTabTitle calls = %#v, want none for nonmatching projects", client.setCalls)
	}
}

func TestActiveLoopTitleSyncErrorIsNonFatal(t *testing.T) {
	root := t.TempDir()
	apiDir := mkdirTestDir(t, root, "api")
	setErr := errors.New("kitty refused title")
	client := &fakeActiveLoopTitleSyncClient{
		setErr: setErr,
		windows: []kitty.OSWindow{{
			Tabs: []kitty.Tab{{
				ID:      10,
				Title:   "api",
				Windows: []kitty.Window{{ID: 101}},
			}},
		}},
	}

	m := testModel()
	m.client = client
	m.activeLoopRefreshSeq = 3
	m = loadSessions(m, []session.RunningSession{{WindowID: 101, Base: "api", Directory: apiDir}})
	candidates := []agentloop.Candidate{{PlanDir: "/plans/api", CWD: apiDir}}

	updated, cmd := m.Update(activeLoopsMsg{seq: 3, candidates: candidates})
	got := updated.(Model)
	if !reflect.DeepEqual(got.activeLoopCandidates, candidates) {
		t.Fatalf("active loops = %#v, want refreshed candidates despite later title sync failure", got.activeLoopCandidates)
	}
	if cmd == nil {
		t.Fatal("accepted active loop refresh did not dispatch title sync")
	}
	msg, ok := cmd().(activeLoopTitleSyncMsg)
	if !ok {
		t.Fatalf("title sync cmd returned %T, want activeLoopTitleSyncMsg", msg)
	}
	if !errors.Is(msg.err, setErr) {
		t.Fatalf("title sync err = %v, want %v", msg.err, setErr)
	}

	updated, _ = got.Update(msg)
	got = updated.(Model)
	if !reflect.DeepEqual(got.activeLoopCandidates, candidates) {
		t.Fatalf("active loops changed after sync error: %#v", got.activeLoopCandidates)
	}
	if !errors.Is(got.activeLoopTitleSyncErr, setErr) {
		t.Fatalf("activeLoopTitleSyncErr = %v, want %v", got.activeLoopTitleSyncErr, setErr)
	}
	status := fallbackStatusMessage(got)
	if status.Severity != statusWarning || !strings.Contains(status.Text, "Loop title sync:") {
		t.Fatalf("status = %+v, want loop title sync warning", status)
	}
}

func mkdirTestDir(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}
