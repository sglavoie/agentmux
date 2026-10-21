package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/tui/commands"
)

func writeReloadConfig(t *testing.T, path, directory string) {
	t.Helper()
	text := fmt.Sprintf(`[harness_emojis]
codex = "🐱"
[[profiles]]
name = "personal"
color = "blue"
harness = "codex"
[[projects]]
title = "ZoomFlow"
profile = "personal"
directory = %q
`, directory)
	// Most editors replace the file rather than changing the watched inode.
	if err := os.WriteFile(path+".new", []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
}

func pollReloadConfig(t *testing.T, m Model) Model {
	t.Helper()
	cmd := m.refreshConfig()
	if cmd == nil {
		t.Fatal("config read was not scheduled")
	}
	if m.refreshConfig() != nil {
		t.Fatal("overlapping config read")
	}
	next, _ := m.Update(cmd())
	return next.(Model)
}

func TestConfigReloadUpdatesOpenLauncherAfterAtomicSave(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "config.toml")
	writeReloadConfig(t, path, root)
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	m := NewModel(cfg, nil)
	m.configPath = path
	m = pollReloadConfig(t, m)
	m, _ = openLauncherCommand(m, commands.Context{})
	updatedDir := t.TempDir()
	writeReloadConfig(t, path, updatedDir)
	m = pollReloadConfig(t, m)
	if got := m.pickerInput().SessionWithResolvedProfile(0).Directory; got != updatedDir {
		t.Fatalf("launcher directory = %q, want %q", got, updatedDir)
	}
	if cfg.Sessions[0].Directory != root {
		t.Fatal("reload mutated the config held by an in-flight command")
	}
	unchanged := m.cfg
	m = pollReloadConfig(t, m)
	if m.cfg != unchanged {
		t.Fatal("unchanged config was replaced")
	}
	for _, invalid := range []string{"[[broken", "[[projects]]\ntitle = 'bad'\nprofile = 'missing'\n"} {
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		m = pollReloadConfig(t, m)
		if m.cfg != unchanged || m.configError == "" {
			t.Fatal("invalid config replaced the last valid config or failed silently")
		}
		m = pollReloadConfig(t, m)
		if m.cfg != unchanged {
			t.Fatal("retry lost the last valid config")
		}
	}
	writeReloadConfig(t, path, root)
	m = pollReloadConfig(t, m)
	if m.configError != "" || m.pickerSessions[0].Directory != root {
		t.Fatal("corrected config did not recover automatically")
	}
}

func TestConfigReloadRemapsLauncherSelectionAndOverrides(t *testing.T) {
	t.Parallel()

	m := pickerModel(t)
	idx := -1
	for i, project := range m.pickerSessions {
		if project.Title == "backend" {
			idx = i
		}
	}
	m.pickerSelected = map[int]bool{idx: true}
	m.pickerPinned = map[int]bool{idx: true}
	m.pickerProfileOverride = map[int]string{idx: "personal"}
	m.pickerDirOverride = map[int]string{idx: t.TempDir()}
	wantDirectory := m.pickerDirOverride[idx]
	for cursor, i := range m.pickerFilteredIdx {
		if i == idx {
			m.pickerCursor = cursor
		}
	}
	next := *m.cfg
	next.Sessions = []config.Session{
		{Title: "aaa", Account: "work"},
		{Title: "backend", Account: "work", Directory: t.TempDir()},
	}
	updated, _ := m.Update(configReloadMsg{cfg: &next})
	m = updated.(Model)
	selected := m.pickerFilteredIdx[m.pickerCursor]
	if m.pickerSessions[selected].Title != "backend" || !m.pickerSelected[selected] || !m.pickerPinned[selected] {
		t.Fatal("config reorder moved the launcher selection or lost its pin")
	}
	project := m.pickerInput().SessionWithResolvedProfile(selected)
	if project.EffectiveProfile() != "personal" || project.Directory != wantDirectory {
		t.Fatalf("explicit overrides lost on reload: %+v", project)
	}
	if len(m.pickerSelected) != 1 || len(m.pickerSessions) != 2 {
		t.Fatal("removed projects survived the reload")
	}
}

func TestConfigReloadWaitsForDirectoryEditToFinish(t *testing.T) {
	t.Parallel()

	m := pickerModel(t)
	m.pickerDirMode = true
	before := m.cfg
	next := *m.cfg
	next.Sessions = nil
	updated, _ := m.Update(configReloadMsg{cfg: &next})
	m = updated.(Model)
	if m.cfg != before || m.pendingConfig == nil {
		t.Fatal("reload disturbed an open directory prompt")
	}
	m = pressKey(m, "esc")
	if m.cfg != &next || len(m.pickerSessions) != 0 || m.pendingConfig != nil {
		t.Fatal("pending reload was not applied after closing the prompt")
	}
}
