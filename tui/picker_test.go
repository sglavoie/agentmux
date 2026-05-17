package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// pickerModel returns a model preloaded with a picker open and seeded with a
// few config sessions across two accounts. Each test starts from this state
// so the picker is already focused.
func pickerModel(t *testing.T) Model {
	t.Helper()
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
			{Title: "backend", Directory: "/home/user/backend", Account: "work"},
			{Title: "dotfiles", Directory: "/home/user/dotfiles", Account: "personal"},
			{Title: "notes", Directory: "/home/user/notes", Account: "personal"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, nil)
	m = pressKey(m, "l")
	if m.mode != ModePicker {
		t.Fatalf("expected ModePicker after l, got %d", m.mode)
	}
	return m
}

func TestPickerTypingNarrowsLive(t *testing.T) {
	m := pickerModel(t)
	if len(m.pickerFilteredIdx) != 4 {
		t.Fatalf("expected 4 picker sessions initially, got %d", len(m.pickerFilteredIdx))
	}
	// Press / to enter search mode, then type "tes" (substring of "notes" only).
	m = pressKey(m, "/")
	if !m.pickerSearchMode {
		t.Fatal("expected pickerSearchMode=true after /")
	}
	m = pressKey(m, "t")
	m = pressKey(m, "e")
	m = pressKey(m, "s")
	if m.pickerFilter != "tes" {
		t.Fatalf("expected pickerFilter=tes, got %q", m.pickerFilter)
	}
	if len(m.pickerFilteredIdx) != 1 {
		t.Fatalf("expected 1 match for 'tes', got %d", len(m.pickerFilteredIdx))
	}
	if m.mode != ModePicker {
		t.Fatalf("expected to stay in ModePicker while typing, got %d", m.mode)
	}
}

func TestPickerBackspaceShrinksBuffer(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "/") // enter search mode
	for _, k := range []string{"t", "e", "s"} {
		m = pressKey(m, k)
	}
	m = pressKey(m, "backspace")
	if m.pickerFilter != "te" {
		t.Fatalf("expected pickerFilter=te, got %q", m.pickerFilter)
	}
	// "te" still matches "notes" only.
	if len(m.pickerFilteredIdx) != 1 {
		t.Fatalf("expected 1 match for 'te', got %d", len(m.pickerFilteredIdx))
	}
	// Over-backspace empties the buffer but stays in search mode.
	for i := 0; i < 5; i++ {
		m = pressKey(m, "backspace")
	}
	if m.pickerFilter != "" {
		t.Fatalf("expected empty pickerFilter after over-backspace, got %q", m.pickerFilter)
	}
	// Exit search mode, then verify backspace in nav mode on empty buffer is a no-op.
	m = pressKey(m, "esc") // search → nav (filter already "")
	if m.pickerSearchMode {
		t.Fatal("expected pickerSearchMode=false after esc in search mode")
	}
	m = pressKey(m, "backspace") // nav mode, empty buffer — no-op
	if m.pickerFilter != "" {
		t.Fatalf("expected empty pickerFilter after backspace on empty in nav mode, got %q", m.pickerFilter)
	}
	if m.mode != ModePicker {
		t.Fatalf("backspace on empty buffer must not exit picker, mode=%d", m.mode)
	}
}

func TestPickerEscClearsBufferThenExits(t *testing.T) {
	m := pickerModel(t)

	// Enter search mode and type a filter.
	m = pressKey(m, "/")
	m = pressKey(m, "t")
	m = pressKey(m, "e")
	if m.pickerFilter == "" {
		t.Fatal("expected buffer populated before esc")
	}

	// Esc in search mode → exits search mode, filter KEPT, stays in picker.
	m = pressKey(m, "esc")
	if m.pickerSearchMode {
		t.Fatal("expected pickerSearchMode=false after esc in search mode")
	}
	if m.pickerFilter != "te" {
		t.Fatalf("expected filter 'te' kept after esc from search mode, got %q", m.pickerFilter)
	}
	if m.mode != ModePicker {
		t.Fatalf("expected to stay in ModePicker after esc from search mode, got %d", m.mode)
	}

	// Esc in nav mode with non-empty filter → clears filter, stays in picker.
	m = pressKey(m, "esc")
	if m.pickerFilter != "" {
		t.Fatalf("expected buffer cleared after esc in nav mode, got %q", m.pickerFilter)
	}
	if m.mode != ModePicker {
		t.Fatalf("expected to stay in ModePicker after filter-clear esc, got %d", m.mode)
	}
	if len(m.pickerFilteredIdx) != 4 {
		t.Fatalf("expected all 4 sessions visible after clear, got %d", len(m.pickerFilteredIdx))
	}

	// Esc in nav mode with empty filter → exit to ModeList.
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc with empty filter, got %d", m.mode)
	}
}

