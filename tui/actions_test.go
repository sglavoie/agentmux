package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
)

// testModelWithSessions is defined in model_test.go and seeds the model with
// config sessions matching makeSessions() so per-row actions that resolve a
// config.Session (e.g. `o` open dir) are available.

func openActionsOverlay(t *testing.T, m Model) Model {
	t.Helper()
	updated, _ := m.Update(keyPress("a"))
	out := updated.(Model)
	if out.mode != ModeActions {
		t.Fatalf("expected ModeActions, got %d", out.mode)
	}
	return out
}

// TestRenderActionsOverlayHeader checks the header carries the row's title.
func TestRenderActionsOverlayHeader(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "W:api", Account: "work", State: "bolt", WindowID: 10},
	})
	m = openActionsOverlay(t, m)
	out := stripAnsi(renderActions(m))
	if !strings.Contains(out, "Actions for ") {
		t.Errorf("expected header prefix; got %q", out)
	}
	if !strings.Contains(out, "Actions for api") {
		t.Errorf("expected project-only title in header, got %q", out)
	}
	if strings.Contains(out, "W:api") {
		t.Errorf("expected header to strip legacy title prefix, got %q", out)
	}
}

// TestActionAvailabilityActiveRow verifies the availability matrix on an
// active (bolt) row: peek is available, clear is not, every other action
// is available (open dir requires a matching config session).
func TestActionAvailabilityActiveRow(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "bolt", WindowID: 10},
	})
	m = openActionsOverlay(t, m)
	avail := map[string]bool{}
	for _, a := range actionsFor(m) {
		avail[a.KeyMatch] = a.Available(m)
	}
	want := map[string]bool{
		keyEnter: true, keyE: true, keyR: true, keyC: true,
		keyDashboardLaunch: true, keyClearStatus: false, keyClose: true,
		keyO: true, keyY: true,
	}
	for k, w := range want {
		if avail[k] != w {
			t.Errorf("active row: action %q availability got %v, want %v", k, avail[k], w)
		}
	}
}

// TestActionAvailabilityAttentionRow verifies the matrix on an attention
// (key) row: peek is unavailable, clear is available.
func TestActionAvailabilityAttentionRow(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "key", WindowID: 10},
	})
	m = openActionsOverlay(t, m)
	avail := map[string]bool{}
	for _, a := range actionsFor(m) {
		avail[a.KeyMatch] = a.Available(m)
	}
	if avail[keyE] != false {
		t.Errorf("attention row: peek should be unavailable")
	}
	if avail[keyDashboardLaunch] != true {
		t.Errorf("attention row: launch should be available")
	}
	if avail[keyClearStatus] != true {
		t.Errorf("attention row: clear should be available")
	}
}

// TestActionAvailabilityIdleRow verifies the matrix on an idle row: both
// peek and clear are unavailable.
func TestActionAvailabilityIdleRow(t *testing.T) {
	t.Parallel()

	m := testModelWithSessions()
	m.idleThreshold = time.Minute
	m.lastActivity = map[int]time.Time{10: time.Now().Add(-time.Hour)}
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "", WindowID: 10},
	})
	m = openActionsOverlay(t, m)
	avail := map[string]bool{}
	for _, a := range actionsFor(m) {
		avail[a.KeyMatch] = a.Available(m)
	}
	if avail[keyE] != false {
		t.Errorf("idle row: peek should be unavailable")
	}
	if avail[keyDashboardLaunch] != true {
		t.Errorf("idle row: launch should be available")
	}
	if avail[keyClearStatus] != false {
		t.Errorf("idle row: clear should be unavailable")
	}
}

// TestActionAvailabilityUnlockedRow verifies that on an unlocked row, the
// destructive actions (close/restart/override) and the lock action are all
// available.
func TestActionAvailabilityUnlockedRow(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "bolt", WindowID: 10, Locked: false},
	})
	m = openActionsOverlay(t, m)
	avail := map[string]bool{}
	for _, a := range actionsFor(m) {
		avail[a.KeyMatch] = a.Available(m)
	}
	want := map[string]bool{
		keyR: true, keyClose: true, keyC: true, keyDashboardLaunch: true, keyLock: true,
	}
	for k, w := range want {
		if avail[k] != w {
			t.Errorf("unlocked row: action %q availability got %v, want %v", k, avail[k], w)
		}
	}
}

