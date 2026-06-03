package tui

import (
	"slices"
	"strings"
	"testing"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/config"
)

func projectChipPickerSearchModel(t *testing.T) Model {
	t.Helper()
	m := sessionSearchFilterModel(t)
	m.cfg.Projects = []config.Session{
		{Title: "Foo"},
		{Title: "Bar"},
		{Title: "baz"},
	}
	m.cfg.Profiles = []config.Account{{Name: "work"}}
	return m
}

func enterChipPickerForTest(t *testing.T, m Model, key string, want chipPickerKind) Model {
	t.Helper()
	updated, cmd := m.Update(keyPress(key))
	if cmd != nil {
		t.Fatalf("enter chip picker key %q returned cmd %T, want nil", key, cmd)
	}
	next := updated.(Model)
	if next.mode != ModeChipPicker {
		t.Fatalf("after %q: mode=%v, want ModeChipPicker", key, next.mode)
	}
	if next.chipPickerKind != want {
		t.Fatalf("after %q: chipPickerKind=%v, want %v", key, next.chipPickerKind, want)
	}
	return next
}

func enterProjectChipPickerForTest(t *testing.T, m Model) Model {
	t.Helper()
	return enterChipPickerForTest(t, m, "r", chipPickerProject)
}

func assertChipPickerOptions(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("chipPickerOptions=%#v, want %#v", got, want)
	}
}

func TestProjectChipPickerSlashStartsSearchWithoutSessionDispatch(t *testing.T) {
	m := enterProjectChipPickerForTest(t, projectChipPickerSearchModel(t))
	seq := m.sessionSearchSeq

	updated, cmd := m.Update(keyPress("/"))
	m = updated.(Model)

	if cmd != nil {
		t.Fatalf("project picker / returned cmd %T, want nil", cmd)
	}
	if !m.chipPickerProjectSearchMode {
		t.Fatal("project picker / did not enter project search mode")
	}
	if m.chipPickerProjectQuery != "" {
		t.Fatalf("chipPickerProjectQuery=%q, want empty", m.chipPickerProjectQuery)
	}
	if m.sessionSearchSeq != seq {
		t.Fatalf("sessionSearchSeq=%d, want unchanged %d", m.sessionSearchSeq, seq)
	}
	if m.sessionLoading {
		t.Fatal("project picker / marked session search loading; want no dispatch")
	}
}

func TestProjectChipPickerQueryFiltersCaseInsensitivelyAndHidesAny(t *testing.T) {
	m := enterProjectChipPickerForTest(t, projectChipPickerSearchModel(t))
	m = pressKey(m, "/")
	m = pressKey(m, "f")
	m = pressKey(m, "O")

	if m.chipPickerProjectQuery != "fO" {
		t.Fatalf("chipPickerProjectQuery=%q, want fO", m.chipPickerProjectQuery)
	}
	assertChipPickerOptions(t, m.chipPickerOptions, []string{"Foo"})

	out := stripAnsi(renderChipPicker(m))
	if strings.Contains(out, "(any)") {
		t.Fatalf("filtered project picker rendered (any); output:\n%s", out)
	}
	if !strings.Contains(out, "Foo") {
		t.Fatalf("filtered project picker did not render Foo; output:\n%s", out)
	}
}

func TestProjectChipPickerSearchExitKeysPreserveFilteredOptions(t *testing.T) {
	for _, key := range []string{"esc", "enter"} {
		t.Run(key, func(t *testing.T) {
			m := enterProjectChipPickerForTest(t, projectChipPickerSearchModel(t))
			m = pressKey(m, "/")
			m = pressKey(m, "f")
			m = pressKey(m, "o")

			updated, cmd := m.Update(keyPress(key))
			m = updated.(Model)

			if cmd != nil {
				t.Fatalf("%s from project search mode returned cmd %T, want nil", key, cmd)
			}
			if m.mode != ModeChipPicker {
				t.Fatalf("%s from project search mode changed mode to %v, want ModeChipPicker", key, m.mode)
			}
			if m.chipPickerProjectSearchMode {
				t.Fatalf("%s from project search mode left search mode active", key)
			}
			if m.chipPickerProjectQuery != "fo" {
				t.Fatalf("chipPickerProjectQuery=%q, want fo", m.chipPickerProjectQuery)
			}
			assertChipPickerOptions(t, m.chipPickerOptions, []string{"Foo"})
		})
	}
}

func TestProjectChipPickerSecondEnterSelectsAndDispatchesSearch(t *testing.T) {
	m := enterProjectChipPickerForTest(t, projectChipPickerSearchModel(t))
	m = pressKey(m, "/")
	m = pressKey(m, "f")
	m = pressKey(m, "o")
	m = pressKey(m, "enter")
	if m.chipPickerProjectSearchMode {
		t.Fatal("first enter should leave project search mode")
	}

	updated, cmd := m.Update(keyPress("enter"))
	m = updated.(Model)

	if cmd == nil {
		t.Fatal("second enter returned nil cmd, want debounced session search")
	}
	if m.mode != ModeSessionSearch {
		t.Fatalf("mode=%v, want ModeSessionSearch", m.mode)
	}
	if !slices.Equal(m.filterProject, []string{"Foo"}) {
		t.Fatalf("filterProject=%#v, want Foo", m.filterProject)
	}
	if !m.sessionLoading {
		t.Fatal("selecting project did not mark session search loading")
	}

	updated, searchCmd := m.Update(sessionSearchDebounceMsg{seq: m.sessionSearchSeq})
	m = updated.(Model)
	doneMsg, ok := drainCmd(t, searchCmd).(sessionSearchDoneMsg)
	if !ok {
		t.Fatalf("project selection debounce produced %T, want sessionSearchDoneMsg", doneMsg)
	}
	if doneMsg.err != nil {
		t.Fatalf("project-filtered search returned error: %v", doneMsg.err)
	}

	updated, _ = m.Update(doneMsg)
	m = updated.(Model)
	if len(m.sessionResults) != 1 {
		t.Fatalf("len(sessionResults)=%d, want 1", len(m.sessionResults))
	}
	if m.sessionResults[0].Meta.Project != "Foo" {
		t.Fatalf("selected project search returned project %q, want Foo", m.sessionResults[0].Meta.Project)
	}
}

