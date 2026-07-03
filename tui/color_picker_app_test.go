package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

type fakeColorPickerClient struct {
	tabColorCalls []setTabColorCall
	userVarsCalls []setUserVarsCall
}

type setTabColorCall struct {
	matchExpr, activeBg, inactiveBg, activeFg, inactiveFg string
}

type setUserVarsCall struct {
	matchExpr string
	vars      map[string]string
}

func (f *fakeColorPickerClient) ListWindows() ([]kitty.OSWindow, error) { return nil, nil }
func (f *fakeColorPickerClient) LaunchTab(kitty.LaunchOpts) error       { return nil }
func (f *fakeColorPickerClient) LaunchOverlay(kitty.LaunchOpts) error   { return nil }
func (f *fakeColorPickerClient) CloseTab(string) error                  { return nil }
func (f *fakeColorPickerClient) MoveTab(string, int) error              { return nil }
func (f *fakeColorPickerClient) SetTabTitle(string, string) error       { return nil }
func (f *fakeColorPickerClient) FocusTab(string) error                  { return nil }
func (f *fakeColorPickerClient) FocusWindow(string) error               { return nil }
func (f *fakeColorPickerClient) GetText(string, string) (string, error) { return "", nil }
func (f *fakeColorPickerClient) LaunchOverlayPager(string) error        { return nil }

func (f *fakeColorPickerClient) SetTabColor(matchExpr, activeBg, inactiveBg, activeFg, inactiveFg string) error {
	f.tabColorCalls = append(f.tabColorCalls, setTabColorCall{matchExpr, activeBg, inactiveBg, activeFg, inactiveFg})
	return nil
}

func (f *fakeColorPickerClient) SetUserVars(matchExpr string, vars map[string]string) error {
	f.userVarsCalls = append(f.userVarsCalls, setUserVarsCall{matchExpr, vars})
	return nil
}

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
	m := newColorPickerModel(ColorPickerContext{DefaultColor: "red"})
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
	m := newColorPickerModel(ColorPickerContext{DefaultColor: "nonexistent"})
	if m.cursor != 0 {
		t.Errorf("cursor = %d, want 0 for unknown default", m.cursor)
	}
}