// TestActionAvailabilityMuteAlwaysOn verifies the mute/unmute entry is
// always available — it must work on muted *and* locked rows so the user
// can quiet a runaway tab without first unlocking it.
func TestActionAvailabilityMuteAlwaysOn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		rs   session.RunningSession
	}{
		{"unmuted-unlocked", session.RunningSession{TabID: 1, Base: "api", Account: "work", State: "bolt", WindowID: 10}},
		{"muted-unlocked", session.RunningSession{TabID: 1, Base: "api", Account: "work", State: "bolt", WindowID: 10, Muted: true}},
		{"unmuted-locked", session.RunningSession{TabID: 1, Base: "api", Account: "work", State: "bolt", WindowID: 10, Locked: true}},
		{"muted-locked", session.RunningSession{TabID: 1, Base: "api", Account: "work", State: "bolt", WindowID: 10, Locked: true, Muted: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := loadSessions(testModelWithSessions(), []session.RunningSession{tc.rs})
			m = openActionsOverlay(t, m)
			found := false
			for _, a := range actionsFor(m) {
				if a.KeyMatch == keyM {
					found = true
					if !a.Available(m) {
						t.Errorf("mute action: expected available on %s row, got false", tc.name)
					}
				}
			}
			if !found {
				t.Errorf("mute action: not present in actionsFor for %s row", tc.name)
			}
		})
	}
}

// TestActionAvailabilityLockedRow verifies that on a locked row, close /
// restart / override are unavailable while the lock toggle itself stays
// available so the user can still unlock.
func TestActionAvailabilityLockedRow(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "bolt", WindowID: 10, Locked: true},
	})
	m = openActionsOverlay(t, m)
	avail := map[string]bool{}
	for _, a := range actionsFor(m) {
		avail[a.KeyMatch] = a.Available(m)
	}
	want := map[string]bool{
		keyR: false, keyClose: false, keyC: false, keyDashboardLaunch: true, keyLock: true,
	}
	for k, w := range want {
		if avail[k] != w {
			t.Errorf("locked row: action %q availability got %v, want %v", k, avail[k], w)
		}
	}
}

func TestActionsOverlayReflectsDashboardRemap(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "key", WindowID: 10},
	})
	m = openActionsOverlay(t, m)
	out := stripAnsi(renderActions(m))

	// Each entry is one row: the label at the left, the immediate key hard
	// against the right edge. The pairing is what this test is about, so it is
	// read off the rendered row rather than by searching for a label and a key
	// independently.
	rows := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := fields[len(fields)-1]
		if len([]rune(key)) != 1 {
			continue
		}
		rows[key] = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), key))
	}
	for key, label := range map[string]string{
		"C": "Launch another session", "x": "Clear status", "X": "End Session",
	} {
		if got := rows[key]; got != label {
			t.Errorf("actions overlay row for %q = %q, want %q; got:\n%s", key, got, label, out)
		}
	}
	// The remap is a swap, so the stale pairings are the interesting negative:
	// `x` used to close and `C` used to clear.
	if rows["x"] == "End Session" || rows["C"] == "Clear status" {
		t.Errorf("stale key mapping survives in:\n%s", out)
	}
	if strings.Index(out, "Launch another session") > strings.Index(out, "Clear status") {
		t.Errorf("expected dashboard launch to appear before clear icon, got:\n%s", out)
	}
	if strings.Index(out, "Clear status") > strings.Index(out, "End Session") {
		t.Errorf("expected clear icon to appear before close, got:\n%s", out)
	}
}

// TestOpenDirActionUnavailableWithoutConfig verifies the `o` action falls
// back to unavailable when no config.Session matches the row's project title.
func TestOpenDirActionUnavailableWithoutConfig(t *testing.T) {
	t.Parallel()

	m := testModel() // no Sessions in cfg
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, Base: "nosuch", Account: "work", State: "bolt"},
	})
	m = openActionsOverlay(t, m)
	for _, a := range actionsFor(m) {
		if a.KeyMatch == keyO && a.Available(m) {
			t.Errorf("expected open dir unavailable without matching config session")
		}
	}
}

// menuCursorOn moves the open menu's selection onto the named action and fails
// the test if it cannot get there. It walks with `j` rather than assigning the
// cursor so the tests below exercise the keymap the operator uses.
func menuCursorOn(t *testing.T, m Model, id commands.ID) Model {
	t.Helper()
	for i := 0; i < len(actionsInput(m).Actions); i++ {
		if actionsInput(m).SelectedID() == id {
			return m
		}
		m = pressKey(m, "j")
	}
	t.Fatalf("the menu never selected %q", id)
	return m
}

// TestActionsMenuEnterOnInitialSelectionFocusesTheSession is the Enter-semantics
// guarantee the selectable menu had to preserve rather than replace: `a` then
// enter used to focus the session because enter was focus-session's own
// mnemonic, and it still does because focus-session is the initial selection.
func TestActionsMenuEnterOnInitialSelectionFocusesTheSession(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "bolt", WindowID: 10},
	})
	m = openActionsOverlay(t, m)
	if got := actionsInput(m).SelectedID(); got != commands.FocusSession {
		t.Fatalf("the menu opened on %q, want the focus entry", got)
	}
	updated, cmd := m.Update(keyPress(keyEnter))
	out := updated.(Model)
	if out.mode != ModeList {
		t.Errorf("mode after enter = %d, want the menu dismissed back to the list", out.mode)
	}
	if cmd == nil {
		t.Error("enter on the focus entry issued no command")
	}
}

