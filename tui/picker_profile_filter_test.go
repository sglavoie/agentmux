package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/launch"
	"github.com/sglavoie/agentmux/tui/layout"
)

func visiblePickerTitles(m Model) []string {
	titles := make([]string, 0, len(m.pickerFilteredIdx))
	for _, idx := range m.pickerFilteredIdx {
		titles = append(titles, m.pickerSessions[idx].Title)
	}
	return titles
}

func TestPickerProfileCycleFiltersProjectProfilesAndWraps(t *testing.T) {
	m := pickerModel(t)
	m.cfg.Accounts = append(m.cfg.Accounts, config.Account{Name: "unused", Color: "gray"})

	if m.pickerProfileFilter != "" {
		t.Fatalf("initial profile filter = %q, want all", m.pickerProfileFilter)
	}
	if got := launch.ProjectProfiles(m.pickerSessions); !slices.Equal(got, []string{"personal", "work"}) {
		t.Fatalf("project-backed profile cycle = %v, want [personal work]", got)
	}

	m = pressKey(m, "f")
	if m.pickerProfileFilter != "personal" {
		t.Fatalf("first profile filter = %q, want personal", m.pickerProfileFilter)
	}
	if got := visiblePickerTitles(m); !slices.Equal(got, []string{"dotfiles", "notes"}) {
		t.Fatalf("personal projects = %v, want [dotfiles notes]", got)
	}

	m = pressKey(m, "f")
	if m.pickerProfileFilter != "work" {
		t.Fatalf("second profile filter = %q, want work", m.pickerProfileFilter)
	}
	if got := visiblePickerTitles(m); !slices.Equal(got, []string{"api", "backend"}) {
		t.Fatalf("work projects = %v, want [api backend]", got)
	}

	m = pressKey(m, "f")
	if m.pickerProfileFilter != "" {
		t.Fatalf("wrapped profile filter = %q, want all", m.pickerProfileFilter)
	}
	if len(m.pickerFilteredIdx) != len(m.pickerSessions) {
		t.Fatalf("wrapped visible projects = %d, want %d", len(m.pickerFilteredIdx), len(m.pickerSessions))
	}
}

func TestPickerProfileCycleCombinesWithQueryAndUsesDefaultProfile(t *testing.T) {
	m := pickerModel(t)
	notesPos := pickerPositionByTitle(t, m, "notes")
	notesIdx := m.pickerFilteredIdx[notesPos]
	m.pickerProfileOverride[notesIdx] = "work"

	m = pressKey(m, "f") // All -> personal.
	if got := visiblePickerTitles(m); !slices.Contains(got, "notes") {
		t.Fatalf("overridden notes project left its default-profile group: %v", got)
	}

	m = pressKey(m, "/")
	m = typeString(m, "notes")
	if got := visiblePickerTitles(m); !slices.Equal(got, []string{"notes"}) {
		t.Fatalf("combined personal + notes results = %v, want [notes]", got)
	}
	m = pressKey(m, "enter")

	m = pressKey(m, "f") // personal -> work; query remains active.
	if m.pickerFilter != "notes" {
		t.Fatalf("profile cycle cleared query: got %q, want notes", m.pickerFilter)
	}
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("combined work + notes results = %v, want none", visiblePickerTitles(m))
	}

	m = pressKey(m, "f") // work -> All; query still narrows to notes.
	if m.pickerProfileFilter != "" {
		t.Fatalf("profile filter after wrap = %q, want all", m.pickerProfileFilter)
	}
	if got := visiblePickerTitles(m); !slices.Equal(got, []string{"notes"}) {
		t.Fatalf("all profiles + notes results = %v, want [notes]", got)
	}
}

func TestPickerProfileCyclePreservesSelectionAndResetsNavigation(t *testing.T) {
	m := pickerModel(t)
	selectedIdx := m.pickerFilteredIdx[0]
	m.pickerSelected[selectedIdx] = true
	m.pickerCursor = len(m.pickerFilteredIdx) - 1
	m.pickerOffset = 2
	m.pickerPreviewEnabled = true
	m.previewWindowID = 999

	updated, cmd := m.Update(keyPress("f"))
	got := updated.(Model)
	if !got.pickerSelected[selectedIdx] {
		t.Fatal("profile cycle discarded a hidden selection")
	}
	if got.pickerCursor != 0 || got.pickerOffset != 0 {
		t.Fatalf("profile cycle navigation = cursor %d offset %d, want 0/0", got.pickerCursor, got.pickerOffset)
	}
	if cmd == nil {
		t.Fatal("profile cycle did not refresh the enabled preview")
	}
}

func TestPickerFIsSearchTextAndEscClearsAllFiltersBeforeExit(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "/")
	m = pressKey(m, "f")
	if m.pickerProfileFilter != "" {
		t.Fatalf("f in search mode changed profile filter to %q", m.pickerProfileFilter)
	}
	if m.pickerFilter != "f" {
		t.Fatalf("f in search mode produced query %q, want f", m.pickerFilter)
	}

	m = pressKey(m, "enter") // Search input -> navigation; query retained.
	m = pressKey(m, "f")     // All -> personal.
	if m.pickerProfileFilter != "personal" {
		t.Fatalf("navigation-mode f filter = %q, want personal", m.pickerProfileFilter)
	}
	m = pressKey(m, "esc")
	if m.mode != ModePicker {
		t.Fatalf("filter-clearing esc left picker: mode=%v", m.mode)
	}
	if m.pickerFilter != "" || m.pickerProfileFilter != "" {
		t.Fatalf("esc retained filters: query=%q profile=%q", m.pickerFilter, m.pickerProfileFilter)
	}
	if len(m.pickerFilteredIdx) != len(m.pickerSessions) {
		t.Fatalf("esc visible projects = %d, want %d", len(m.pickerFilteredIdx), len(m.pickerSessions))
	}

	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("second esc mode = %v, want ModeList", m.mode)
	}
}

func TestPickerProfileIndicatorAndEntryReset(t *testing.T) {
	m := pickerModel(t)
	m.width = 120

	out := stripAnsi(renderPicker(m))
	if !strings.Contains(out, "Launch Sessions → Kitty: compatibility default  [profile: All]") {
		t.Fatalf("initial picker header missing All profile indicator:\n%s", out)
	}
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "f profile") {
		t.Fatalf("picker footer missing profile-cycle binding:\n%s", footer)
	}

	m = pressKey(m, "f")
	out = stripAnsi(renderPicker(m))
	if !strings.Contains(out, "[profile: personal]") {
		t.Fatalf("filtered picker header missing personal indicator:\n%s", out)
	}

	m.mode = ModeList
	m = pressKey(m, "l")
	if m.pickerProfileFilter != "" {
		t.Fatalf("l reopened launcher with profile filter %q, want all", m.pickerProfileFilter)
	}

	m.mode = ModeList
	m.pickerProfileFilter = "stale"
	got, _ := openLauncherCommand(m, commands.Context{})
	if got.pickerProfileFilter != "" {
		t.Fatalf("command palette reopened launcher with profile filter %q, want all", got.pickerProfileFilter)
	}
}

func TestPickerProfileIndicatorDoesNotOverflowNarrowHeader(t *testing.T) {
	m := pickerModel(t)
	m.width = 20
	first := strings.SplitN(renderPicker(m), "\n", 2)[0]
	if got := layout.StyledWidth(first); got > m.width {
		t.Fatalf("narrow picker header width = %d, want <= %d: %q", got, m.width, stripAnsi(first))
	}
}
