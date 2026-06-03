package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

// ---- Components / RemoveComponent ----

func TestFilterComponentsOrder(t *testing.T) {
	f := Parse("api backend @work !key")
	comps := f.Components()
	if len(comps) != 4 {
		t.Fatalf("expected 4 components, got %d (%v)", len(comps), comps)
	}
	wantLabels := []string{"/api", "/backend", "@work", "!key"}
	wantKinds := []chipKind{chipSubstring, chipSubstring, chipAccount, chipState}
	for i, c := range comps {
		if c.label != wantLabels[i] {
			t.Errorf("comp[%d].label = %q, want %q", i, c.label, wantLabels[i])
		}
		if c.kind != wantKinds[i] {
			t.Errorf("comp[%d].kind = %d, want %d", i, c.kind, wantKinds[i])
		}
	}
}

func TestFilterRemoveSubstringMiddle(t *testing.T) {
	// Remove the second substring out of three components total: substring,
	// substring, account. Result must drop only the targeted substring.
	f := Parse("api backend @work")
	out := f.RemoveComponent(1)
	if !strings.EqualFold(strings.Join(out.Substrings, ","), "api") {
		t.Errorf("expected Substrings=[api], got %v", out.Substrings)
	}
	if out.Account != "work" {
		t.Errorf("expected Account preserved, got %q", out.Account)
	}
	if out.Raw != "api @work" {
		t.Errorf("expected Raw re-serialized to %q, got %q", "api @work", out.Raw)
	}
}

func TestFilterRemoveAccount(t *testing.T) {
	f := Parse("api @work !key")
	out := f.RemoveComponent(1) // account chip
	if out.Account != "" {
		t.Errorf("expected Account cleared, got %q", out.Account)
	}
	if len(out.Substrings) != 1 || out.Substrings[0] != "api" {
		t.Errorf("expected substrings preserved, got %v", out.Substrings)
	}
	if out.State != "key" {
		t.Errorf("expected State preserved, got %q", out.State)
	}
}

func TestFilterRemoveState(t *testing.T) {
	f := Parse("api @work !key")
	out := f.RemoveComponent(2) // state chip
	if out.State != "" {
		t.Errorf("expected State cleared, got %q", out.State)
	}
}

func TestFilterRemoveOutOfRangeNoOp(t *testing.T) {
	f := Parse("api")
	out := f.RemoveComponent(5)
	if len(out.Substrings) != 1 {
		t.Errorf("expected unchanged filter, got %v", out.Substrings)
	}
}

func TestSerializeFilterEscapesLeadingMarkers(t *testing.T) {
	f := Filter{Substrings: []string{"@literal", "!banged"}}
	got := serializeFilter(f)
	if got != `\@literal \!banged` {
		t.Errorf("expected escapes, got %q", got)
	}
	// Round-trip must yield the original substrings.
	if rt := Parse(got); rt.Substrings[0] != "@literal" || rt.Substrings[1] != "!banged" {
		t.Errorf("escape round-trip failed: %v", rt.Substrings)
	}
}

// ---- Render: chip line ----

func chipTestModel() Model {
	cfg := &config.Config{
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
			{Name: "personal", Color: "yellow"},
		},
	}
	m := Model{cfg: cfg, width: 120, focusedChipIdx: -1}
	m.sessions = []session.RunningSession{
		{Base: "x", State: "bolt", Account: "work"},
	}
	return m
}

func TestRenderChipsEmptyCollapses(t *testing.T) {
	m := chipTestModel()
	if got := renderChips(m, m.width); got != "" {
		t.Errorf("expected empty chip line when no filter, got %q", got)
	}
}

