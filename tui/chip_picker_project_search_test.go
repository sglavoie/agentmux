package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
)

func chipPickerSearchModel(t *testing.T) Model {
	t.Helper()
	m := sessionSearchFilterModel(t)
	m.cfg.Projects = []config.Session{
		{Title: "Foo"},
		{Title: "Bar"},
		{Title: "baz"},
	}
	m.cfg.Profiles = []config.Account{
		{Name: "work"},
		{Name: "personal"},
		{Name: "ops"},
	}
	return m
}

type chipPickerSearchCase struct {
	name           string
	key            string
	kind           chipPickerKind
	query          string
	wantOptions    []string
	wantSelected   string
	wantAllOptions []string
	wantNoMatch    string
	wantResults    int
}

func chipPickerSearchCases() []chipPickerSearchCase {
	return []chipPickerSearchCase{
		{
			name:           "harness",
			key:            "h",
			kind:           chipPickerHarness,
			query:          "cL",
			wantOptions:    []string{"claude"},
			wantSelected:   "claude",
			wantAllOptions: []string{"", "claude", "copilot", "codex"},
			wantNoMatch:    "  (no harnesses match)",
			wantResults:    2,
		},
		{
			name:           "profile",
			key:            "p",
			kind:           chipPickerProfile,
			query:          "wO",
			wantOptions:    []string{"work"},
			wantSelected:   "work",
			wantAllOptions: []string{"", "work", "personal", "ops"},
			wantNoMatch:    "  (no profiles match)",
			wantResults:    2,
		},
		{
			name:           "project",
			key:            "r",
			kind:           chipPickerProject,
			query:          "fO",
			wantOptions:    []string{"Foo"},
			wantSelected:   "Foo",
			wantAllOptions: []string{"", "Foo", "Bar", "baz"},
			wantNoMatch:    "  (no projects match)",
			wantResults:    1,
		},
	}
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

func selectedFilterForKind(m Model, kind chipPickerKind) []string {
	switch kind {
	case chipPickerHarness:
		return m.filterHarness
	case chipPickerProfile:
		return m.filterProfile
	case chipPickerProject:
		return m.filterProject
	}
	return nil
}

func assertChipPickerOptions(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("chipPickerOptions=%#v, want %#v", got, want)
	}
}

func TestChipPickerSlashStartsSearchWithoutSessionDispatch(t *testing.T) {
	for _, tc := range chipPickerSearchCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := enterChipPickerForTest(t, chipPickerSearchModel(t), tc.key, tc.kind)
			seq := m.sessionSearchSeq

			updated, cmd := m.Update(keyPress("/"))
			m = updated.(Model)

			if cmd != nil {
				t.Fatalf("%s picker / returned cmd %T, want nil", tc.name, cmd)
			}
			if !m.chipPickerSearchMode {
				t.Fatalf("%s picker / did not enter search mode", tc.name)
			}
			if m.chipPickerQuery != "" {
				t.Fatalf("%s picker query=%q, want empty", tc.name, m.chipPickerQuery)
			}
			if m.sessionSearchSeq != seq {
				t.Fatalf("sessionSearchSeq=%d, want unchanged %d", m.sessionSearchSeq, seq)
			}
			if m.sessionLoading {
				t.Fatalf("%s picker / marked session search loading; want no dispatch", tc.name)
			}
		})
	}
}

func TestChipPickerQueryFiltersCaseInsensitivelyAndHidesAny(t *testing.T) {
	for _, tc := range chipPickerSearchCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := enterChipPickerForTest(t, chipPickerSearchModel(t), tc.key, tc.kind)
			m = pressKey(m, "/")
			m = typeString(m, tc.query)

			if m.chipPickerQuery != tc.query {
				t.Fatalf("chipPickerQuery=%q, want %q", m.chipPickerQuery, tc.query)
			}
			assertChipPickerOptions(t, m.chipPickerOptions, tc.wantOptions)

			out := stripAnsi(renderChipPicker(m))
			if strings.Contains(out, "(any)") {
				t.Fatalf("filtered %s picker rendered (any); output:\n%s", tc.name, out)
			}
			if !strings.Contains(out, tc.wantSelected) {
				t.Fatalf("filtered %s picker did not render %s; output:\n%s", tc.name, tc.wantSelected, out)
			}
		})
	}
}

