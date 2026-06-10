package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/session"
)

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
	m := testModel()
	m.activeLoopRefreshSeq = 6
	m.activeLoopCandidates = []agentloop.Candidate{current}

	updated, _ := m.Update(activeLoopsMsg{
		seq:        5,
		candidates: []agentloop.Candidate{older},
	})
	got := updated.(Model)
	if !reflect.DeepEqual(got.activeLoopCandidates, []agentloop.Candidate{current}) {
		t.Fatalf("active loops = %#v, want current candidate preserved", got.activeLoopCandidates)
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
