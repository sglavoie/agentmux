package customization

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func cpKeyPress(key string) tea.KeyPressMsg {
	switch key {
	case "j":
		return tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"})
	case "k":
		return tea.KeyPressMsg(tea.Key{Code: 'k', Text: "k"})
	case "q":
		return tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"})
	case "enter":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	case "esc":
		return tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc})
	default:
		return tea.KeyPressMsg(tea.Key{Code: rune(key[0]), Text: key})
	}
}

func TestColorPicker_InitialCursorPosition(t *testing.T) {
	m := newColorPickerModel(ColorContext{DefaultColor: "red"})
	wantIdx := -1
	for i, name := range colorPickerOrder {
		if name == "red" {
			wantIdx = i
			break
		}
	}
	if m.cursor != wantIdx {
		t.Errorf("cursor = %d, want %d (red)", m.cursor, wantIdx)
	}
}

func TestColorPicker_InitialCursorDefaultUnknown(t *testing.T) {
	m := newColorPickerModel(ColorContext{DefaultColor: "nonexistent"})
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 for unknown default", m.cursor)
	}
}

func TestColorPicker_NavigationWraps(t *testing.T) {
	m := newColorPickerModel(ColorContext{DefaultColor: "green"})
	if m.cursor != 0 {
		t.Fatalf("expected cursor at 0 (green), got %d", m.cursor)
	}

	// k from first item wraps to last
	updated, _ := m.Update(cpKeyPress("k"))
	m = updated.(colorPickerModel)
	if m.cursor != len(m.items)-1 {
		t.Errorf("k from first: cursor = %d, want %d", m.cursor, len(m.items)-1)
	}

	// j from last item wraps to first
	updated, _ = m.Update(cpKeyPress("j"))
	m = updated.(colorPickerModel)
	if m.cursor != 0 {
		t.Errorf("j from last: cursor = %d, want 0", m.cursor)
	}
}

func TestColorPickerApplyHookReceivesPick(t *testing.T) {
	var picked []string
	ctx := ColorContext{DefaultColor: "blue", Subject: "api", Apply: func(name string) error {
		picked = append(picked, name)
		return nil
	}}
	m := newColorPickerModel(ctx)
	if !strings.Contains(m.View().Content, "Set Color: api") {
		t.Fatalf("header does not name the session:\n%s", m.View().Content)
	}
	updated, _ := m.Update(cpKeyPress("j"))
	updated, cmd := updated.(colorPickerModel).Update(cpKeyPress("enter"))
	m = updated.(colorPickerModel)
	if cmd == nil || !m.applied || m.err != nil {
		t.Fatalf("enter: applied=%v err=%v", m.applied, m.err)
	}

	reset := newColorPickerModel(ctx)
	reset.cursor = len(reset.items) - 1
	updated, _ = reset.Update(cpKeyPress("enter"))
	if !updated.(colorPickerModel).applied {
		t.Fatal("reset was not applied")
	}
	want := []string{colorPickerOrder[(indexOfColor("blue")+1)%len(colorPickerOrder)], ""}
	if len(picked) != 2 || picked[0] != want[0] || picked[1] != want[1] {
		t.Fatalf("picks = %q; want %q", picked, want)
	}
}

func indexOfColor(name string) int {
	for i, candidate := range colorPickerOrder {
		if candidate == name {
			return i
		}
	}
	return -1
}

// A failed apply reads as what the operator was doing, not as the call that
// failed: no "set color:" or "set tab color:" plumbing, and the cause kept.

func TestColorPicker_ApplyFailureIsOperatorWorded(t *testing.T) {
	ctx := ColorContext{DefaultColor: "blue", Subject: "api", Apply: func(string) error {
		return errors.New("daemon unreachable")
	}}
	updated, _ := newColorPickerModel(ctx).Update(cpKeyPress("enter"))
	view := updated.(colorPickerModel).View().Content
	if !strings.Contains(view, "Couldn't apply the color to this session: daemon unreachable") {
		t.Fatalf("apply failure not explained:\n%s", view)
	}
	if strings.Contains(view, "set color") {
		t.Fatalf("apply failure leaks the call name:\n%s", view)
	}
}