func TestPickerSelectionPersistsAcrossFilterChanges(t *testing.T) {
	m := pickerModel(t)
	// Default sort: personal, then work — cursor=0 is "dotfiles".
	first := m.pickerFilteredIdx[m.pickerCursor]
	m = pressKey(m, " ")
	if !m.pickerSelected[first] {
		t.Fatal("expected first row selected after space")
	}
	// Enter search mode and type a filter that hides the selected row.
	m = pressKey(m, "/")
	for _, k := range []string{"a", "c"} {
		m = pressKey(m, k)
	}
	for _, idx := range m.pickerFilteredIdx {
		if idx == first {
			t.Fatal("expected the toggled row to be filtered out by 'ac'")
		}
	}
	// Selection is keyed by original index, so it must persist.
	if !m.pickerSelected[first] {
		t.Fatal("selection must persist even when the row is filtered out")
	}
	// Exit search mode (filter kept), then clear filter.
	m = pressKey(m, "esc") // search → nav, filter "ac" kept
	m = pressKey(m, "esc") // nav + "ac" filter → clear filter
	if !m.pickerSelected[first] {
		t.Fatal("selection lost after clearing filter")
	}
	if len(m.pickerFilteredIdx) != 4 {
		t.Fatalf("expected all 4 sessions after filter cleared, got %d", len(m.pickerFilteredIdx))
	}
}

func TestPickerEnterLaunchesWhileFiltered(t *testing.T) {
	m := pickerModel(t)
	// Enter search mode, filter to one row, then exit search mode.
	m = pressKey(m, "/")
	for _, k := range []string{"t", "e", "s"} {
		m = pressKey(m, k)
	}
	if len(m.pickerFilteredIdx) != 1 {
		t.Fatalf("expected 1 visible row, got %d", len(m.pickerFilteredIdx))
	}
	m = pressKey(m, "esc") // exit search mode → nav (filter kept)
	m = pressKey(m, " ")   // select the visible row
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList {
		t.Fatalf("expected ModeList after enter, got %d", updated.(Model).mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil launch cmd after enter in picker")
	}
}

func TestPickerDSLAccountFilter(t *testing.T) {
	// Drive the DSL filter via applyPickerFilter directly to verify the underlying
	// semantics — the live-typing path (in search mode) is covered by TestPickerTypingNarrowsLive.
	sessions := []config.Session{
		{Title: "api", Account: "work"},
		{Title: "dotfiles", Account: "personal"},
		{Title: "notes", Account: "personal"},
	}
	got := applyPickerFilter(sessions, "@personal")
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("expected [1,2] for @personal, got %v", got)
	}
}

func TestPickerStickyInputRendered(t *testing.T) {
	m := pickerModel(t)

	// Initial state: nav mode, empty filter → hint text, no cursor.
	out := stripAnsi(renderPicker(m))
	if strings.Contains(out, "/█") {
		t.Errorf("expected no cursor block in initial render (not yet in search mode), got:\n%s", out)
	}
	if !strings.Contains(out, "press / to filter") {
		t.Errorf("expected hint 'press / to filter' in initial render, got:\n%s", out)
	}

	// After pressing `/`: search mode active → prompt with cursor.
	m = pressKey(m, "/")
	out = stripAnsi(renderPicker(m))
	if !strings.Contains(out, "/█") {
		t.Errorf("expected '/█' prompt after entering search mode, got:\n%s", out)
	}

	// After typing "te": buffer shown with cursor.
	m = pressKey(m, "t")
	m = pressKey(m, "e")
	out = stripAnsi(renderPicker(m))
	if !strings.Contains(out, "/te█") {
		t.Errorf("expected buffer 'te' rendered next to the prompt, got:\n%s", out)
	}
}

func TestPickerSlashEntersSearchMode(t *testing.T) {
	m := pickerModel(t)
	if m.pickerSearchMode {
		t.Fatal("expected pickerSearchMode=false initially")
	}
	m = pressKey(m, "/")
	if !m.pickerSearchMode {
		t.Fatal("expected pickerSearchMode=true after /")
	}
	// The slash itself must NOT be added to the filter buffer.
	if m.pickerFilter != "" {
		t.Fatalf("expected empty pickerFilter after /, got %q", m.pickerFilter)
	}
	if m.mode != ModePicker {
		t.Fatalf("expected to stay in ModePicker after /, got mode=%d", m.mode)
	}
}