func TestProjectChipPickerNoMatchStateIsSafe(t *testing.T) {
	m := enterProjectChipPickerForTest(t, projectChipPickerSearchModel(t))
	m = pressKey(m, "/")
	m = typeString(m, "zzzz")

	if len(m.chipPickerOptions) != 0 {
		t.Fatalf("len(chipPickerOptions)=%d, want 0", len(m.chipPickerOptions))
	}
	out := stripAnsi(renderChipPicker(m))
	if !strings.Contains(out, "  (no projects match)") {
		t.Fatalf("no-match message missing; output:\n%s", out)
	}

	m = pressKey(m, "enter")
	if m.chipPickerProjectSearchMode {
		t.Fatal("enter from no-match search mode should return to normal picker mode")
	}
	if len(m.chipPickerOptions) != 0 {
		t.Fatalf("len(chipPickerOptions)=%d after leaving search mode, want 0", len(m.chipPickerOptions))
	}

	for _, key := range []string{"j", "k", "G", "pgdown", "pgup"} {
		m = pressKey(m, key)
		if m.mode != ModeChipPicker {
			t.Fatalf("key %q changed mode to %v, want ModeChipPicker", key, m.mode)
		}
		if m.chipPickerCursor != 0 || m.chipPickerOffset != 0 {
			t.Fatalf("key %q moved empty picker to cursor=%d offset=%d, want 0/0",
				key, m.chipPickerCursor, m.chipPickerOffset)
		}
	}

	updated, cmd := m.Update(keyPress("enter"))
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("enter on no-match project picker returned cmd %T, want nil", cmd)
	}
	if m.mode != ModeChipPicker {
		t.Fatalf("enter on no-match project picker changed mode to %v, want ModeChipPicker", m.mode)
	}
	if len(m.filterProject) != 0 {
		t.Fatalf("enter on no-match project picker set filterProject=%#v", m.filterProject)
	}
}

func TestProjectChipPickerReopenClearsQuery(t *testing.T) {
	m := enterProjectChipPickerForTest(t, projectChipPickerSearchModel(t))
	m = pressKey(m, "/")
	m = pressKey(m, "f")
	m = pressKey(m, "o")
	m = pressKey(m, "enter")
	if m.chipPickerProjectQuery == "" {
		t.Fatal("setup failed: project query was not retained after leaving search mode")
	}

	m = pressKey(m, "q")
	if m.mode != ModeSessionSearch {
		t.Fatalf("q from project picker changed mode to %v, want ModeSessionSearch", m.mode)
	}

	m = enterProjectChipPickerForTest(t, m)
	if m.chipPickerProjectQuery != "" {
		t.Fatalf("reopened project picker query=%q, want empty", m.chipPickerProjectQuery)
	}
	if m.chipPickerProjectSearchMode {
		t.Fatal("reopened project picker started in search mode")
	}
	assertChipPickerOptions(t, m.chipPickerOptions, []string{"", "Foo", "Bar", "baz"})
}

func TestChipPickerSearchIsProjectOnly(t *testing.T) {
	cases := []struct {
		name string
		key  string
		kind chipPickerKind
	}{
		{name: "harness", key: "h", kind: chipPickerHarness},
		{name: "profile", key: "p", kind: chipPickerProfile},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := enterChipPickerForTest(t, projectChipPickerSearchModel(t), tc.key, tc.kind)
			before := append([]string(nil), m.chipPickerOptions...)

			updated, cmd := m.Update(keyPress("/"))
			m = updated.(Model)

			if cmd != nil {
				t.Fatalf("%s picker / returned cmd %T, want nil", tc.name, cmd)
			}
			if m.chipPickerProjectSearchMode {
				t.Fatalf("%s picker entered project search mode", tc.name)
			}
			if m.chipPickerProjectQuery != "" {
				t.Fatalf("%s picker query=%q, want empty", tc.name, m.chipPickerProjectQuery)
			}
			assertChipPickerOptions(t, m.chipPickerOptions, before)

			footer := stripAnsi(renderFooter(m))
			if strings.Contains(footer, "/: search") {
				t.Fatalf("%s picker footer unexpectedly advertised project search: %q", tc.name, footer)
			}
		})
	}
}

func TestProjectChipPickerOverlayFitsNarrowShortTerminal(t *testing.T) {
	m := enterProjectChipPickerForTest(t, projectChipPickerSearchModel(t))
	m.width = 60
	m.height = 10
	m = pressKey(m, "/")
	m = pressKey(m, "b")
	scrollChipPickerToCursor(&m)

	raw := m.render()
	plain := stripAnsi(raw)
	if !strings.Contains(plain, "Choose project") {
		t.Fatalf("project picker overlay missing header; output:\n%s", plain)
	}
	if !strings.Contains(plain, "/b") {
		t.Fatalf("project picker overlay missing search query; output:\n%s", plain)
	}

	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	if len(lines) > m.height+1 {
		t.Fatalf("overlay output exceeds terminal height: got %d lines, want <= %d\n%s",
			len(lines), m.height+1, plain)
	}
	for _, line := range lines {
		if w := runewidth.StringWidth(stripAnsi(line)); w > m.width {
			t.Fatalf("overlay line exceeded width=%d (got %d): %q", m.width, w, line)
		}
	}
}