func TestRenderChipsOne(t *testing.T) {
	m := chipTestModel()
	m.activeFilter = Parse("api")
	out := stripAnsi(renderChips(m, m.width))
	if !strings.Contains(out, "▌/api ✕") {
		t.Errorf("expected substring chip body, got %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Errorf("expected single trailing newline, got %q", out)
	}
}

func TestRenderChipsAllKindsOnOneLine(t *testing.T) {
	m := chipTestModel()
	m.activeFilter = Parse("api @work !key")
	out := stripAnsi(renderChips(m, m.width))
	for _, want := range []string{"▌/api ✕", "▌@work ✕", "▌!key ✕"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in chip line, got %q", want, out)
		}
	}
	if strings.Count(strings.TrimRight(out, "\n"), "\n") != 0 {
		t.Errorf("expected chips on a single visual line at width=120, got %q", out)
	}
}

func TestRenderChipsWrapAtNarrowWidth(t *testing.T) {
	m := chipTestModel()
	m.activeFilter = Parse("aaaa bbbb cccc dddd")
	// Each chip is ~9 cells (▌/aaaa ✕). Set width tight enough to force wrap.
	out := stripAnsi(renderChips(m, 25))
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 2 {
		t.Errorf("expected chips to wrap onto multiple lines at width=25, got:\n%s", out)
	}
}

// ---- Logic: keys and focus ----

func TestNumericKeyRemovesChip(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = Parse("api @work !key") // 3 components: /api, @work, !key
	m = pressKey(m, "2")                     // remove @work
	if m.activeFilter.Account != "" {
		t.Errorf("expected account removed, got %q", m.activeFilter.Account)
	}
	if len(m.activeFilter.Substrings) != 1 || m.activeFilter.Substrings[0] != "api" {
		t.Errorf("expected substrings preserved, got %v", m.activeFilter.Substrings)
	}
	if m.activeFilter.State != "key" {
		t.Errorf("expected state preserved, got %q", m.activeFilter.State)
	}
}

func TestNumericKeyOutOfRangeNoOp(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = Parse("api") // 1 component
	m = pressKey(m, "5")
	if len(m.activeFilter.Substrings) != 1 {
		t.Errorf("expected unchanged filter, got %v", m.activeFilter.Substrings)
	}
}

func TestTabCyclesFocus(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = Parse("api @work !key")
	if m.focusedChipIdx != -1 {
		t.Fatalf("expected initial focus=-1, got %d", m.focusedChipIdx)
	}
	m = pressKey(m, "tab")
	if m.focusedChipIdx != 0 {
		t.Errorf("expected focus=0 after first tab, got %d", m.focusedChipIdx)
	}
	m = pressKey(m, "tab")
	if m.focusedChipIdx != 1 {
		t.Errorf("expected focus=1 after second tab, got %d", m.focusedChipIdx)
	}
	m = pressKey(m, "tab")
	if m.focusedChipIdx != 2 {
		t.Errorf("expected focus=2 after third tab, got %d", m.focusedChipIdx)
	}
	m = pressKey(m, "tab")
	if m.focusedChipIdx != 0 {
		t.Errorf("expected wrap to focus=0 after fourth tab, got %d", m.focusedChipIdx)
	}
}

func TestTabNoFiltersNoOp(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "tab")
	if m.focusedChipIdx != -1 {
		t.Errorf("expected focus to remain -1 without chips, got %d", m.focusedChipIdx)
	}
}

func TestXWithFocusedChipPreservesFilterAndClearsStatus(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.filtered[0].State = "key"
	m.activeFilter = Parse("api @work")
	m = pressKey(m, "tab") // focus /api
	if m.focusedChipIdx != 0 {
		t.Fatalf("setup: expected focus=0, got %d", m.focusedChipIdx)
	}
	updated, cmd := m.Update(keyPress("x"))
	m2 := updated.(Model)
	if len(m2.activeFilter.Substrings) != 1 {
		t.Errorf("expected substring preserved, got %v", m2.activeFilter.Substrings)
	}
	if m2.activeFilter.Account != "work" {
		t.Errorf("expected account preserved, got %q", m2.activeFilter.Account)
	}
	if cmd == nil {
		t.Fatal("expected x to dispatch clear-status cmd")
	}
	if m2.mode == ModeConfirm {
		t.Errorf("expected x to clear status, not open close-confirm overlay")
	}
}

