package tui

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/agentloop"
	"github.com/sglavoie/agentmux/internal/session"
)

func loopTailPickerCandidate(plan, cwd string, pid int) agentloop.Candidate {
	return agentloop.Candidate{
		PlanDir:       plan,
		CWD:           cwd,
		PID:           pid,
		RunStartedAt:  time.Now().Add(-5 * time.Minute),
		ActiveSession: 3,
		ActiveTurn:    7,
	}
}

func loopTailPickerFixture(candidates []agentloop.Candidate) Model {
	return Model{
		mode:               ModeLoopTailPicker,
		width:              100,
		height:             40,
		loopTailCandidates: candidates,
	}
}

func TestRenderLoopTailPickerNormalState(t *testing.T) {
	m := loopTailPickerFixture([]agentloop.Candidate{
		loopTailPickerCandidate("/plans/api", "/work/api", 1234),
		loopTailPickerCandidate("/plans/backend", "/work/backend", 2345),
	})

	out := stripAnsi(renderLoopTailPicker(m))
	for _, want := range []string{
		"Choose loop to tail",
		"api",
		"pid 1234",
		"s3/t7",
		"started",
		"cwd /work/api",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in loop-tail picker output; got %q", want, out)
		}
	}
}

func TestRenderLoopTailPickerNarrowBoxClamps(t *testing.T) {
	m := loopTailPickerFixture([]agentloop.Candidate{
		loopTailPickerCandidate(
			"/plans/a-very-long-plan-directory-name-that-must-truncate",
			"/work/a-very-long-cwd-path-that-must-truncate",
			1234,
		),
	})
	m.width = 34
	m.height = 12

	raw := renderLoopTailPickerBox(m)
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if w := runewidth.StringWidth(stripAnsi(line)); w > m.width {
			t.Fatalf("box line exceeded m.width=%d (got %d): %q", m.width, w, line)
		}
	}
}

func TestRenderLoopTailPickerEmptyState(t *testing.T) {
	m := loopTailPickerFixture(nil)

	out := stripAnsi(renderLoopTailPicker(m))
	if !strings.Contains(out, "Choose loop to tail") {
		t.Fatalf("expected header in empty picker; got %q", out)
	}
	if !strings.Contains(out, "(no active loop candidates)") {
		t.Fatalf("expected empty state in picker; got %q", out)
	}
}

func TestRenderLoopTailPickerTinyHeightKeepsCursorRow(t *testing.T) {
	m := loopTailPickerFixture([]agentloop.Candidate{
		loopTailPickerCandidate("/plans/api", "/work/api", 111),
		loopTailPickerCandidate("/plans/backend", "/work/backend", 222),
		loopTailPickerCandidate("/plans/cli", "/work/cli", 333),
	})
	m.height = 9
	m.loopTailCursor = 2
	scrollLoopTailPickerToCursor(&m)

	out := stripAnsi(renderLoopTailPicker(m))
	if !strings.Contains(out, "cli") {
		t.Fatalf("expected cursor row to render on tiny height; got %q", out)
	}
}

func TestRenderLoopTailPickerOverlayComposition(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m.mode = ModeLoopTailPicker
	m.width = 100
	m.height = 40
	m.loopTailCandidates = []agentloop.Candidate{
		loopTailPickerCandidate("/plans/api", "/work/api", 1234),
	}

	out := m.render()
	plain := stripAnsi(out)
	if !strings.Contains(plain, "Choose loop to tail") {
		t.Fatalf("expected picker header in overlay output; got %q", plain)
	}
	if !strings.Contains(plain, "Agent Mux") {
		t.Fatalf("expected dashboard base to remain behind picker; got %q", plain)
	}
	if !strings.Contains(out, "╭") {
		t.Fatalf("expected overlay box in output; got %q", out)
	}
}

func TestLoopTailPickerNavigationClampsAndPages(t *testing.T) {
	candidates := make([]agentloop.Candidate, 8)
	for i := range candidates {
		candidates[i] = loopTailPickerCandidate("/plans/plan"+string(rune('a'+i)), "/work/x", 100+i)
	}
	m := loopTailPickerFixture(candidates)
	m.height = 11

	updated, _ := m.Update(keyPress("j"))
	m = updated.(Model)
	if m.loopTailCursor != 1 {
		t.Fatalf("j cursor = %d, want 1", m.loopTailCursor)
	}

	updated, _ = m.Update(keyPress("G"))
	m = updated.(Model)
	if m.loopTailCursor != len(candidates)-1 {
		t.Fatalf("G cursor = %d, want last", m.loopTailCursor)
	}
	if m.loopTailOffset == 0 {
		t.Fatalf("expected offset to scroll near bottom")
	}

	updated, _ = m.Update(keyPress("pgup"))
	m = updated.(Model)
	if m.loopTailCursor >= len(candidates)-1 {
		t.Fatalf("pgup did not move cursor up; got %d", m.loopTailCursor)
	}

	updated, _ = m.Update(keyPress("g"))
	m = updated.(Model)
	if m.loopTailCursor != 0 || m.loopTailOffset != 0 {
		t.Fatalf("g cursor/offset = %d/%d, want 0/0", m.loopTailCursor, m.loopTailOffset)
	}

	updated, _ = m.Update(keyPress("k"))
	m = updated.(Model)
	if m.loopTailCursor != 0 {
		t.Fatalf("k at top cursor = %d, want 0", m.loopTailCursor)
	}
}

