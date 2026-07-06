package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/plans"
)

// planViewTestModel returns a Model in ModePlanView with a viewerPlan
// pointed at a freshly seeded plan dir, but no lines loaded yet. The
// caller is expected to drive readPlanFilesCmd (or feed a
// planFilesLoadedMsg directly) to populate planViewerLines.
func planViewTestModel(t *testing.T) (Model, plans.PlanMeta) {
	t.Helper()
	root := t.TempDir()
	planDir := seedPlanDir(t, root, "alpha-aabb112233cc", map[string]string{
		"manifest.md":  "# alpha plan\n\nalpha needle beta\n",
		"session-1.md": "first line\nneedle on line two\nlast line\n",
	})
	meta := plans.PlanMeta{
		Name:    plans.DisplayName(filepath.Base(planDir)),
		DirPath: planDir,
		Files:   []string{"manifest.md", "session-1.md"},
		ModTime: time.Now(),
	}

	cfg := &config.Config{}
	cfg.Defaults.PlansDirectory = root
	cfg.Defaults.Editor = "myeditor"
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")

	m := Model{
		mode:   ModePlanSearch,
		width:  100,
		height: 30,
		cfg:    cfg,
		client: client,
	}
	return m, meta
}

// TestEnterPlanView_TransitionsModeAndDispatchesLoad verifies enterPlanView
// sets ModePlanView, copies the meta into viewerPlan, bumps the load seq,
// and returns a non-nil tea.Cmd that produces a planFilesLoadedMsg.
func TestEnterPlanView_TransitionsModeAndDispatchesLoad(t *testing.T) {
	m, meta := planViewTestModel(t)

	updated, cmd := m.enterPlanView(meta)
	got := updated.(Model)
	if got.mode != ModePlanView {
		t.Fatalf("mode = %v, want ModePlanView", got.mode)
	}
	if got.viewerPlan == nil {
		t.Fatal("viewerPlan = nil, want non-nil meta copy")
	}
	if got.viewerPlan.DirPath != meta.DirPath {
		t.Errorf("viewerPlan.DirPath = %q, want %q", got.viewerPlan.DirPath, meta.DirPath)
	}
	if !got.planViewerLoading {
		t.Errorf("planViewerLoading = false, want true")
	}
	if got.planViewerLoadSeq == 0 {
		t.Errorf("planViewerLoadSeq = 0, want > 0")
	}
	if cmd == nil {
		t.Fatal("enterPlanView returned nil cmd; want readPlanFilesCmd")
	}
	msg := cmd()
	loaded, ok := msg.(planFilesLoadedMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want planFilesLoadedMsg", msg)
	}
	if loaded.err != nil {
		t.Fatalf("planFilesLoadedMsg.err = %v, want nil", loaded.err)
	}
	if len(loaded.lines) == 0 {
		t.Fatal("planFilesLoadedMsg.lines is empty; want content lines")
	}
}

// TestPlanFilesLoadedMsg_PopulatesLinesAndMatches feeds a planFilesLoadedMsg
// to a model that has planQuery set and verifies the Update handler
// populates planViewerLines, computes planViewerMatchLines, and places the
// cursor on the first match.
func TestPlanFilesLoadedMsg_PopulatesLinesAndMatches(t *testing.T) {
	m, meta := planViewTestModel(t)
	updated, cmd := m.enterPlanView(meta)
	m = updated.(Model)
	m.planQuery = "needle"

	loaded := cmd().(planFilesLoadedMsg)
	updated, _ = m.Update(loaded)
	m = updated.(Model)

	if m.planViewerLoading {
		t.Errorf("planViewerLoading still true after planFilesLoadedMsg")
	}
	if len(m.planViewerLines) == 0 {
		t.Fatal("planViewerLines empty after load")
	}
	if len(m.planViewerMatchLines) < 2 {
		t.Fatalf("planViewerMatchLines = %d, want at least 2 matches for 'needle'",
			len(m.planViewerMatchLines))
	}
	if m.planViewerCursor != m.planViewerMatchLines[0] {
		t.Errorf("planViewerCursor = %d, want first match line %d",
			m.planViewerCursor, m.planViewerMatchLines[0])
	}
	// Separator lines are part of planViewerLines; the first line should be
	// a separator for the first file.
	if want := "--- manifest.md ---"; m.planViewerLines[0] != want {
		t.Errorf("planViewerLines[0] = %q, want %q", m.planViewerLines[0], want)
	}
}

