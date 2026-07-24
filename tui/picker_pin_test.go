package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/overrides"
)

func pickerPositionByTitle(t *testing.T, m Model, title string) int {
	t.Helper()
	for pos, idx := range m.pickerFilteredIdx {
		if idx >= 0 && idx < len(m.pickerSessions) && m.pickerSessions[idx].Title == title {
			return pos
		}
	}
	t.Fatalf("picker title %q not found in visible indices %v", title, m.pickerFilteredIdx)
	return -1
}

func pinPickerTitle(t *testing.T, m Model, title string) Model {
	t.Helper()
	m.pickerCursor = pickerPositionByTitle(t, m, title)
	updated, _ := m.Update(keyPress("P"))
	return updated.(Model)
}

func TestPickerPinToggleReordersPersistsAndRenders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launch_overrides.json")
	t.Setenv(overrides.PathEnvVar, path)

	m := pickerModel(t)
	m = pinPickerTitle(t, m, "api")

	if got := m.pickerSessions[m.pickerFilteredIdx[0]].Title; got != "api" {
		t.Fatalf("first picker row after pin = %q, want api", got)
	}
	if m.pickerCursor != 0 {
		t.Fatalf("cursor after pin = %d, want 0 on the same project", m.pickerCursor)
	}
	apiIdx := m.pickerFilteredIdx[m.pickerCursor]
	if !m.pickerPinned[apiIdx] {
		t.Fatal("api is not marked pinned in the model")
	}

	rendered := stripAnsi(renderPickerList(m, 0))
	var apiRow string
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "api") {
			apiRow = line
			break
		}
	}
	if !strings.Contains(apiRow, "📌") {
		t.Fatalf("pinned api row has no pin marker: %q", apiRow)
	}
	footer := stripAnsi(renderFooter(m))
	if !strings.Contains(footer, "P: pin") {
		t.Fatalf("launcher footer has no pin binding: %q", footer)
	}

	saved, err := overrides.Load()
	if err != nil {
		t.Fatalf("load persisted pins: %v", err)
	}
	if saved.Version != overrides.CurrentVersion {
		t.Fatalf("saved version = %d, want %d", saved.Version, overrides.CurrentVersion)
	}
	if len(saved.PinnedProjects) != 1 ||
		saved.PinnedProjects[0] != (overrides.PinnedProject{ProfileName: "work", ProjectTitle: "api"}) {
		t.Fatalf("saved pins = %+v, want work/api", saved.PinnedProjects)
	}

	fresh := pickerModel(t)
	if got := fresh.pickerSessions[fresh.pickerFilteredIdx[0]].Title; got != "api" {
		t.Fatalf("first picker row after restart = %q, want persisted api pin", got)
	}
	fresh = pressKey(fresh, "P")
	if got := fresh.pickerSessions[fresh.pickerFilteredIdx[fresh.pickerCursor]].Title; got != "api" {
		t.Fatalf("cursor moved to %q after unpin, want to remain on api", got)
	}
	if fresh.pickerCursor != 2 {
		t.Fatalf("api cursor after unpin = %d, want restored sort position 2", fresh.pickerCursor)
	}
	saved, err = overrides.Load()
	if err != nil {
		t.Fatalf("load state after unpin: %v", err)
	}
	if len(saved.PinnedProjects) != 0 {
		t.Fatalf("pins after unpin = %+v, want none", saved.PinnedProjects)
	}
}

func TestPickerPinsRetainExistingOrderSelectionOverrideAndFilter(t *testing.T) {
	t.Setenv(overrides.PathEnvVar, filepath.Join(t.TempDir(), "launch_overrides.json"))
	m := pickerModel(t)

	dotfilesPos := pickerPositionByTitle(t, m, "dotfiles")
	m.pickerCursor = dotfilesPos
	m = pressKey(m, " ")
	dotfilesIdx := m.pickerFilteredIdx[dotfilesPos]
	m.pickerProfileOverride[dotfilesIdx] = "work"

	m = pinPickerTitle(t, m, "backend")
	m = pinPickerTitle(t, m, "api")
	gotPinned := []string{
		m.pickerSessions[m.pickerFilteredIdx[0]].Title,
		m.pickerSessions[m.pickerFilteredIdx[1]].Title,
	}
	if !slices.Equal(gotPinned, []string{"api", "backend"}) {
		t.Fatalf("pinned order = %v, want existing api/backend order", gotPinned)
	}
	if !m.pickerSelected[dotfilesIdx] {
		t.Fatal("pin reordering lost the selected dotfiles project")
	}
	if got := m.pickerProfileOverride[dotfilesIdx]; got != "work" {
		t.Fatalf("pin reordering changed dotfiles override to %q", got)
	}

	m = pressKey(m, "/")
	for _, key := range []string{"@", "w", "o", "r", "k"} {
		m = pressKey(m, key)
	}
	if len(m.pickerFilteredIdx) != 2 {
		t.Fatalf("@work visible rows = %d, want 2", len(m.pickerFilteredIdx))
	}
	filteredTitles := []string{
		m.pickerSessions[m.pickerFilteredIdx[0]].Title,
		m.pickerSessions[m.pickerFilteredIdx[1]].Title,
	}
	if !slices.Equal(filteredTitles, []string{"api", "backend"}) {
		t.Fatalf("@work pinned order = %v, want api/backend", filteredTitles)
	}

	saved, err := overrides.Load()
	if err != nil {
		t.Fatalf("load combined launcher state: %v", err)
	}
	if len(saved.Entries) != 1 ||
		saved.Entries[0] != (overrides.Entry{ProjectTitle: "dotfiles", ProfileName: "work"}) {
		t.Fatalf("saved profile overrides = %+v, want dotfiles/work", saved.Entries)
	}
	if len(saved.PinnedProjects) != 2 {
		t.Fatalf("saved pins = %+v, want two", saved.PinnedProjects)
	}
}

func TestPickerPinIdentityIncludesDefaultProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launch_overrides.json")
	t.Setenv(overrides.PathEnvVar, path)
	if err := overrides.Save(overrides.File{
		Version: overrides.CurrentVersion,
		PinnedProjects: []overrides.PinnedProject{
			{ProfileName: "work", ProjectTitle: "api"},
		},
	}); err != nil {
		t.Fatalf("seed pin: %v", err)
	}

	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "personal", Color: "yellow"},
			{Name: "work", Color: "blue"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/personal/api", Account: "personal"},
			{Title: "api", Directory: "/work/api", Account: "work"},
		},
	}
	m := NewModel(cfg, kitty.NewClientWithSocket(kitty.NewMockRunner(nil), ""))
	m = loadSessions(m, nil)
	m = pressKey(m, "l")

	firstIdx := m.pickerFilteredIdx[0]
	secondIdx := m.pickerFilteredIdx[1]
	if got := m.pickerSessions[firstIdx].EffectiveProfile(); got != "work" {
		t.Fatalf("first duplicate-title profile = %q, want pinned work", got)
	}
	if !m.pickerPinned[firstIdx] || m.pickerPinned[secondIdx] {
		t.Fatalf("pin map = %v, want only work/api pinned", m.pickerPinned)
	}
}

func TestPickerUppercasePIsSearchTextWhileFiltering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launch_overrides.json")
	t.Setenv(overrides.PathEnvVar, path)
	m := pickerModel(t)
	m = pressKey(m, "/")
	m = pressKey(m, "P")

	if m.pickerFilter != "P" {
		t.Fatalf("picker filter = %q, want uppercase P text", m.pickerFilter)
	}
	if len(m.pickerPinned) != 0 {
		t.Fatalf("P in search mode changed pins: %v", m.pickerPinned)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("P in search mode unexpectedly wrote launcher state: %v", err)
	}
}

func TestPickerPinSaveFailureLeavesOrderingUnchanged(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "occupied")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("create occupied destination: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatalf("create destination blocker: %v", err)
	}
	t.Setenv(overrides.PathEnvVar, path)

	m := pickerModel(t)
	before := slices.Clone(m.pickerFilteredIdx)
	m.pickerCursor = pickerPositionByTitle(t, m, "api")
	updated, cmd := m.Update(keyPress("P"))
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("failed pin save returned no toast command")
	}
	if len(got.pickerPinned) != 0 {
		t.Fatalf("failed pin save changed pin state: %v", got.pickerPinned)
	}
	if !slices.Equal(got.pickerFilteredIdx, before) {
		t.Fatalf("failed pin save changed ordering: got %v want %v", got.pickerFilteredIdx, before)
	}
	if len(got.toasts) == 0 || got.toasts[0].Severity != toastError ||
		!strings.Contains(got.toasts[0].Text, "Could not update pin") {
		t.Fatalf("failed pin save toast = %+v", got.toasts)
	}
}

func TestLoadPickerStatePrunesStaleAndDuplicatePins(t *testing.T) {
	t.Setenv(overrides.PathEnvVar, filepath.Join(t.TempDir(), "launch_overrides.json"))
	if err := overrides.Save(overrides.File{
		Version: overrides.CurrentVersion,
		PinnedProjects: []overrides.PinnedProject{
			{ProfileName: "work", ProjectTitle: "api"},
			{ProfileName: "WORK", ProjectTitle: "API"},
			{ProfileName: "work", ProjectTitle: "missing"},
		},
	}); err != nil {
		t.Fatalf("seed pins: %v", err)
	}

	m := pickerModel(t)
	if len(m.pickerPinned) != 1 {
		t.Fatalf("loaded pins = %v, want one valid pin", m.pickerPinned)
	}
	saved, err := overrides.Load()
	if err != nil {
		t.Fatalf("load pruned pins: %v", err)
	}
	if len(saved.PinnedProjects) != 1 ||
		saved.PinnedProjects[0] != (overrides.PinnedProject{ProfileName: "work", ProjectTitle: "api"}) {
		t.Fatalf("pruned pins = %+v, want canonical work/api", saved.PinnedProjects)
	}
}
