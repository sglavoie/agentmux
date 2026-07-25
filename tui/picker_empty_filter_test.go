package tui

import "testing"

// ---- Empty-filter cursor safety tests (picker) ----
//
// Every `m.pickerFilteredIdx[m.pickerCursor]` access in handlePickerNavKey
// is guarded by either `len(m.pickerFilteredIdx) > 0` or a clamp returning
// the low bound when the slice is empty. These tests drive every key
// against a filter that matches zero rows and assert no panic, the picker
// stays responsive, and backspace fully restores the navigable list.

// pressKeyPickerSafe wraps pressKey with a recover so a panic on one key
// is reported but doesn't abort the rest of the suite.
func pressKeyPickerSafe(t *testing.T, m Model, key string) Model {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic on key %q in picker: %v", key, r)
		}
	}()
	return pressKey(m, key)
}

// TestPickerEmptyFilterNavigationIsSafe drives every navigation key after
// narrowing the picker filter to zero rows. Excludes `enter` because it
// intentionally exits to ModeList — that path is covered separately.
func TestPickerEmptyFilterNavigationIsSafe(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "/")
	for _, ch := range "zzzz" {
		m = pressKey(m, string(ch))
	}
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("setup: expected 0 matches for 'zzzz', got %d", len(m.pickerFilteredIdx))
	}
	m = pressKey(m, "esc") // exit search mode; filter kept
	if m.pickerSearchMode {
		t.Fatal("setup: expected pickerSearchMode=false after esc from search")
	}
	if m.mode != ModePicker {
		t.Fatalf("setup: expected ModePicker after esc from search, got %d", m.mode)
	}
	for _, k := range []string{"j", "k", "g", "G", " ", "P", "s", "o", "C"} {
		m = pressKeyPickerSafe(t, m, k)
		if m.mode != ModePicker {
			t.Errorf("expected to stay in ModePicker after %q with empty filter, got mode=%d", k, m.mode)
			m.mode = ModePicker
		}
		if m.pickerCursor < 0 {
			t.Errorf("pickerCursor went negative after %q: %d", k, m.pickerCursor)
			m.pickerCursor = 0
		}
	}
}

// TestPickerEnterOnEmptyFilterExitsCleanly drives `enter` while the picker
// filter matches zero rows. With nothing selectable the launch is a no-op,
// but the handler must still cleanly exit to ModeList and not panic.
func TestPickerEnterOnEmptyFilterExitsCleanly(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "/")
	for _, ch := range "zzzz" {
		m = pressKey(m, string(ch))
	}
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("setup: expected 0 matches, got %d", len(m.pickerFilteredIdx))
	}
	m = pressKey(m, "esc") // search → nav
	updated, cmd := m.Update(keyPress("enter"))
	m2 := updated.(Model)
	if m2.mode != ModeList {
		t.Errorf("expected ModeList after enter on empty-filter picker, got %d", m2.mode)
	}
	if cmd != nil {
		t.Errorf("expected nil cmd from empty-selection launch, got non-nil")
	}
}

// TestPickerBackspaceRecoversFromEmptyFilter verifies the documented
// recovery path: when the filter collapses the list to zero rows, every
// backspace re-applies the filter and resets pickerCursor to 0 so the user
// can navigate the moment matches return.
func TestPickerBackspaceRecoversFromEmptyFilter(t *testing.T) {
	m := pickerModel(t)
	m = pressKey(m, "/")
	for _, ch := range "zzzz" {
		m = pressKey(m, string(ch))
	}
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("setup: expected 0 matches, got %d", len(m.pickerFilteredIdx))
	}
	// Pre-move the cursor in nav mode to confirm backspace resets it.
	m = pressKey(m, "esc") // search → nav, filter "zzzz" kept
	m.pickerCursor = 99    // simulate a stale cursor value
	// Backspace fires in nav mode while the filter still has content.
	for i := 0; i < 4; i++ {
		m = pressKey(m, "backspace")
		if m.pickerCursor != 0 {
			t.Errorf("expected pickerCursor reset to 0 after backspace #%d, got %d", i+1, m.pickerCursor)
		}
	}
	if m.pickerFilter != "" {
		t.Errorf("expected empty filter after 4 backspaces, got %q", m.pickerFilter)
	}
	if len(m.pickerFilteredIdx) != 4 {
		t.Fatalf("expected 4 visible rows after filter cleared, got %d", len(m.pickerFilteredIdx))
	}
	// Now navigation must work normally again.
	m = pressKey(m, "j")
	if m.pickerCursor != 1 {
		t.Errorf("expected pickerCursor=1 after j with non-empty list, got %d", m.pickerCursor)
	}
}

// TestPickerEmptyFilterPreviewSafe ensures the picker preview path also
// tolerates an empty filtered index — pickerCurrentWindowID guards before
// dereferencing the cursor.
func TestPickerEmptyFilterPreviewSafe(t *testing.T) {
	m := pickerModel(t)
	m.width, m.height = 120, 40
	m.cfg.Defaults.Preview = pickerPreviewCfg()
	m.pickerPreviewEnabled = true
	m = pressKey(m, "/")
	for _, ch := range "zzzz" {
		m = pressKey(m, string(ch))
	}
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("setup: expected 0 matches, got %d", len(m.pickerFilteredIdx))
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("picker preview panicked on empty filter: %v", r)
		}
	}()
	_ = renderPicker(m)
}