func TestColorPicker_NavigationWraps(t *testing.T) {
	m := newColorPickerModel(ColorPickerContext{DefaultColor: "green"})
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

func TestColorPicker_EnterAppliesColor(t *testing.T) {
	client := &fakeColorPickerClient{}
	m := newColorPickerModel(ColorPickerContext{
		Client:       client,
		TabID:        42,
		WindowID:     100,
		DefaultColor: "blue",
	})

	// Move to "red" (index 2)
	m.cursor = 2
	if m.items[2].name != "red" {
		t.Fatalf("expected items[2] = red, got %s", m.items[2].name)
	}

	updated, cmd := m.Update(cpKeyPress("enter"))
	m = updated.(colorPickerModel)

	if cmd == nil {
		t.Fatal("expected tea.Quit cmd")
	}

	if m.err != nil {
		t.Fatalf("unexpected error: %v", m.err)
	}

	// Verify SetTabColor: active colors reset to kitty.conf, inactive colors
	// keep the selected profile accent.
	if len(client.tabColorCalls) != 1 {
		t.Fatalf("SetTabColor calls = %d, want 1", len(client.tabColorCalls))
	}
	call := client.tabColorCalls[0]
	redColor, _ := palette.Lookup("red")
	if call.activeBg != kitty.TabColorDefault {
		t.Errorf("activeBg = %q, want %q", call.activeBg, kitty.TabColorDefault)
	}
	if call.activeFg != kitty.TabColorDefault {
		t.Errorf("activeFg = %q, want %q", call.activeFg, kitty.TabColorDefault)
	}
	if call.inactiveBg != redColor.InactiveBg {
		t.Errorf("inactiveBg = %q, want %q", call.inactiveBg, redColor.InactiveBg)
	}
	if call.inactiveFg != palette.InactiveFg {
		t.Errorf("inactiveFg = %q, want %q", call.inactiveFg, palette.InactiveFg)
	}

	// Verify SetUserVars (managed tab)
	if len(client.userVarsCalls) != 1 {
		t.Fatalf("SetUserVars calls = %d, want 1", len(client.userVarsCalls))
	}
	if client.userVarsCalls[0].vars["cc_color"] != "red" {
		t.Errorf("cc_color = %q, want red", client.userVarsCalls[0].vars["cc_color"])
	}
}

func TestColorPicker_EnterOnResetAppliesDefault(t *testing.T) {
	client := &fakeColorPickerClient{}
	m := newColorPickerModel(ColorPickerContext{
		Client:       client,
		TabID:        42,
		WindowID:     100,
		DefaultColor: "blue",
	})

	// Move cursor to the reset item (last)
	m.cursor = len(m.items) - 1
	if !m.items[m.cursor].isReset {
		t.Fatal("expected last item to be reset")
	}

	m.Update(cpKeyPress("enter"))

	if len(client.tabColorCalls) != 1 {
		t.Fatalf("SetTabColor calls = %d, want 1", len(client.tabColorCalls))
	}
	blueColor, _ := palette.Lookup("blue")
	call := client.tabColorCalls[0]
	if call.activeBg != kitty.TabColorDefault {
		t.Errorf("reset applied activeBg = %q, want %q", call.activeBg, kitty.TabColorDefault)
	}
	if call.activeFg != kitty.TabColorDefault {
		t.Errorf("reset applied activeFg = %q, want %q", call.activeFg, kitty.TabColorDefault)
	}
	if call.inactiveBg != blueColor.InactiveBg {
		t.Errorf("reset applied inactiveBg = %q, want %q (blue default)", call.inactiveBg, blueColor.InactiveBg)
	}
}

func TestColorPicker_EscCancels(t *testing.T) {
	client := &fakeColorPickerClient{}
	m := newColorPickerModel(ColorPickerContext{
		Client:       client,
		TabID:        42,
		WindowID:     100,
		DefaultColor: "blue",
	})

	_, cmd := m.Update(cpKeyPress("esc"))
	if cmd == nil {
		t.Fatal("expected tea.Quit cmd on esc")
	}
	if len(client.tabColorCalls) != 0 {
		t.Errorf("SetTabColor should not be called on esc, got %d calls", len(client.tabColorCalls))
	}
	if len(client.userVarsCalls) != 0 {
		t.Errorf("SetUserVars should not be called on esc, got %d calls", len(client.userVarsCalls))
	}
}

func TestColorPicker_UnmanagedNoUserVars(t *testing.T) {
	client := &fakeColorPickerClient{}
	m := newColorPickerModel(ColorPickerContext{
		Client:       client,
		TabID:        42,
		WindowID:     0, // unmanaged
		DefaultColor: "green",
	})

	m.cursor = 1 // blue
	m.Update(cpKeyPress("enter"))

	if len(client.tabColorCalls) != 1 {
		t.Fatalf("SetTabColor calls = %d, want 1", len(client.tabColorCalls))
	}
	if len(client.userVarsCalls) != 0 {
		t.Errorf("SetUserVars should not be called for unmanaged (WindowID=0), got %d calls", len(client.userVarsCalls))
	}
}

func TestColorPicker_ManagedSetUserVars(t *testing.T) {
	client := &fakeColorPickerClient{}
	m := newColorPickerModel(ColorPickerContext{
		Client:       client,
		TabID:        42,
		WindowID:     77, // managed
		DefaultColor: "green",
	})

	m.cursor = 4 // purple
	if m.items[4].name != "purple" {
		t.Fatalf("expected items[4] = purple, got %s", m.items[4].name)
	}
	m.Update(cpKeyPress("enter"))

	if len(client.userVarsCalls) != 1 {
		t.Fatalf("SetUserVars calls = %d, want 1", len(client.userVarsCalls))
	}
	if client.userVarsCalls[0].vars["cc_color"] != "purple" {
		t.Errorf("cc_color = %q, want purple", client.userVarsCalls[0].vars["cc_color"])
	}
}
