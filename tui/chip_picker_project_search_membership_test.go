package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/transcript"
)

func TestChipPickerProfileSelectionMatchesSharedHomeMembership(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	shared := transcript.SessionMeta{
		ID:       "shared",
		Harness:  harness.Claude,
		Profile:  "work",
		Profiles: []string{"work", "personal"},
		Project:  "Foo",
		LastAt:   now,
		Snippet:  "shared home transcript",
	}
	workOnly := transcript.SessionMeta{
		ID:      "work-only",
		Harness: harness.Claude,
		Profile: "work",
		Project: "Bar",
		LastAt:  now.Add(-time.Hour),
		Snippet: "work only transcript",
	}
	m := chipPickerSearchModel(t)
	m.transcriptIndex = writeTranscriptIndex(t, map[string]transcript.SessionMeta{
		"claude:shared":    shared,
		"claude:work-only": workOnly,
	})
	m.transcriptReaders = map[string]transcript.Reader{}
	m.sessionResults = []transcript.Result{
		{Meta: shared, Preview: shared.Snippet},
		{Meta: workOnly, Preview: workOnly.Snippet},
	}

	m = enterChipPickerForTest(t, m, "p", chipPickerProfile)
	m = pressKey(m, "/")
	m = typeString(m, "per")
	m = pressKey(m, "enter")

	updated, cmd := m.Update(keyPress("enter"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("selecting personal profile returned nil cmd, want debounced session search")
	}
	if !slices.Equal(m.filterProfile, []string{"personal"}) {
		t.Fatalf("filterProfile = %#v, want [personal]", m.filterProfile)
	}

	updated, searchCmd := m.Update(sessionSearchDebounceMsg{seq: m.sessionSearchSeq})
	m = updated.(Model)
	doneMsg, ok := drainCmd(t, searchCmd).(sessionSearchDoneMsg)
	if !ok {
		t.Fatalf("profile selection debounce produced %T, want sessionSearchDoneMsg", doneMsg)
	}
	if doneMsg.err != nil {
		t.Fatalf("profile-filtered search returned error: %v", doneMsg.err)
	}
	updated, _ = m.Update(doneMsg)
	m = updated.(Model)
	if len(m.sessionResults) != 1 || m.sessionResults[0].Meta.ID != "shared" {
		t.Fatalf("personal profile filter results = %+v, want only shared-home transcript", m.sessionResults)
	}
	out := stripAnsi(renderSessionSearch(m))
	if !strings.Contains(out, "[work+1/claude]") {
		t.Fatalf("shared-home result did not render compact profile label:\n%s", out)
	}
}

func TestChipPickerProjectSelectionMatchesSharedDirectoryMembership(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	shared := transcript.SessionMeta{
		ID:       "shared-project",
		Harness:  harness.Claude,
		Profile:  "work",
		Project:  "Foo",
		Projects: []string{"Foo", "Bar"},
		LastAt:   now,
		Snippet:  "shared directory transcript",
	}
	other := transcript.SessionMeta{
		ID:      "other-project",
		Harness: harness.Claude,
		Profile: "work",
		Project: "baz",
		LastAt:  now.Add(-time.Hour),
		Snippet: "other project transcript",
	}
	m := chipPickerSearchModel(t)
	m.transcriptIndex = writeTranscriptIndex(t, map[string]transcript.SessionMeta{
		"claude:shared-project": shared,
		"claude:other-project":  other,
	})
	m.transcriptReaders = map[string]transcript.Reader{}
	m.sessionResults = []transcript.Result{
		{Meta: shared, Preview: shared.Snippet},
		{Meta: other, Preview: other.Snippet},
	}

	m = enterProjectChipPickerForTest(t, m)
	m = pressKey(m, "/")
	m = typeString(m, "Bar")
	m = pressKey(m, "enter")

	updated, cmd := m.Update(keyPress("enter"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("selecting Bar returned nil cmd, want debounced session search")
	}
	if !slices.Equal(m.filterProject, []string{"Bar"}) {
		t.Fatalf("filterProject = %#v, want [Bar]", m.filterProject)
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
	if len(m.sessionResults) != 1 || m.sessionResults[0].Meta.ID != shared.ID {
		t.Fatalf("Bar project filter results = %+v, want only shared transcript", m.sessionResults)
	}
	if m.sessionResults[0].Meta.Project != "Foo" {
		t.Fatalf("stored result project = %q, want stable primary Foo", m.sessionResults[0].Meta.Project)
	}
	row := stripAnsi(renderSessionSearchList(m, 10))
	if strings.Contains(row, "Bar") || strings.Contains(row, "Foo") {
		t.Fatalf("filtered shared-project row repeated project metadata:\n%s", row)
	}
	if view := stripAnsi(renderSessionSearch(m)); !strings.Contains(view, "[r] project: Bar") {
		t.Fatalf("filtered shared-project view did not show selected alias in chip:\n%s", view)
	}
}

func TestChipPickerNoMatchStateIsSafe(t *testing.T) {
	for _, tc := range chipPickerSearchCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := enterChipPickerForTest(t, chipPickerSearchModel(t), tc.key, tc.kind)
			m = pressKey(m, "/")
			m = typeString(m, "zzzz")

			if len(m.chipPickerOptions) != 0 {
				t.Fatalf("len(chipPickerOptions)=%d, want 0", len(m.chipPickerOptions))
			}
			out := stripAnsi(renderChipPicker(m))
			if !strings.Contains(out, tc.wantNoMatch) {
				t.Fatalf("no-match message missing; output:\n%s", out)
			}

			m = pressKey(m, "enter")
			if m.chipPickerSearchMode {
				t.Fatalf("enter from no-match %s search mode should return to normal picker mode", tc.name)
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
				t.Fatalf("enter on no-match %s picker returned cmd %T, want nil", tc.name, cmd)
			}
			if m.mode != ModeChipPicker {
				t.Fatalf("enter on no-match %s picker changed mode to %v, want ModeChipPicker", tc.name, m.mode)
			}
			if len(selectedFilterForKind(m, tc.kind)) != 0 {
				t.Fatalf("enter on no-match %s picker set filter=%#v", tc.name, selectedFilterForKind(m, tc.kind))
			}
		})
	}
}