func TestPickerNavModeIgnoresPrintableChars(t *testing.T) {
	m := pickerModel(t)
	// In nav mode, printable chars other than known shortcuts are silently ignored.
	m = pressKey(m, "t")
	m = pressKey(m, "e")
	m = pressKey(m, "s")
	if m.pickerFilter != "" {
		t.Fatalf("expected empty filter in nav mode after typing, got %q", m.pickerFilter)
	}
	if len(m.pickerFilteredIdx) != 4 {
		t.Fatalf("expected all 4 sessions unfiltered in nav mode, got %d", len(m.pickerFilteredIdx))
	}
}

func TestPickerFooterShowsFilterHint(t *testing.T) {
	m := pickerModel(t)
	m.width, m.height = 120, 40
	footer := renderFooter(m)
	if !strings.Contains(footer, "type to filter") {
		t.Errorf("expected picker footer to advertise live filter, got:\n%s", footer)
	}
	if !strings.Contains(footer, "esc: clear/exit") {
		t.Errorf("expected picker footer to advertise esc behavior, got:\n%s", footer)
	}
}

func TestApplyPickerFilterDSLSubstring(t *testing.T) {
	sessions := []config.Session{
		{Title: "api", Directory: "/home/user/api", Account: "work"},
		{Title: "backend", Directory: "/home/user/backend", Account: "work"},
		{Title: "dotfiles", Directory: "/home/user/dotfiles", Account: "personal"},
	}
	got := applyPickerFilter(sessions, "back")
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("expected [1] for 'back', got %v", got)
	}
}

func TestApplyPickerFilterDSLAccount(t *testing.T) {
	sessions := []config.Session{
		{Title: "api", Account: "work"},
		{Title: "dotfiles", Account: "personal"},
	}
	got := applyPickerFilter(sessions, "@personal")
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("expected [1] for '@personal', got %v", got)
	}
}

func TestApplyPickerFilterEmptyReturnsAll(t *testing.T) {
	sessions := []config.Session{
		{Title: "api", Account: "work"},
		{Title: "dotfiles", Account: "personal"},
	}
	got := applyPickerFilter(sessions, "")
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("expected [0,1] for empty query, got %v", got)
	}
}

// pickerPreviewCfg returns a Preview config with thresholds friendly for tests.
func pickerPreviewCfg() config.PreviewConfig {
	return config.PreviewConfig{
		Direction: "horizontal",
		Ratio:     0.4,
		RefreshMs: 1000,
		MinCols:   40,
		MinRows:   10,
	}
}

func TestPickerPreviewToggleP(t *testing.T) {
	m := pickerModel(t)
	m.width, m.height = 120, 40
	m.cfg.Defaults.Preview = pickerPreviewCfg()
	if m.pickerPreviewEnabled {
		t.Fatal("picker preview should start disabled")
	}
	if m.previewEnabled {
		t.Fatal("list preview should start disabled")
	}
	m = pressKey(m, "p")
	if !m.pickerPreviewEnabled {
		t.Fatal("expected pickerPreviewEnabled=true after p in picker")
	}
	if m.previewEnabled {
		t.Fatal("p in picker must not touch m.previewEnabled")
	}
	// `p` should not have landed in the filter buffer.
	if strings.Contains(m.pickerFilter, "p") {
		t.Errorf("expected p to act as toggle, not buffer append; pickerFilter=%q", m.pickerFilter)
	}
	m = pressKey(m, "p")
	if m.pickerPreviewEnabled {
		t.Fatal("expected pickerPreviewEnabled=false after second p in picker")
	}
	if m.previewEnabled {
		t.Fatal("second p in picker must not touch m.previewEnabled")
	}
}

func TestPickerPreviewRenderShowsConfigBlock(t *testing.T) {
	m := pickerModel(t)
	m.width, m.height = 120, 40
	m.cfg.Defaults.Preview = pickerPreviewCfg()
	m.pickerPreviewEnabled = true
	out := stripAnsi(renderPicker(m))
	// pickerCursor=0 → first sorted row: "dotfiles" (personal).
	if !strings.Contains(out, "dotfiles") {
		t.Errorf("expected highlighted config title in preview pane, got:\n%s", out)
	}
	if !strings.Contains(out, "dir:") {
		t.Errorf("expected dir label in preview pane, got:\n%s", out)
	}
	if !strings.Contains(out, "profile:") {
		t.Errorf("expected profile label in preview pane, got:\n%s", out)
	}
	if !strings.Contains(out, "personal") {
		t.Errorf("expected profile value in preview pane, got:\n%s", out)
	}
	if !strings.Contains(out, "harness:") {
		t.Errorf("expected harness label in preview pane, got:\n%s", out)
	}
}

