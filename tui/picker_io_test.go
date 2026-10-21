package tui

import (
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/overrides"
)

// isPickerIOMsg reports the messages the launcher's filesystem commands send.
func isPickerIOMsg(msg tea.Msg) bool {
	switch msg.(type) {
	case pickerDirProblemsMsg, pickerDirCompletedMsg, pickerStateSavedMsg:
		return true
	}
	return false
}

// pickerIOMessages runs cmd's tree until want launcher filesystem results have
// arrived (a save can take a while: it syncs the file). Every other command
// runs unobserved and its message is discarded, so a tick or a preview read
// neither blocks the test nor reaches the model.
func pickerIOMessages(cmd tea.Cmd, want int) []tea.Msg {
	if cmd == nil || want <= 0 {
		return nil
	}
	results := make(chan tea.Msg, 64)
	run := func(c tea.Cmd) {
		go func() { results <- c() }()
	}
	run(cmd)
	var out []tea.Msg
	deadline := time.After(5 * time.Second)
	for len(out) < want {
		select {
		case msg := <-results:
			switch msg := msg.(type) {
			case tea.BatchMsg:
				for _, c := range msg {
					if c != nil {
						run(c)
					}
				}
			default:
				if isPickerIOMsg(msg) {
					out = append(out, msg)
				}
			}
		case <-deadline:
			return out
		}
	}
	return out
}

// settlePickerIO feeds the one launcher filesystem result cmd starts back into
// the model, as the runtime would.
func settlePickerIO(m Model, cmd tea.Cmd) Model {
	for _, msg := range pickerIOMessages(cmd, 1) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}
	return m
}

// pressKeyIO is pressKey for a key whose effect includes launcher filesystem
// work: the key's directory check, completion, or save lands before it returns.
func pressKeyIO(m Model, key string) Model {
	updated, cmd := m.Update(keyPress(key))
	return settlePickerIO(updated.(Model), cmd)
}

// A completion answers only the prompt it was asked for: one typed past, or a
// launcher reopened since, gets nothing.
func TestPickerDirCompletionIsDroppedWhenThePromptMovedOn(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "d")
	asked := m.pickerDirValue
	updated, cmd := m.Update(keyPress("tab"))
	m = updated.(Model)
	if m.pickerDirValue != asked {
		t.Fatal("tab completed synchronously")
	}
	m = typeString(m, "zz")
	for _, msg := range pickerIOMessages(cmd, 1) {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	if m.pickerDirValue != asked+"zz" {
		t.Fatalf("a stale completion replaced typed text: %q", m.pickerDirValue)
	}
}

// Quick toggles finish in any order, but only the newest state may reach the
// file.
func TestPickerSavesLeaveTheNewestStateOnDisk(t *testing.T) {
	t.Setenv(overrides.PathEnvVar, filepath.Join(t.TempDir(), "launch_overrides.json"))
	m := pickerModel(t)
	m.pickerCursor = pickerPositionByTitle(t, m, "api")
	updated, first := m.Update(keyPress("P"))
	m = updated.(Model)
	m.pickerCursor = pickerPositionByTitle(t, m, "notes")
	updated, second := m.Update(keyPress("P"))
	m = updated.(Model)
	// The newer save runs first; the older one must not overwrite it.
	m = settlePickerIO(m, second)
	m = settlePickerIO(m, first)
	saved, err := overrides.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.PinnedProjects) != 2 {
		t.Fatalf("saved pins = %+v, want both api and notes", saved.PinnedProjects)
	}
	if len(m.pickerPinned) != 2 {
		t.Fatalf("model pins = %v, want both", m.pickerPinned)
	}
}
