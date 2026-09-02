package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/dashboard"
)

// TestUpdateUnknownMsgIsNoop verifies the Update switch's terminal default
// branch: an unrecognized message type must leave the model unchanged and
// return no command. Regression guard for the msg.go ↔ Update.case mapping
// — if a future msg type is added without a handler, this test still
// passes but the new type's bug becomes visible elsewhere.
func TestUpdateUnknownMsgIsNoop(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	type unknownMsg struct{}
	before := m
	updated, cmd := m.Update(unknownMsg{})
	got := updated.(Model)
	if cmd != nil {
		t.Errorf("expected nil cmd for unknown msg, got %T", cmd)
	}
	if got.mode != before.mode {
		t.Errorf("unknown msg changed mode: %d → %d", before.mode, got.mode)
	}
	if got.cursor != before.cursor {
		t.Errorf("unknown msg moved cursor: %d → %d", before.cursor, got.cursor)
	}
}

// TestModeRoundTripBackToList exercises a representative round-trip from
// ModeList through every overlay/picker mode and back to ModeList via the
// documented exit keys. Catches regressions where an exit key stops
// restoring ModeList for any single mode.
func TestModeRoundTripBackToList(t *testing.T) {
	cases := []struct {
		name    string
		openKey string
		// extraEsc covers modes where opening immediately shows an overlay
		// inside the mode, so the first esc closes the overlay and the
		// second exits the mode.
		extraEsc bool
	}{
		{"search via /", "/", false},
		{"help via ?", "?", false},
		{"actions via a", "a", false},
		{"config-picker via c", "c", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := loadSessions(testModel(), makeSessions())
			m = pressKey(m, tc.openKey)
			if m.mode == ModeList {
				t.Fatalf("opening %q did not change mode away from ModeList", tc.openKey)
			}
			m = pressKey(m, "esc")
			if tc.extraEsc {
				m = pressKey(m, "esc")
			}
			if m.mode != ModeList {
				t.Fatalf("after opening with %q and esc'ing, expected ModeList, got %d", tc.openKey, m.mode)
			}
		})
	}
}

// TestPickerEscClearsThenExits exercises the documented two-stage Esc in
// the picker: first Esc clears a non-empty filter, second Esc exits to
// ModeList. Regression guard against either stage swallowing the wrong key.
func TestPickerEscClearsThenExits(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", Color: "blue"},
		},
		Sessions: []config.Session{
			{Title: "api", Account: "work"},
			{Title: "backend", Account: "work"},
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
	// Enter search mode and type a query.
	m = pressKey(m, "/")
	m = pressKey(m, "a")
	m = pressKey(m, "p")
	m = pressKey(m, "i")
	// Exit search mode (filter remains live-applied).
	m = pressKey(m, "enter")
	if m.pickerFilter == "" {
		t.Fatal("expected pickerFilter populated after typing")
	}
	// First Esc: clear filter, stay in picker.
	m = pressKey(m, "esc")
	if m.mode != ModePicker {
		t.Fatalf("first esc must keep ModePicker, got %d", m.mode)
	}
	if m.pickerFilter != "" {
		t.Errorf("first esc must clear pickerFilter, got %q", m.pickerFilter)
	}
	// Second Esc: exit picker.
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("second esc must exit to ModeList, got %d", m.mode)
	}
}

// TestPickerSpaceWithEmptyFilteredIdxIsSafe verifies that pressing space
// in the picker when the live filter has produced zero matches does not
// panic on an out-of-range pickerCursor and does not mutate selection.
func TestPickerSpaceWithEmptyFilteredIdxIsSafe(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", Color: "blue"}},
		Sessions:      []config.Session{{Title: "api", Account: "work"}},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, nil)
	m = pressKey(m, "l")
	// Narrow to zero matches.
	m = pressKey(m, "/")
	for _, r := range "zzzzz" {
		m = pressKey(m, string(r))
	}
	m = pressKey(m, "enter")
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("expected zero filtered matches, got %d", len(m.pickerFilteredIdx))
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("space on empty filtered list panicked: %v", r)
		}
	}()
	updated, cmd := m.Update(keyPress(" "))
	if cmd != nil {
		t.Errorf("expected nil cmd from space on empty filter, got %T", cmd)
	}
	got := updated.(Model)
	for k, v := range got.pickerSelected {
		if v {
			t.Errorf("expected no selection from empty-filter space, got idx=%d", k)
		}
	}
}