// TestPlanViewer_MatchNavigationWrapsAround drives n/N over a model with
// multiple matches and verifies the index wraps in both directions.
func TestPlanViewer_MatchNavigationWrapsAround(t *testing.T) {
	m, meta := planViewTestModel(t)
	updated, cmd := m.enterPlanView(meta)
	m = updated.(Model)
	m.planQuery = "needle"

	loaded := cmd().(planFilesLoadedMsg)
	updated, _ = m.Update(loaded)
	m = updated.(Model)

	n := len(m.planViewerMatchLines)
	if n < 2 {
		t.Fatalf("setup: need at least 2 matches, got %d", n)
	}

	// `n` advances one match.
	m.jumpToNextPlanMatch(1)
	if m.planViewerMatchIdx != 1 {
		t.Errorf("after `n`: matchIdx=%d, want 1", m.planViewerMatchIdx)
	}

	// Wrap forward: advancing from the last match returns to index 0.
	m.planViewerMatchIdx = n - 1
	m.jumpToNextPlanMatch(1)
	if m.planViewerMatchIdx != 0 {
		t.Errorf("forward wrap: matchIdx=%d, want 0", m.planViewerMatchIdx)
	}

	// Wrap backward: stepping back from index 0 returns to the last match.
	m.jumpToNextPlanMatch(-1)
	if m.planViewerMatchIdx != n-1 {
		t.Errorf("backward wrap: matchIdx=%d, want %d", m.planViewerMatchIdx, n-1)
	}
}

func TestPlanViewerPageKeysScrollWrappedRowsWithinOneLogicalLine(t *testing.T) {
	m := Model{
		mode:             ModePlanView,
		width:            8,
		height:           16,
		planViewerLines:  []string{"abcdefghijklmnopqrstuvwxTAIL"},
		planViewerCursor: 0,
	}

	updated, _ := m.Update(keyPress("pgdown"))
	m = updated.(Model)

	if m.planViewerCursor != 0 {
		t.Fatalf("pgdown cursor = %d, want selected logical line to remain 0", m.planViewerCursor)
	}
	if m.planViewerScroll == 0 {
		t.Fatalf("pgdown did not advance visual row scroll")
	}
	out := stripAnsi(renderPlanViewBody(m, planViewerVisibleHeight(m)))
	if !strings.Contains(out, "TAIL") {
		t.Fatalf("pgdown did not reveal wrapped tail:\n%s", out)
	}
	if strings.Contains(out, "abcdefgh") {
		t.Fatalf("pgdown still rendered first wrapped row:\n%s", out)
	}

	updated, _ = m.Update(keyPress("pgup"))
	m = updated.(Model)

	if m.planViewerCursor != 0 {
		t.Fatalf("pgup cursor = %d, want selected logical line to remain 0", m.planViewerCursor)
	}
	if m.planViewerScroll != 0 {
		t.Fatalf("pgup scroll = %d, want 0", m.planViewerScroll)
	}
}