func TestLoopTailPickerCancelPreservesDashboardState(t *testing.T) {
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "api", Account: "work", Directory: "/home/user/api"},
		{TabID: 2, WindowID: 12, Base: "backend", Account: "work", Directory: "/home/user/backend"},
	})
	m.applyFilterChange(Parse("backend"))
	m.previewEnabled = true
	m.previewWindowID = 12
	m.previewText = "scrollback"
	m.mode = ModeLoopTailPicker
	m.loopTailCandidates = []agentloop.Candidate{
		loopTailPickerCandidate("/plans/backend", "/home/user/backend", 1234),
	}
	priorFilter := m.activeFilter
	priorFiltered := append([]session.RunningSession(nil), m.filtered...)
	priorCursor := m.cursor
	priorPreviewEnabled := m.previewEnabled
	priorPreviewWindowID := m.previewWindowID
	priorPreviewText := m.previewText

	updated, cmd := m.Update(keyPress("esc"))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("cancel returned cmd %v, want nil", cmd)
	}
	if got.mode != ModeList {
		t.Fatalf("mode = %v, want ModeList", got.mode)
	}
	if !reflect.DeepEqual(got.activeFilter, priorFilter) {
		t.Fatalf("activeFilter changed: %#v -> %#v", priorFilter, got.activeFilter)
	}
	if !reflect.DeepEqual(got.filtered, priorFiltered) {
		t.Fatalf("filtered sessions changed on cancel")
	}
	if got.cursor != priorCursor {
		t.Fatalf("cursor = %d, want %d", got.cursor, priorCursor)
	}
	if got.previewEnabled != priorPreviewEnabled ||
		got.previewWindowID != priorPreviewWindowID ||
		got.previewText != priorPreviewText {
		t.Fatalf("preview state changed on cancel")
	}
}

func TestLoopTailPickerEnterLaunchesSelectedCandidate(t *testing.T) {
	stubLoopTailLookPath(t, "/bin/agent-session-loop", nil)
	var execCalls []*exec.Cmd
	stubLoopTailExecProcess(t, nil, &execCalls)
	client := &fakeLoopTailKittyClient{}
	candidates := []agentloop.Candidate{
		loopTailPickerCandidate("/plans/api", "/work/api", 111),
		loopTailPickerCandidate("/plans/backend", "/work/backend", 222),
	}
	m := loopTailPickerFixture(candidates)
	m.client = client
	m.loopTailCursor = 1

	updated, cmd := m.Update(keyPress("enter"))
	got := updated.(Model)
	if got.mode != ModeList {
		t.Fatalf("mode = %v, want ModeList", got.mode)
	}
	if len(got.loopTailCandidates) != 0 {
		t.Fatalf("expected picker candidates cleared, got %#v", got.loopTailCandidates)
	}
	if cmd == nil {
		t.Fatal("enter on selected candidate returned nil cmd")
	}

	done, ok := cmd().(loopTailDoneMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want loopTailDoneMsg", done)
	}
	if done.err != nil {
		t.Fatalf("launch err = %v, want nil", done.err)
	}
	if len(execCalls) != 1 {
		t.Fatalf("exec calls = %d, want 1", len(execCalls))
	}
	if got := execCalls[0].Args[2]; got != "/plans/backend" {
		t.Fatalf("launched plan = %q, want /plans/backend", got)
	}
	if len(client.overlays) != 0 {
		t.Fatalf("kitty overlay calls = %d, want 0", len(client.overlays))
	}
}

func TestLoopTailPickerEnterOnEmptyListIsNoOp(t *testing.T) {
	m := loopTailPickerFixture(nil)
	updated, cmd := m.Update(keyPress("enter"))
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("empty enter returned cmd %v, want nil", cmd)
	}
	if got.mode != ModeLoopTailPicker {
		t.Fatalf("mode = %v, want ModeLoopTailPicker", got.mode)
	}
}

func TestLoopTailPickerCtrlCQuits(t *testing.T) {
	m := loopTailPickerFixture([]agentloop.Candidate{
		loopTailPickerCandidate("/plans/api", "/work/api", 1234),
	})

	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if cmd == nil {
		t.Fatal("ctrl+c in loop-tail picker returned nil; expected tea.Quit")
	}
}
