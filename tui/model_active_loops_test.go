package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

func stubActiveLoopDiscover(t *testing.T, candidates []agentloop.Candidate, err error) {
	t.Helper()
	old := commands.ActiveLoopDiscover
	commands.ActiveLoopDiscover = func(agentloop.DiscoverOptions) ([]agentloop.Candidate, error) {
		return candidates, err
	}
	t.Cleanup(func() { commands.ActiveLoopDiscover = old })
}

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
	stubActiveLoopDiscover(t, []agentloop.Candidate{candidate}, nil)

	m := testModel()
	cmds := topLevelBatchCmds(t, m.Init())
	if len(cmds) < 2 {
		t.Fatalf("init batch command count = %d, want at least 2", len(cmds))
	}
	msg, ok := cmds[2]().(commands.ActiveLoopsMsg)
	if !ok {
		t.Fatalf("init command produced %T, want commands.ActiveLoopsMsg", msg)
	}
	updated, _ := m.Update(msg)
	if got := updated.(Model).activeLoopCandidates; !reflect.DeepEqual(got, []agentloop.Candidate{candidate}) {
		t.Fatalf("active loops = %#v, want candidate", got)
	}
}

func TestTickRefreshesActiveLoops(t *testing.T) {
	candidate := agentloop.Candidate{PlanDir: "/plans/backend", CWD: "/work/backend"}
	stubActiveLoopDiscover(t, []agentloop.Candidate{candidate}, nil)

	// Settle the startup refresh the constructor armed, so the tick below
	// starts a topology refresh instead of collapsing into the pending bit.
	m := loadSessions(testModel(), makeSessions())
	m.activeLoopRefreshSeq = 7
	updated, cmd := m.Update(commands.TickMsg{})
	got := updated.(Model)
	if got.activeLoopRefreshSeq != 8 {
		t.Fatalf("activeLoopRefreshSeq = %d, want 8", got.activeLoopRefreshSeq)
	}
	cmds := topLevelBatchCmds(t, cmd)
	if len(cmds) < 2 {
		t.Fatalf("tick batch command count = %d, want at least 2", len(cmds))
	}
	msg, ok := cmds[1]().(commands.ActiveLoopsMsg)
	if !ok {
		t.Fatalf("tick command produced %T, want commands.ActiveLoopsMsg", msg)
	}
	updated, _ = got.Update(msg)
	if candidates := updated.(Model).activeLoopCandidates; !reflect.DeepEqual(candidates, []agentloop.Candidate{candidate}) {
		t.Fatalf("active loops = %#v, want candidate", candidates)
	}
}

func TestActiveLoopRefreshStoresCandidatesWithoutTitleWrite(t *testing.T) {
	t.Parallel()

	candidates := []agentloop.Candidate{{PlanDir: "/plans/api", CWD: "/work/api"}}
	m := testModel()
	m.activeLoopRefreshSeq = 4

	updated, cmd := m.Update(commands.ActiveLoopsMsg{Seq: 4, Candidates: candidates})
	got := updated.(Model)
	if !reflect.DeepEqual(got.activeLoopCandidates, candidates) {
		t.Fatalf("active loops = %#v, want %#v", got.activeLoopCandidates, candidates)
	}
	if cmd != nil {
		t.Fatal("active loop refresh must not dispatch a Kitty title write")
	}
}

func TestActiveLoopRefreshClearsStaleCandidates(t *testing.T) {
	t.Parallel()

	m := testModel()
	m.activeLoopRefreshSeq = 4
	m.activeLoopCandidates = []agentloop.Candidate{{PlanDir: "/plans/stale", CWD: "/work/stale"}}

	updated, cmd := m.Update(commands.ActiveLoopsMsg{Seq: 4})
	if got := updated.(Model).activeLoopCandidates; len(got) != 0 {
		t.Fatalf("active loops = %#v, want empty", got)
	}
	if cmd != nil {
		t.Fatal("empty active loop refresh must not dispatch a Kitty title write")
	}
}

func TestActiveLoopRefreshDropsStaleSequence(t *testing.T) {
	t.Parallel()

	current := agentloop.Candidate{PlanDir: "/plans/current", CWD: "/work/current"}
	m := testModel()
	m.activeLoopRefreshSeq = 6
	m.activeLoopCandidates = []agentloop.Candidate{current}

	updated, cmd := m.Update(commands.ActiveLoopsMsg{Seq: 5, Candidates: []agentloop.Candidate{{PlanDir: "/plans/older", CWD: "/work/older"}}})
	got := updated.(Model)
	if !reflect.DeepEqual(got.activeLoopCandidates, []agentloop.Candidate{current}) {
		t.Fatalf("active loops = %#v, want current candidate preserved", got.activeLoopCandidates)
	}
	if cmd != nil {
		t.Fatal("stale active loop refresh returned a command")
	}
}