func TestChipPickerReopenClearsQuery(t *testing.T) {
	for _, tc := range chipPickerSearchCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := enterChipPickerForTest(t, chipPickerSearchModel(t), tc.key, tc.kind)
			m = pressKey(m, "/")
			m = typeString(m, strings.ToLower(tc.query))
			m = pressKey(m, "enter")
			if m.chipPickerQuery == "" {
				t.Fatalf("setup failed: %s query was not retained after leaving search mode", tc.name)
			}

			m = pressKey(m, "q")
			if m.mode != ModeSessionSearch {
				t.Fatalf("q from %s picker changed mode to %v, want ModeSessionSearch", tc.name, m.mode)
			}

			m = enterChipPickerForTest(t, m, tc.key, tc.kind)
			if m.chipPickerQuery != "" {
				t.Fatalf("reopened %s picker query=%q, want empty", tc.name, m.chipPickerQuery)
			}
			if m.chipPickerSearchMode {
				t.Fatalf("reopened %s picker started in search mode", tc.name)
			}
			assertChipPickerOptions(t, m.chipPickerOptions, tc.wantAllOptions)
		})
	}
}

func TestChipPickerSearchFooterAdvertisedForAllKinds(t *testing.T) {
	for _, tc := range chipPickerSearchCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := enterChipPickerForTest(t, chipPickerSearchModel(t), tc.key, tc.kind)
			footer := stripAnsi(renderFooter(m))
			if !strings.Contains(footer, "/ search") {
				t.Fatalf("%s picker footer did not advertise search: %q", tc.name, footer)
			}
		})
	}
}

func TestProjectChipPickerOverlayFitsNarrowShortTerminal(t *testing.T) {
	m := enterProjectChipPickerForTest(t, chipPickerSearchModel(t))
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