// TestActionsMenuEnterRunsTheSelectedEntry is the other half: once the cursor
// has moved, enter must run what is under it rather than the entry that
// declared enter as its mnemonic. A menu where enter always focused would have
// passed the test above and still be wrong.
func TestActionsMenuEnterRunsTheSelectedEntry(t *testing.T) {
	t.Parallel()

	m := testModelWithSessions()
	// Close is the clearest evidence of which entry ran, but only when it stops
	// at a modal: with confirm_destructive off it takes the toast-and-undo path
	// and lands back on the list, which is also where focusing would have left
	// the model.
	m.cfg.Defaults.ConfirmDestructive = true
	m = loadSessions(m, []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "bolt", WindowID: 10},
	})
	m = openActionsOverlay(t, m)
	m = menuCursorOn(t, m, commands.CloseSession)
	updated, _ := m.Update(keyPress(keyEnter))
	out := updated.(Model)
	if out.mode == ModeList {
		t.Fatalf("enter on the close entry returned straight to the list, want its confirmation")
	}
	if out.mode != ModeConfirm {
		t.Fatalf("mode after enter = %d, want ModeConfirm", out.mode)
	}
	if body := stripAnsi(renderConfirm(out)); !strings.Contains(body, "End Session") {
		t.Errorf("the confirmation does not name the selected action:\n%s", body)
	}
}

// TestActionsMenuEnterRefusesAnUnavailableEntry keeps a disabled entry
// selectable without making it runnable — the requirement that it explain
// itself rather than vanish is only safe if reaching it cannot fire it.
func TestActionsMenuEnterRefusesAnUnavailableEntry(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "bolt", WindowID: 10, Locked: true},
	})
	m = openActionsOverlay(t, m)
	m = menuCursorOn(t, m, commands.CloseSession)
	selected, ok := actionsInput(m).SelectedAction()
	if !ok || selected.Available(m) {
		t.Fatalf("close on a locked row reports available=%v, want it refused", ok && selected.Available(m))
	}
	updated, cmd := m.Update(keyPress(keyEnter))
	out := updated.(Model)
	if out.mode != ModeActions {
		t.Errorf("mode after enter = %d, want the menu still open", out.mode)
	}
	if cmd != nil {
		t.Error("enter on an unavailable entry issued a command")
	}
	if got := stripAnsi(renderActions(out)); !strings.Contains(got, "Unavailable:") {
		t.Errorf("the menu does not say why the entry will not run:\n%s", got)
	}
}

// TestActionsMenuOwnsTheKeyboard is the modal-composition requirement seen from
// the keyboard: while the menu is up, a dashboard binding that is not one of its
// own keys does nothing at all. j and k are the interesting pair — they move the
// list one layer down, and a menu that let either through would scroll a list the
// operator cannot see while the selection stayed put.
//
// Mouse input is blocked a layer higher, by View refusing to expose a handler
// outside the two live-dashboard modes; TestViewEnablesMouseOnlyForLiveDashboard
// is what covers it.
func TestActionsMenuOwnsTheKeyboard(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	cursor := m.cursor
	for _, key := range []string{"/", "l", "f", "n", "tab", "?"} {
		updated, _ := m.Update(keyPress(key))
		out := updated.(Model)
		if out.mode != ModeActions {
			t.Errorf("%q left the menu for mode %d", key, out.mode)
		}
		if out.cursor != cursor {
			t.Errorf("%q moved the dashboard cursor from %d to %d", key, cursor, out.cursor)
		}
	}
	// j and k are answered by the menu, so they must move its selection and leave
	// the list's cursor alone.
	moved := pressKey(m, "j")
	if moved.cursor != cursor {
		t.Errorf("j moved the dashboard cursor from %d to %d", cursor, moved.cursor)
	}
	if moved.actionsCursor == m.actionsCursor {
		t.Error("j did not move the menu's own selection")
	}
}

// TestActionsOverlayEscReturnsToList verifies esc closes the overlay.
func TestActionsOverlayEscReturnsToList(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc, got %d", m.mode)
	}
}

// TestActionsOverlayQReturnsToList verifies q closes the overlay (not quit).
func TestActionsOverlayQReturnsToList(t *testing.T) {
	t.Parallel()

	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	m = pressKey(m, "q")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after q in overlay, got %d", m.mode)
	}
}