// TestHandleListKeyTabCyclesChips verifies that tab cycles through the
// active filter's chip indices and wraps around. Catches the case where
// tab fails to wrap or jumps past Components()-1.
func TestHandleListKeyTabCyclesChips(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	// Apply a filter with three components directly so the chip focus path is
	// independent of search input details.
	m.activeFilter = dashboard.Parse("api @work !bolt")
	m.filtered = dashboard.ApplyFilter(m.sessions, m.activeFilter, m.lastActivity, m.idleThreshold)
	if got := len(m.activeFilter.Components()); got != 3 {
		t.Fatalf("expected 3 chips, got %d (raw=%q)", got, m.activeFilter.Raw)
	}
	if m.focusedChipIdx != -1 {
		t.Fatalf("expected focusedChipIdx=-1 on return to list, got %d", m.focusedChipIdx)
	}
	m = pressKey(m, "tab")
	if m.focusedChipIdx != 0 {
		t.Fatalf("first tab → focusedChipIdx=0, got %d", m.focusedChipIdx)
	}
	m = pressKey(m, "tab")
	if m.focusedChipIdx != 1 {
		t.Fatalf("second tab → focusedChipIdx=1, got %d", m.focusedChipIdx)
	}
	m = pressKey(m, "tab")
	if m.focusedChipIdx != 2 {
		t.Fatalf("third tab → focusedChipIdx=2, got %d", m.focusedChipIdx)
	}
	m = pressKey(m, "tab")
	if m.focusedChipIdx != 0 {
		t.Fatalf("fourth tab must wrap to 0, got %d", m.focusedChipIdx)
	}
}

// TestNavOnEmptyListIsSafe pins that j/k/g/G on a model with no filtered
// rows never produce a negative cursor and never panic. clamp's lo-wins
// semantics matter here — with hi<lo (because len-1 == -1) the function
// must return lo (0).
func TestNavOnEmptyListIsSafe(t *testing.T) {
	m := loadSessions(testModel(), nil)
	if len(m.filtered) != 0 {
		t.Fatalf("expected empty filtered, got %d", len(m.filtered))
	}
	for _, k := range []string{"j", "k", "g", "G"} {
		m = pressKey(m, k)
		if m.cursor != 0 {
			t.Errorf("after %q on empty list, cursor=%d (want 0)", k, m.cursor)
		}
	}
}

// TestSearchEmptyInputMatchesAll verifies that entering search mode with
// no text typed (or after deleting every char) keeps every session
// visible. Recurring case-sensitivity / empty-input class of bugs.
func TestSearchEmptyInputMatchesAll(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	full := len(m.filtered)
	m = pressKey(m, "/")
	m = pressKey(m, "x") // narrow
	m = pressKey(m, "backspace")
	if got := len(m.filtered); got != full {
		t.Fatalf("after typing+deleting in search, expected %d rows, got %d", full, got)
	}
	m = pressKey(m, "enter")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after enter, got %d", m.mode)
	}
	if got := len(m.filtered); got != full {
		t.Fatalf("after committing empty search, expected %d rows, got %d", full, got)
	}
}

