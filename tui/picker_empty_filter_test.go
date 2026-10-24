package tui

import (
	"strings"
	"testing"
)

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
// narrowing the picker filter to zero rows. Excludes `enter`, whose
// nothing-to-launch warning is covered separately.
func TestPickerEmptyFilterNavigationIsSafe(t *testing.T) {
	t.Parallel()

	m := pickerModel(t)
	m = pressKey(m, "/")
	for _, ch := range "zzzz" {
		m = pressKey(m, string(ch))
	}
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("setup: expected 0 matches for 'zzzz', got %d", len(m.pickerFilteredIdx))
	}
	m = pressKey(m, "enter") // exit search mode; filter kept
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

// TestPickerEnterOnEmptyFilterStaysWithWarning drives `enter` while the
// picker filter matches zero rows. With nothing to launch the launcher stays
// open, does not panic, and says why in a warning toast.
func TestPickerEnterOnEmptyFilterStaysWithWarning(t *testing.T) {
	t.Parallel()

	m := pickerModel(t)
	m = pressKey(m, "/")
	for _, ch := range "zzzz" {
		m = pressKey(m, string(ch))
	}
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("setup: expected 0 matches, got %d", len(m.pickerFilteredIdx))
	}
	m = pressKey(m, "enter") // search → nav
	updated, _ := m.Update(keyPress("enter"))
	m2 := updated.(Model)
	if m2.mode != ModePicker {
		t.Errorf("expected ModePicker after enter with nothing to launch, got %d", m2.mode)
	}
	if n := len(m2.toasts); n == 0 || m2.toasts[n-1].Severity != toastWarning ||
		!strings.Contains(m2.toasts[n-1].Text, "Nothing to launch") {
		t.Errorf("expected a 'Nothing to launch' warning toast, got %+v", m2.toasts)
	}
	if m2.launchBatchID != m.launchBatchID {
		t.Errorf("expected no launch batch, batch ID moved %d → %d", m.launchBatchID, m2.launchBatchID)
	}
}

// TestPickerEnterLaunchesHighlightedWithNothingChecked pins the README's
// "highlight your project, and press Enter": with no checkbox ticked, Enter
// launches the highlighted project rather than nothing.
func TestPickerEnterLaunchesHighlightedWithNothingChecked(t *testing.T) {
	t.Parallel()

	m := pickerModel(t)
	if len(m.pickerFilteredIdx) == 0 {
		t.Fatal("setup: expected visible projects")
	}
	todo, hidden := m.pickerInput().LaunchQueue()
	if len(todo) != 1 || hidden != 0 {
		t.Fatalf("expected the highlighted project queued, got %d (hidden %d)", len(todo), hidden)
	}
	want := m.pickerSessions[m.pickerFilteredIdx[m.pickerCursor]].Title
	if todo[0].Session.Title != want {
		t.Errorf("queued %q, want highlighted %q", todo[0].Session.Title, want)
	}
	updated, cmd := m.Update(keyPress("enter"))
	if updated.(Model).mode != ModeList || cmd == nil {
		t.Errorf("expected a launch and ModeList, got mode=%d cmd=%v", updated.(Model).mode, cmd != nil)
	}
}

// TestPickerEnterSkipsCheckedProjectsTheFilterHides verifies a checked
// project the filter hides is not launched, and is counted for the warning.
func TestPickerEnterSkipsCheckedProjectsTheFilterHides(t *testing.T) {
	t.Parallel()

	m := pickerModel(t)
	if len(m.pickerSessions) < 2 {
		t.Fatal("setup: expected at least two projects")
	}
	m.pickerSelected = map[int]bool{0: true, 1: true}
	m.pickerFilteredIdx = []int{1}
	m.pickerCursor = 0
	todo, hidden := m.pickerInput().LaunchQueue()
	if len(todo) != 1 || hidden != 1 {
		t.Fatalf("expected 1 queued and 1 hidden, got %d and %d", len(todo), hidden)
	}
	if todo[0].Session.Title != m.pickerSessions[1].Title {
		t.Errorf("queued %q, want the visible checked %q", todo[0].Session.Title, m.pickerSessions[1].Title)
	}

	m.pickerFilteredIdx = nil
	todo, hidden = m.pickerInput().LaunchQueue()
	if len(todo) != 0 || hidden != 2 {
		t.Fatalf("expected nothing queued and 2 hidden, got %d and %d", len(todo), hidden)
	}
}

// TestPickerBackspaceRecoversFromEmptyFilter verifies the documented
// recovery path: when the filter collapses the list to zero rows, every
// backspace re-applies the filter and resets pickerCursor to 0 so the user
// can navigate the moment matches return.
func TestPickerBackspaceRecoversFromEmptyFilter(t *testing.T) {
	t.Parallel()

	m := pickerModel(t)
	m = pressKey(m, "/")
	for _, ch := range "zzzz" {
		m = pressKey(m, string(ch))
	}
	if len(m.pickerFilteredIdx) != 0 {
		t.Fatalf("setup: expected 0 matches, got %d", len(m.pickerFilteredIdx))
	}
	// Pre-move the cursor in nav mode to confirm backspace resets it.
	m = pressKey(m, "enter") // search → nav, filter "zzzz" kept
	m.pickerCursor = 99      // simulate a stale cursor value
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
	t.Parallel()

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