func TestPickerProfileOverrideUpdatesLaunchProfile(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "C")
	if m.mode != ModeConfigPicker {
		t.Fatalf("expected ModeConfigPicker after C in picker, got %d", m.mode)
	}
	if m.configPickerReturnMode != ModePicker {
		t.Fatalf("expected config picker to return to ModePicker, got %d", m.configPickerReturnMode)
	}

	// Picker starts on "dotfiles" with default profile "personal". Move to work.
	m = pressKey(m, "k")
	m = pressKey(m, "enter")
	if m.mode != ModePicker {
		t.Fatalf("expected ModePicker after choosing override profile, got %d", m.mode)
	}
	if got := m.pickerEffectiveProfile(0); got != "work" {
		t.Fatalf("expected picker override to switch launch profile to work, got %q", got)
	}

	m.width, m.height = 120, 40
	m.cfg.Defaults.Preview = pickerPreviewCfg()
	m.pickerPreviewEnabled = true
	out := stripAnsi(renderPicker(m))
	if !strings.Contains(out, "(override)") {
		t.Errorf("expected picker row to show override marker, got:\n%s", out)
	}
	if !strings.Contains(out, "launch profile: work") {
		t.Errorf("expected preview to show launch profile after override, got:\n%s", out)
	}
}

func TestPickerPreviewRenderAutoDisableNarrow(t *testing.T) {
	m := pickerModel(t)
	cfg := pickerPreviewCfg()
	m.cfg.Defaults.Preview = cfg
	m.width, m.height = 30, 40 // below MinCols (40)
	m.pickerPreviewEnabled = true
	out := stripAnsi(renderPicker(m))
	if strings.Contains(out, "dir:") {
		t.Errorf("expected preview pane to auto-disable at narrow width, got:\n%s", out)
	}
}

func TestPickerPreviewRunningPeek(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2, Preview: pickerPreviewCfg()},
		Accounts: []config.Account{{Name: "work", Color: "blue"}},
		Sessions: []config.Session{
			{Title: "api", Directory: "/home/user/api", Account: "work"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 99, Base: "W:api", Account: "work"},
	})
	m = pressKey(m, "l")
	m.width, m.height = 120, 40
	m.pickerPreviewEnabled = true
	m.previewWindowID = 99
	m.previewText = "alpha\nbeta\ngamma"
	out := stripAnsi(renderPicker(m))
	if !strings.Contains(out, "── running ──") {
		t.Errorf("expected running divider when picker highlight matches a running session, got:\n%s", out)
	}
	if !strings.Contains(out, "gamma") {
		t.Errorf("expected scrollback tail in preview pane, got:\n%s", out)
	}
}

func TestPickerPreviewMovementRefetchesWindow(t *testing.T) {
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2, Preview: pickerPreviewCfg()},
		Accounts: []config.Account{{Name: "work", Color: "blue"}, {Name: "personal", Color: "green"}},
		Sessions: []config.Session{
			{Title: "api", Account: "work"},
			{Title: "notes", Account: "personal"},
		},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, WindowID: 11, Base: "W:api", Account: "work"},
		{TabID: 2, WindowID: 22, Base: "P:notes", Account: "personal"},
	})
	m.width, m.height = 120, 40
	m = pressKey(m, "l")
	// Sorted: personal first ("notes") then work ("api"). Cursor=0 → notes.
	if !m.pickerPreviewEnabled {
		// Enable preview from within the picker.
		m = pressKey(m, "p")
	}
	if m.previewWindowID != 22 {
		t.Errorf("expected previewWindowID=22 (notes) on picker entry, got %d", m.previewWindowID)
	}
	// Move cursor → highlight api → preview should track its window.
	updated, cmd := m.Update(keyPress("j"))
	m = updated.(Model)
	if m.previewWindowID != 11 {
		t.Errorf("expected previewWindowID=11 (api) after j, got %d", m.previewWindowID)
	}
	if cmd == nil {
		t.Error("expected non-nil fetch cmd after picker cursor moved to a different window")
	}
}

// ---- Empty-filter cursor safety tests (picker) ----
//
// Every `m.pickerFilteredIdx[m.pickerCursor]` access in handlePickerNavKey
// is guarded by either `len(m.pickerFilteredIdx) > 0` or a clamp returning
// the low bound when the slice is empty. These tests drive every key
// against a filter that matches zero rows and assert no panic, the picker
// stays responsive, and backspace fully restores the navigable list.