func TestActiveLoopsForSelectedRowMatchesCanonicalWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realDir := mkdirTestDir(t, root, "project")
	linkDir := filepath.Join(root, "project-link")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	match := agentloop.Candidate{PlanDir: "/plans/match", CWD: realDir}
	m := loadSessions(testModel(), []session.RunningSession{{Directory: linkDir}})
	m.activeLoopCandidates = []agentloop.Candidate{{PlanDir: "/plans/other", CWD: filepath.Join(root, "other")}, match}

	if got := m.activeLoopsForSession(m.sessions[0]); !reflect.DeepEqual(got, []agentloop.Candidate{match}) {
		t.Fatalf("selected-row active loops = %#v, want match", got)
	}
}

func TestActiveLoopsForSelectedRowPrefersSameHarness(t *testing.T) {
	t.Parallel()

	workspace := mkdirTestDir(t, t.TempDir(), "project")
	codexSession := session.RunningSession{WindowID: 101, Directory: workspace, Harness: "codex"}
	copilotSession := session.RunningSession{WindowID: 202, Directory: workspace, Harness: "copilot"}
	copilotLoop := agentloop.Candidate{PlanDir: "/plans/copilot", CWD: workspace, Harness: "copilot"}
	m := loadSessions(testModel(), []session.RunningSession{codexSession, copilotSession})
	m.activeLoopCandidates = []agentloop.Candidate{copilotLoop}

	if got := m.activeLoopsForSession(copilotSession); !reflect.DeepEqual(got, []agentloop.Candidate{copilotLoop}) {
		t.Fatalf("copilot row active loops = %#v, want copilot loop", got)
	}
	if got := m.activeLoopsForSession(codexSession); len(got) != 0 {
		t.Fatalf("codex row active loops = %#v, want none", got)
	}
}

func TestActiveLoopsForSelectedRowMarksProjectOwner(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	callerDir := mkdirTestDir(t, root, "scripts")
	workspace := mkdirTestDir(t, root, "project")
	caller := session.RunningSession{WindowID: 42, Directory: callerDir, Harness: "claude"}
	codex := session.RunningSession{WindowID: 101, Directory: workspace, Harness: "codex"}
	copilot := session.RunningSession{WindowID: 202, Directory: workspace, Harness: "copilot"}
	loop := agentloop.Candidate{PlanDir: "/plans/project", CWD: workspace, Harness: "copilot"}
	m := loadSessions(testModel(), []session.RunningSession{caller, codex, copilot})
	m.activeLoopCandidates = []agentloop.Candidate{loop}

	if got := m.activeLoopsForSession(copilot); !reflect.DeepEqual(got, []agentloop.Candidate{loop}) {
		t.Fatalf("copilot project row active loops = %#v, want loop", got)
	}
	if got := m.activeLoopsForSession(codex); len(got) != 0 {
		t.Fatalf("codex row active loops = %#v, want none", got)
	}
	if got := m.activeLoopsForSession(caller); len(got) != 0 {
		t.Fatalf("caller row active loops = %#v, want none", got)
	}
}

func TestActiveLoopsForSelectedRowUsesWindowIDBeforeWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspace := mkdirTestDir(t, root, "project")
	worktree := mkdirTestDir(t, root, "project--ws14")
	target := session.RunningSession{WindowID: 101, Directory: workspace, Harness: "codex"}
	other := session.RunningSession{WindowID: 202, Directory: workspace, Harness: "codex"}
	loop := agentloop.Candidate{PlanDir: "/plans/project", CWD: worktree, Harness: "codex", WindowID: 101}
	m := loadSessions(testModel(), []session.RunningSession{target, other})
	m.activeLoopCandidates = []agentloop.Candidate{loop}

	if got := m.activeLoopsForSession(target); !reflect.DeepEqual(got, []agentloop.Candidate{loop}) {
		t.Fatalf("target row active loops = %#v, want loop", got)
	}
	if got := m.activeLoopsForSession(other); len(got) != 0 {
		t.Fatalf("other row active loops = %#v, want none", got)
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