func TestChipPickerSearchExitKeysPreserveFilteredOptions(t *testing.T) {
	for _, tc := range chipPickerSearchCases() {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"esc", "enter"} {
				t.Run(key, func(t *testing.T) {
					m := enterChipPickerForTest(t, chipPickerSearchModel(t), tc.key, tc.kind)
					m = pressKey(m, "/")
					m = typeString(m, strings.ToLower(tc.query))

					updated, cmd := m.Update(keyPress(key))
					m = updated.(Model)

					if cmd != nil {
						t.Fatalf("%s from %s search mode returned cmd %T, want nil", key, tc.name, cmd)
					}
					if m.mode != ModeChipPicker {
						t.Fatalf("%s from %s search mode changed mode to %v, want ModeChipPicker", key, tc.name, m.mode)
					}
					if m.chipPickerSearchMode {
						t.Fatalf("%s from %s search mode left search mode active", key, tc.name)
					}
					wantQuery := strings.ToLower(tc.query)
					if m.chipPickerQuery != wantQuery {
						t.Fatalf("chipPickerQuery=%q, want %q", m.chipPickerQuery, wantQuery)
					}
					assertChipPickerOptions(t, m.chipPickerOptions, tc.wantOptions)
				})
			}
		})
	}
}

func TestChipPickerSecondEnterSelectsAndDispatchesSearch(t *testing.T) {
	for _, tc := range chipPickerSearchCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := enterChipPickerForTest(t, chipPickerSearchModel(t), tc.key, tc.kind)
			m = pressKey(m, "/")
			m = typeString(m, strings.ToLower(tc.query))
			m = pressKey(m, "enter")
			if m.chipPickerSearchMode {
				t.Fatalf("first enter should leave %s search mode", tc.name)
			}

			updated, cmd := m.Update(keyPress("enter"))
			m = updated.(Model)

			if cmd == nil {
				t.Fatal("second enter returned nil cmd, want debounced session search")
			}
			if m.mode != ModeSessionSearch {
				t.Fatalf("mode=%v, want ModeSessionSearch", m.mode)
			}
			if !slices.Equal(selectedFilterForKind(m, tc.kind), []string{tc.wantSelected}) {
				t.Fatalf("%s filter=%#v, want %s", tc.name, selectedFilterForKind(m, tc.kind), tc.wantSelected)
			}
			if !m.sessionLoading {
				t.Fatalf("selecting %s did not mark session search loading", tc.name)
			}

			updated, searchCmd := m.Update(sessionSearchDebounceMsg{seq: m.sessionSearchSeq})
			m = updated.(Model)
			doneMsg, ok := drainCmd(t, searchCmd).(sessionSearchDoneMsg)
			if !ok {
				t.Fatalf("%s selection debounce produced %T, want sessionSearchDoneMsg", tc.name, doneMsg)
			}
			if doneMsg.err != nil {
				t.Fatalf("%s-filtered search returned error: %v", tc.name, doneMsg.err)
			}

			updated, _ = m.Update(doneMsg)
			m = updated.(Model)
			if len(m.sessionResults) != tc.wantResults {
				t.Fatalf("len(sessionResults)=%d, want %d", len(m.sessionResults), tc.wantResults)
			}
			for _, res := range m.sessionResults {
				switch tc.kind {
				case chipPickerHarness:
					if res.Meta.Harness != tc.wantSelected {
						t.Fatalf("selected harness search returned harness %q, want %q", res.Meta.Harness, tc.wantSelected)
					}
				case chipPickerProfile:
					if !res.Meta.MatchesAnyProfile([]string{tc.wantSelected}) {
						t.Fatalf("selected profile search returned profiles %#v, want membership in %q", res.Meta.ProfileNames(), tc.wantSelected)
					}
				case chipPickerProject:
					if res.Meta.Project != tc.wantSelected {
						t.Fatalf("selected project search returned project %q, want %q", res.Meta.Project, tc.wantSelected)
					}
				}
			}
		})
	}
}
