package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/session"
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
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "W:api", Account: "work", State: "bolt", WindowID: 10},
	})
	m = openActionsOverlay(t, m)
	out := stripAnsi(renderActions(m, 46))
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
	m := loadSessions(testModelWithSessions(), []session.RunningSession{
		{TabID: 1, Base: "api", Account: "work", State: "key", WindowID: 10},
	})
	m = openActionsOverlay(t, m)
	out := stripAnsi(renderActions(m, 80))

	for _, frag := range []string{"C   launch new", "x   clear icon", "X   close"} {
		if !strings.Contains(out, frag) {
			t.Errorf("expected actions overlay to contain %q, got:\n%s", frag, out)
		}
	}
	for _, stale := range []string{"x   close", "C   clear"} {
		if strings.Contains(out, stale) {
			t.Errorf("expected stale actions hint %q to be absent, got:\n%s", stale, out)
		}
	}
	if strings.Index(out, "C   launch new") > strings.Index(out, "x   clear icon") {
		t.Errorf("expected dashboard launch to appear before clear icon, got:\n%s", out)
	}
	if strings.Index(out, "x   clear icon") > strings.Index(out, "X   close") {
		t.Errorf("expected clear icon to appear before close, got:\n%s", out)
	}
}

// TestOpenDirActionUnavailableWithoutConfig verifies the `o` action falls
// back to unavailable when no config.Session matches the row's project title.
func TestOpenDirActionUnavailableWithoutConfig(t *testing.T) {
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

// TestActionsOverlayEscReturnsToList verifies esc closes the overlay.
func TestActionsOverlayEscReturnsToList(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc, got %d", m.mode)
	}
}

// TestActionsOverlayQReturnsToList verifies q closes the overlay (not quit).
func TestActionsOverlayQReturnsToList(t *testing.T) {
	m := loadSessions(testModelWithSessions(), makeSessions())
	m = openActionsOverlay(t, m)
	m = pressKey(m, "q")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after q in overlay, got %d", m.mode)
	}
}
