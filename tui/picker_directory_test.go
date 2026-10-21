package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/overrides"
)

func typeKeys(m Model, text string) Model {
	for _, r := range text {
		m = pressKey(m, string(r))
	}
	return m
}

func TestPickerDirectoryOverrideAppliesToOneLaunchOnly(t *testing.T) {
	t.Setenv(overrides.PathEnvVar, filepath.Join(t.TempDir(), "launch_overrides.json"))
	previous := actions.ProjectDirectoryProblem
	actions.ProjectDirectoryProblem = config.ProjectDirectoryProblem
	t.Cleanup(func() { actions.ProjectDirectoryProblem = previous })
	other := t.TempDir()
	if err := os.Mkdir(filepath.Join(other, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := pickerModel(t)
	m.pickerCursor = pickerPositionByTitle(t, m, "api")
	apiIdx := m.pickerFilteredIdx[m.pickerCursor]
	configured := m.pickerSessions[apiIdx].Directory

	m = pressKey(m, "d")
	if !m.pickerDirMode || m.pickerDirValue != configured {
		t.Fatalf("prompt = open %v holding %q, want open holding %q", m.pickerDirMode, m.pickerDirValue, configured)
	}
	m = typeKeys(pressKey(m, "ctrl+u"), filepath.Join(other, "nope"))
	m = pressKey(m, "enter")
	if !m.pickerDirMode || m.pickerDirError != "does not exist" || len(m.pickerDirOverride) != 0 {
		t.Fatalf("a missing directory was accepted: open %v, error %q, overrides %v", m.pickerDirMode, m.pickerDirError, m.pickerDirOverride)
	}

	// Typing is taken literally: d, j, and space edit the path, not the list.
	m = typeKeys(pressKey(m, "ctrl+u"), other+"/s")
	m = pressKey(pressKeyIO(m, "tab"), "enter")
	want := filepath.Join(other, "sub")
	if m.pickerDirMode || m.pickerDirOverride[apiIdx] != want {
		t.Fatalf("override = %q (prompt open %v), want %q", m.pickerDirOverride[apiIdx], m.pickerDirMode, want)
	}
	if queue := m.pickerInput().OpenTargets(); len(queue) != 1 || queue[0].Directory != want || queue[0].Title != "api" {
		t.Fatalf("launch target = %+v, want api in %q", queue, want)
	}
	if row := stripAnsi(renderPickerList(m, 0)); !strings.Contains(row, "(dir: "+want+")") {
		t.Fatalf("row does not name the override:\n%s", row)
	}

	cleared := pressKey(pressKey(pressKey(m, "d"), "ctrl+u"), "enter")
	if len(cleared.pickerDirOverride) != 0 {
		t.Fatalf("an empty entry kept the override: %v", cleared.pickerDirOverride)
	}

	// Nothing is saved, and sending the launch or leaving drops the choice.
	if saved, err := overrides.Load(); err != nil || len(saved.Entries) != 0 {
		t.Fatalf("saved entries = %+v (err %v), want none", saved.Entries, err)
	}
	if launched := pressKey(m, "enter"); len(launched.pickerDirOverride) != 0 {
		t.Fatalf("the override outlived its launch: %v", launched.pickerDirOverride)
	}
	if left := pressKey(m, "q"); len(left.pickerDirOverride) != 0 {
		t.Fatalf("the override outlived the launcher: %v", left.pickerDirOverride)
	}
	if fresh := pickerModel(t); len(fresh.pickerDirOverride) != 0 {
		t.Fatalf("override after reopening = %v, want none", fresh.pickerDirOverride)
	}
}