func TestPlanViewerMatchJumpScrollsToFirstWrappedRow(t *testing.T) {
	m := Model{
		mode:   ModePlanView,
		width:  8,
		height: 8,
		planViewerLines: []string{
			"short",
			"abcdefghijklmnop",
			"needleabcdefghijklmnop",
		},
		planViewerMatchLines: []int{2},
	}

	m.jumpToNextPlanMatch(1)

	rows := planViewerRows(m)
	wantScroll := planViewerFirstRowForLine(rows, 2)
	if wantScroll < 0 {
		t.Fatalf("setup: no wrapped row found for source line 2: %#v", rows)
	}
	if m.planViewerCursor != 2 {
		t.Fatalf("cursor = %d, want matched logical source line 2", m.planViewerCursor)
	}
	if m.planViewerScroll != wantScroll {
		t.Fatalf("scroll = %d, want first wrapped row %d for matched line", m.planViewerScroll, wantScroll)
	}
}

func TestPlanViewerNavigationEmptyAndInvalidSelectionsClampSafely(t *testing.T) {
	empty := Model{
		mode:             ModePlanView,
		width:            8,
		height:           8,
		planViewerCursor: 4,
		planViewerScroll: 4,
	}
	updated, _ := empty.Update(keyPress("pgdown"))
	got := updated.(Model)
	if got.planViewerCursor != 0 {
		t.Fatalf("empty cursor = %d, want 0", got.planViewerCursor)
	}
	if got.planViewerScroll != 0 {
		t.Fatalf("empty scroll = %d, want 0", got.planViewerScroll)
	}

	m := Model{
		mode:             ModePlanView,
		width:            8,
		height:           8,
		planViewerLines:  []string{"first", "second"},
		planViewerCursor: 99,
		planViewerScroll: 99,
	}
	scrollPlanViewerToCursor(&m)
	if m.planViewerCursor != 1 {
		t.Fatalf("high cursor = %d, want clamped to last logical line 1", m.planViewerCursor)
	}
	if m.planViewerScroll > planViewerMaxScrollOffset(len(planViewerRows(m)), planViewerVisibleHeight(m)) {
		t.Fatalf("high scroll = %d exceeds wrapped-row bounds", m.planViewerScroll)
	}

	m.planViewerCursor = -5
	m.planViewerScroll = -5
	scrollPlanViewerToCursor(&m)
	if m.planViewerCursor != 0 {
		t.Fatalf("negative cursor = %d, want 0", m.planViewerCursor)
	}
	if m.planViewerScroll != 0 {
		t.Fatalf("negative scroll = %d, want 0", m.planViewerScroll)
	}
}

// TestPlanViewer_EscReturnsToPlanSearch verifies pressing esc inside the
// viewer transitions back to ModePlanSearch and zeroes the viewer-local
// state so the picker view renders cleanly.
func TestPlanViewer_EscReturnsToPlanSearch(t *testing.T) {
	m, meta := planViewTestModel(t)
	updated, cmd := m.enterPlanView(meta)
	m = updated.(Model)
	loaded := cmd().(planFilesLoadedMsg)
	updated, _ = m.Update(loaded)
	m = updated.(Model)

	if m.mode != ModePlanView {
		t.Fatalf("setup: mode=%v, want ModePlanView", m.mode)
	}

	updated, _ = m.Update(keyPress("esc"))
	m = updated.(Model)

	if m.mode != ModePlanSearch {
		t.Errorf("after esc: mode=%v, want ModePlanSearch", m.mode)
	}
	if m.viewerPlan != nil {
		t.Errorf("after esc: viewerPlan = %+v, want nil", m.viewerPlan)
	}
	if m.planViewerLines != nil {
		t.Errorf("after esc: planViewerLines not nil")
	}
	if m.planViewerMatchLines != nil {
		t.Errorf("after esc: planViewerMatchLines not nil")
	}
}