func TestBackspaceRemovesFocusedChip(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = Parse("api @work")
	m = pressKey(m, "tab") // focus /api
	m = pressKey(m, "backspace")
	if len(m.activeFilter.Substrings) != 0 {
		t.Errorf("expected substring removed via backspace, got %v", m.activeFilter.Substrings)
	}
}

func TestUppercaseXWithoutFocusedChipOpensConfirm(t *testing.T) {
	// Uppercase X remains the direct close-session shortcut when no chip is focused.
	// Tested under the legacy confirm flow so the assertion checks ModeConfirm;
	// the toast/undo default is exercised in undo_test.go.
	m := loadSessions(modalConfirmModel(), makeSessions())
	m.activeFilter = Parse("api")
	// No tab pressed → focusedChipIdx remains -1.
	updated, _ := m.Update(keyPress("X"))
	m2 := updated.(Model)
	if m2.mode != ModeConfirm {
		t.Errorf("expected X to open close-confirm, got mode=%d", m2.mode)
	}
}

func TestRenderFocusedChipShowsBracketedRemove(t *testing.T) {
	m := chipTestModel()
	m.activeFilter = Parse("api @work")
	m.focusedChipIdx = 1 // focus @work
	out := stripAnsi(renderChips(m, m.width))
	if !strings.Contains(out, "▌@work [✕]") {
		t.Errorf("expected focused chip to show [✕], got %q", out)
	}
	if !strings.Contains(out, "▌/api ✕") {
		t.Errorf("expected unfocused chip to retain plain ✕, got %q", out)
	}
}

func TestEnterOnFocusedChipRemoves(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = Parse("api @work")
	m = pressKey(m, "tab") // focus first chip (/api)
	updated, cmd := m.Update(keyPress("enter"))
	if cmd != nil {
		t.Errorf("expected no focusCmd when removing chip, got non-nil cmd")
	}
	m = updated.(Model)
	if len(m.activeFilter.Substrings) != 0 {
		t.Errorf("expected substring removed, got %v", m.activeFilter.Substrings)
	}
	if m.activeFilter.Account != "work" {
		t.Errorf("expected account preserved, got %q", m.activeFilter.Account)
	}
	if m.focusedChipIdx != -1 {
		t.Errorf("expected focus reset after chip removal, got %d", m.focusedChipIdx)
	}
}

func TestEnterWithoutFocusedChipFocusesTab(t *testing.T) {
	// Enter on a row focuses the session's tab. The actions overlay moved to
	// `a` (asserted in TestAOpensActionsOverlay) — chip-focus enter still
	// removes the focused chip (asserted in TestEnterOnFocusedChipRemoves).
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = Parse("api")
	updated, cmd := m.Update(keyPress("enter"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Fatalf("expected ModeList after enter, got %d", m2.mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil focusCmd after enter")
	}
}

func TestEscClearsChipsAndFocus(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = Parse("api @work")
	m = pressKey(m, "tab")
	if m.focusedChipIdx != 0 {
		t.Fatalf("setup: expected focus=0, got %d", m.focusedChipIdx)
	}
	m = pressKey(m, "esc")
	if !m.activeFilter.IsEmpty() {
		t.Errorf("expected filter cleared, got %+v", m.activeFilter)
	}
	if m.focusedChipIdx != -1 {
		t.Errorf("expected focus cleared, got %d", m.focusedChipIdx)
	}
}

// ---- Footer hint ----

func TestFooterShowsChipHintsWhenFiltered(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.activeFilter = Parse("api @work")
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "1-2: remove chip") {
		t.Errorf("expected chip count hint, got:\n%s", footer)
	}
	if !strings.Contains(footer, "tab: focus next") {
		t.Errorf("expected tab hint, got:\n%s", footer)
	}
}

func TestFooterOmitsChipHintsWithoutFilter(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	footer := stripAnsi(renderFooter(m))
	if strings.Contains(footer, "remove chip") {
		t.Errorf("expected no chip hint without active filter, got:\n%s", footer)
	}
}