// TestSearchSpecialCharLeadingSigilLiteral verifies that the backslash
// escape syntax lets a leading sigil character pass through to the
// substring matcher. Recurring DSL parsing class of bugs.
func TestSearchSpecialCharLeadingSigilLiteral(t *testing.T) {
	m := loadSessions(testModel(), []session.RunningSession{
		{TabID: 1, Base: "@oddly-named", Account: "work", Directory: "/x"},
		{TabID: 2, Base: "ordinary", Account: "work", Directory: "/y"},
	})
	m = pressKey(m, "/")
	for _, r := range `\@oddly` {
		m = pressKey(m, string(r))
	}
	// Both substrings & no account filter should result; only the @-prefixed
	// row matches.
	if got := len(m.filtered); got != 1 {
		t.Fatalf("expected 1 row matching escaped sigil, got %d (raw=%q)", got, m.activeFilter.Raw)
	}
	if m.activeFilter.Account != "" {
		t.Errorf("escaped @ must not populate Account, got %q", m.activeFilter.Account)
	}
}

// TestHandleKeyDispatchByMode is a table-driven smoke test that maps each
// mode to the handler it routes to, sanity-checking that no mode falls
// through to the default (ModeList) branch by accident.
func TestHandleKeyDispatchByMode(t *testing.T) {
	for _, mode := range []Mode{
		ModeList,
		ModeSearch,
		ModeConfirm,
		ModePicker,
		ModeConfigPicker,
		ModeHelp,
		ModeActions,
		ModeCommandPalette,
		ModeWorktrees,
		ModeLiveSessionPicker,
	} {
		t.Run(modeName(mode), func(t *testing.T) {
			m := loadSessions(testModel(), makeSessions())
			m.mode = mode
			// ctrl+c must always quit, regardless of mode. Use as a
			// uniform dispatch probe — if the handler is wrong, ctrl+c
			// won't produce tea.Quit.
			_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
			if cmd == nil {
				t.Fatalf("ctrl+c in mode %d returned nil cmd; expected tea.Quit", mode)
			}
		})
	}
}

// TestSpaceInListFocuses verifies the bare space shortcut remains a row action
// now that dashboard filtering only starts after /.
func TestSpaceInListFocuses(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	_, cmd := m.Update(keyPress(" "))
	if cmd == nil {
		t.Fatal("space on a non-empty list must return commands.Focus")
	}
}

// TestCtrlCQuitsFromSearchMode pins that ctrl+c quits from inside the
// search input. The handler used to lack a keyCtrlC case, so msg.Text
// (empty for ctrl combos) fell through to the default branch and the
// keystroke was silently swallowed — leaving the user stuck.
func TestCtrlCQuitsFromSearchMode(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m = pressKey(m, "/")
	if m.mode != ModeSearch {
		t.Fatalf("expected ModeSearch, got %d", m.mode)
	}
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if cmd == nil {
		t.Fatal("ctrl+c in ModeSearch returned nil; expected tea.Quit")
	}
}

// TestCtrlCQuitsFromPickerSearchMode pins the same fix for the picker's
// search submode. Same handler shape, same bug, same fix.
func TestCtrlCQuitsFromPickerSearchMode(t *testing.T) {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts:      []config.Account{{Name: "work", Color: "blue"}},
		Sessions:      []config.Session{{Title: "api", Account: "work"}},
	}
	runner := kitty.NewMockRunner(nil)
	client := kitty.NewClientWithSocket(runner, "")
	m := NewModel(cfg, client)
	m = loadSessions(m, nil)
	m = pressKey(m, "l")
	m = pressKey(m, "/")
	if !m.pickerSearchMode {
		t.Fatal("expected picker search mode active after /")
	}
	_, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if cmd == nil {
		t.Fatal("ctrl+c in picker search mode returned nil; expected tea.Quit")
	}
}

func modeName(m Mode) string {
	switch m {
	case ModeList:
		return "ModeList"
	case ModeSearch:
		return "ModeSearch"
	case ModeConfirm:
		return "ModeConfirm"
	case ModePicker:
		return "ModePicker"
	case ModeConfigPicker:
		return "ModeConfigPicker"
	case ModeHelp:
		return "ModeHelp"
	case ModeActions:
		return "ModeActions"
	case ModeCommandPalette:
		return "ModeCommandPalette"
	case ModeWorktrees:
		return "ModeWorktrees"
	case ModeLiveSessionPicker:
		return "ModeLiveSessionPicker"
	}
	return "Mode?"
}