// TestPlanViewer_StaleLoadIsDropped ensures a slow file-read returning after
// the user already exited (or opened a different plan) is silently ignored
// rather than re-entering the viewer.
func TestPlanViewer_StaleLoadIsDropped(t *testing.T) {
	m, meta := planViewTestModel(t)
	updated, cmd := m.enterPlanView(meta)
	m = updated.(Model)
	loaded := cmd().(planFilesLoadedMsg)

	// Simulate the user exiting before the load returns.
	updated, _ = m.Update(keyPress("esc"))
	m = updated.(Model)

	// Replay the stale message — Update must drop it without altering
	// viewer state.
	updated, _ = m.Update(loaded)
	m = updated.(Model)

	if m.mode != ModePlanSearch {
		t.Errorf("stale load: mode=%v, want ModePlanSearch", m.mode)
	}
	if m.viewerPlan != nil {
		t.Errorf("stale load reopened viewer: viewerPlan = %+v", m.viewerPlan)
	}
	if m.planViewerLines != nil {
		t.Errorf("stale load populated planViewerLines")
	}
}

// TestPlanViewer_OpenEditorInvokesLaunch exercises the `o` keybinding from
// inside the viewer.
func TestPlanViewer_OpenEditorInvokesLaunch(t *testing.T) {
	m, meta := planViewTestModel(t)
	runner := kitty.NewMockRunner(nil)
	m.client = kitty.NewClientWithSocket(runner, "")

	updated, cmd := m.enterPlanView(meta)
	m = updated.(Model)
	loaded := cmd().(planFilesLoadedMsg)
	updated, _ = m.Update(loaded)
	m = updated.(Model)

	_, oCmd := m.Update(keyPress("o"))
	if oCmd == nil {
		t.Fatal("`o` produced nil cmd; want openPlanInEditorCmd")
	}
	if msg := oCmd(); msg != nil {
		if errMsg, ok := msg.(planEditorErrMsg); ok {
			t.Fatalf("openPlanInEditorCmd surfaced an error: %v", errMsg.err)
		}
	}

	launchCalled := false
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a == "launch" {
				launchCalled = true
				break
			}
		}
	}
	if !launchCalled {
		t.Error("expected kitten @ launch call after `o` keypress in viewer")
	}
}

// TestReadPlanFilesCmd_NoFiles guards against a degenerate plan whose
// Files slice is empty: the load must complete with err=nil and an empty
// lines slice so the viewer renders the (no content) placeholder cleanly.
func TestReadPlanFilesCmd_NoFiles(t *testing.T) {
	dir := t.TempDir()
	meta := plans.PlanMeta{Name: "empty", DirPath: dir, Files: nil}
	msg := readPlanFilesCmd(1, meta)()
	loaded, ok := msg.(planFilesLoadedMsg)
	if !ok {
		t.Fatalf("got %T, want planFilesLoadedMsg", msg)
	}
	if loaded.err != nil {
		t.Errorf("err = %v, want nil", loaded.err)
	}
	if len(loaded.lines) != 0 {
		t.Errorf("lines = %v, want empty", loaded.lines)
	}
}

// TestReadPlanFilesCmd_MissingFileSurfacesError ensures a missing file in
// meta.Files surfaces the read error rather than silently truncating the
// output. The seq is propagated unchanged.
func TestReadPlanFilesCmd_MissingFileSurfacesError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	meta := plans.PlanMeta{
		Name:    "broken",
		DirPath: dir,
		Files:   []string{"ok.md", "missing.md"},
	}
	msg := readPlanFilesCmd(42, meta)()
	loaded := msg.(planFilesLoadedMsg)
	if loaded.seq != 42 {
		t.Errorf("seq = %d, want 42", loaded.seq)
	}
	if loaded.err == nil {
		t.Fatal("err = nil, want a read error for missing file")
	}
}

func TestRenderPlanViewBody_WrapsLongLineWithoutTruncation(t *testing.T) {
	m := Model{
		width:           8,
		planViewerLines: []string{"abcdefghijklmnopqrstuvwxTAIL"},
	}

	out := stripAnsi(renderPlanViewBody(m, 10))
	if strings.Contains(out, "…") {
		t.Fatalf("rendered plan view used truncation ellipsis:\n%s", out)
	}
	for _, want := range []string{"abcdefgh", "ijklmnop", "qrstuvwx", "TAIL"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered plan view missing wrapped content %q:\n%s", want, out)
		}
	}
}