// pressKeyPickerSafe wraps pressKey with a recover so a panic on one key
// is reported but doesn't abort the rest of the suite.
func pressKeyPickerSafe(t *testing.T, m Model, key string) Model {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic on key %q in picker: %v", key, r)
		}
	}()
	return pressKey(m, key)
}

// TestPickerEmptyFilterNavigationIsSafe drives every navigation key after
// narrowing the picker filter to zero rows. Excludes `enter` because it
// intentionally exits to ModeList — that path is covered separately.
func TestPickerEmptyFilterNavigationIsSafe(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "/")
	for _, ch := range "zzzz" {
		m = pressKey(m, string(ch))
	}
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("setup: expected 0 matches for 'zzzz', got %d", len(m.pickerFilteredIdx))
	}
	m = pressKey(m, "esc") // exit search mode; filter kept
	if m.pickerSearchMode {
		t.Fatal("setup: expected pickerSearchMode=false after esc from search")
	}
	if m.mode != ModePicker {
		t.Fatalf("setup: expected ModePicker after esc from search, got %d", m.mode)
	}
	for _, k := range []string{"j", "k", " ", "o", "C"} {
		m = pressKeyPickerSafe(t, m, k)
		if m.mode != ModePicker {
			t.Errorf("expected to stay in ModePicker after %q with empty filter, got mode=%d", k, m.mode)
			m.mode = ModePicker
		}
		if m.pickerCursor < 0 {
			t.Errorf("pickerCursor went negative after %q: %d", k, m.pickerCursor)
			m.pickerCursor = 0
		}
	}
}

// TestPickerEnterOnEmptyFilterExitsCleanly drives `enter` while the picker
// filter matches zero rows. With nothing selectable the launch is a no-op,
// but the handler must still cleanly exit to ModeList and not panic.
func TestPickerEnterOnEmptyFilterExitsCleanly(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "/")
	for _, ch := range "zzzz" {
		m = pressKey(m, string(ch))
	}
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("setup: expected 0 matches, got %d", len(m.pickerFilteredIdx))
	}
	m = pressKey(m, "esc") // search → nav
	updated, cmd := m.Update(keyPress("enter"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Errorf("expected ModeList after enter on empty-filter picker, got %d", m2.mode)
	}
	if cmd != nil {
		t.Errorf("expected nil cmd from empty-selection launch, got non-nil")
	}
}

// TestPickerBackspaceRecoversFromEmptyFilter verifies the documented
// recovery path: when the filter collapses the list to zero rows, every
// backspace re-applies the filter and resets pickerCursor to 0 so the user
// can navigate the moment matches return.
func TestPickerBackspaceRecoversFromEmptyFilter(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "/")
	for _, ch := range "zzzz" {
		m = pressKey(m, string(ch))
	}
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("setup: expected 0 matches, got %d", len(m.pickerFilteredIdx))
	}
	// Pre-move the cursor in nav mode to confirm backspace resets it.
	m = pressKey(m, "esc") // search → nav, filter "zzzz" kept
	m.pickerCursor = 99    // simulate a stale cursor value
	// Backspace fires in nav mode while the filter still has content.
	for i := 0; i < 4; i++ {
		m = pressKey(m, "backspace")
		if m.pickerCursor != 0 {
			t.Errorf("expected pickerCursor reset to 0 after backspace #%d, got %d", i+1, m.pickerCursor)
		}
	}
	if m.pickerFilter != "" {
		t.Errorf("expected empty filter after 4 backspaces, got %q", m.pickerFilter)
	}
	if len(m.pickerFilteredIdx) != 4 {
		t.Fatalf("expected 4 visible rows after filter cleared, got %d", len(m.pickerFilteredIdx))
	}
	// Now navigation must work normally again.
	m = pressKey(m, "j")
	if m.pickerCursor != 1 {
		t.Errorf("expected pickerCursor=1 after j with non-empty list, got %d", m.pickerCursor)
	}
}

// TestPickerEmptyFilterPreviewSafe ensures the picker preview path also
// tolerates an empty filtered index — pickerCurrentWindowID guards before
// dereferencing the cursor.
func TestPickerEmptyFilterPreviewSafe(t *testing.T) {
	m := pickerModel(t)
	m.width, m.height = 120, 40
	m.cfg.Defaults.Preview = pickerPreviewCfg()
	m.pickerPreviewEnabled = true
	m = pressKey(m, "/")
	for _, ch := range "zzzz" {
		m = pressKey(m, string(ch))
	}
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("setup: expected 0 matches, got %d", len(m.pickerFilteredIdx))
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("picker preview panicked on empty filter: %v", r)
		}
	}()
	_ = renderPicker(m)
}