func TestRenderPlanViewBody_VisualScrollShowsWrappedTail(t *testing.T) {
	m := Model{
		width:            8,
		planViewerScroll: 3,
		planViewerLines:  []string{"abcdefghijklmnopqrstuvwxTAIL"},
	}

	out := stripAnsi(renderPlanViewBody(m, 3))
	if !strings.Contains(out, "TAIL") {
		t.Fatalf("visual scroll did not reveal wrapped tail:\n%s", out)
	}
	if strings.Contains(out, "abcdefgh") {
		t.Fatalf("visual scroll rendered the first wrapped row unexpectedly:\n%s", out)
	}
}

func TestRenderPlanViewBody_IndicatorsUseWrappedRowCount(t *testing.T) {
	m := Model{
		width:           8,
		planViewerLines: []string{"abcdefghijklmnopqrstuvwxTAIL"},
	}

	top := stripAnsi(renderPlanViewBody(m, 3))
	if !strings.Contains(top, "▼") {
		t.Fatalf("top window missing bottom indicator for wrapped rows:\n%s", top)
	}
	if strings.Contains(top, "▲") {
		t.Fatalf("top window showed unexpected top indicator:\n%s", top)
	}

	m.planViewerScroll = 1
	middle := stripAnsi(renderPlanViewBody(m, 3))
	if !strings.Contains(middle, "▲") || !strings.Contains(middle, "▼") {
		t.Fatalf("middle window should show both wrapped-row indicators:\n%s", middle)
	}
}

func TestRenderPlanViewBody_StylesEveryWrappedRowBySourceLine(t *testing.T) {
	m := Model{
		width:           8,
		planViewerLines: []string{"--- abcdefghijklmnop ---", "needleabcdefghijklmnop", "selectedabcdefghijklmnop"},
		planViewerMatchLines: []int{
			1,
		},
		planViewerCursor: 2,
	}

	out := renderPlanViewBody(m, 20)
	rows := planViewerRows(m)
	for _, row := range rows {
		var want string
		switch row.sourceLine {
		case 0:
			want = styleDim.Render(row.text)
		case 1:
			want = styleSearch.Render(row.text)
		case 2:
			want = styleSelected.Render(row.text)
		default:
			t.Fatalf("unexpected source line in row %#v", row)
		}
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing styled row %q for source line %d:\n%q",
				row.text, row.sourceLine, out)
		}
	}
}

func TestRenderPlanViewBody_RowsFitWithinModelWidth(t *testing.T) {
	m := Model{
		width: 12,
		planViewerLines: []string{
			"abcdefghijklmnopqrstuvwx",
			"こんにちこんにちは",
			"short",
		},
		planViewerMatchLines: []int{0, 1},
		planViewerCursor:     2,
	}

	out := stripAnsi(renderPlanViewBody(m, 20))
	for i, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if w := runewidth.StringWidth(line); w > m.width {
			t.Errorf("rendered row %d width = %d, want <= %d: %q", i, w, m.width, line)
		}
	}
}

func TestPlanViewerRows_WrapsLongProseAndMapsSourceLine(t *testing.T) {
	lines := []string{
		"short",
		"the quick brown foxxxxxxxxxxxxxxxx jumped over the lazy dog.",
	}

	rows := planViewerRowsForWidth(lines, 16)
	want := []planViewerRow{
		{text: "short", sourceLine: 0},
		{text: "the quick brown", sourceLine: 1},
		{text: "foxxxxxxxxxxxxxx", sourceLine: 1},
		{text: "xx jumped over", sourceLine: 1},
		{text: "the lazy dog.", sourceLine: 1},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows len = %d, want %d: %#v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %#v, want %#v", i, rows[i], want[i])
		}
	}
}

func TestPlanViewerRows_WrapsLongUnbrokenTokens(t *testing.T) {
	rows := planViewerRowsForWidth([]string{"abcdefghijkl"}, 5)
	want := []string{"abcde", "fghij", "kl"}
	if len(rows) != len(want) {
		t.Fatalf("rows len = %d, want %d: %#v", len(rows), len(want), rows)
	}
	for i, wantText := range want {
		if rows[i].text != wantText {
			t.Errorf("row %d text = %q, want %q", i, rows[i].text, wantText)
		}
		if rows[i].sourceLine != 0 {
			t.Errorf("row %d sourceLine = %d, want 0", i, rows[i].sourceLine)
		}
	}
}

func TestPlanViewerRows_PreservesEmptyLines(t *testing.T) {
	rows := planViewerRowsForWidth([]string{"", "after"}, 80)
	want := []planViewerRow{
		{text: "", sourceLine: 0},
		{text: "after", sourceLine: 1},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows len = %d, want %d: %#v", len(rows), len(want), rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %#v, want %#v", i, rows[i], want[i])
		}
	}
}

func TestPlanViewerRows_SeparatorsUseLogicalSourceLine(t *testing.T) {
	lines := []string{
		"--- very-long-plan-file-name.md ---",
		"ordinary content",
	}

	rows := planViewerRowsForWidth(lines, 8)
	if len(rows) < 3 {
		t.Fatalf("rows len = %d, want separator to wrap: %#v", len(rows), rows)
	}
	seenContent := false
	for _, row := range rows {
		switch row.sourceLine {
		case 0:
			if !row.isSeparator(lines) {
				t.Errorf("separator row %#v was not detected from source line", row)
			}
		case 1:
			seenContent = true
			if row.isSeparator(lines) {
				t.Errorf("content row %#v was detected as a separator", row)
			}
		default:
			t.Errorf("row sourceLine = %d, want 0 or 1", row.sourceLine)
		}
	}
	if !seenContent {
		t.Fatal("wrapped rows did not include the content line")
	}
}

func TestPlanViewerRows_MatchesUseLogicalSourceLine(t *testing.T) {
	lines := []string{
		"needle appears in a long enough line to wrap",
		"plain row",
	}
	rows := planViewerRowsForWidth(lines, 12)
	matches := computePlanViewerMatches(lines, "needle")
	if len(matches) != 1 || matches[0] != 0 {
		t.Fatalf("matches = %v, want logical source line 0", matches)
	}

	for _, row := range rows {
		got := row.isMatch(matches)
		want := row.sourceLine == 0
		if got != want {
			t.Errorf("row %#v isMatch = %v, want %v", row, got, want)
		}
	}
}

func TestPlanViewerRows_WrapsUnicodeWideCharacters(t *testing.T) {
	rows := planViewerRowsForWidth([]string{"こんにち"}, 7)
	want := []string{"こんに", "ち"}
	if len(rows) != len(want) {
		t.Fatalf("rows len = %d, want %d: %#v", len(rows), len(want), rows)
	}
	for i, wantText := range want {
		if rows[i].text != wantText {
			t.Errorf("row %d text = %q, want %q", i, rows[i].text, wantText)
		}
		if w := runewidth.StringWidth(rows[i].text); w > 7 {
			t.Errorf("row %d width = %d, want <= 7", i, w)
		}
	}
}

func TestPlanViewerRows_ZeroWidthFallsBackToBoundedWrap(t *testing.T) {
	line := strings.Repeat("x", planViewerFallbackWidth+1)
	rows := planViewerRows(Model{
		width:           0,
		planViewerLines: []string{line},
	})

	if len(rows) != 2 {
		t.Fatalf("rows len = %d, want 2 from fallback width: %#v", len(rows), rows)
	}
	if got := len(rows[0].text); got != planViewerFallbackWidth {
		t.Errorf("first row len = %d, want fallback width %d", got, planViewerFallbackWidth)
	}
	if rows[1].text != "x" {
		t.Errorf("second row = %q, want final x", rows[1].text)
	}
}
