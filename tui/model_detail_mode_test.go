package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/session"
)

// detailSessions builds three sessions with non-zero WindowIDs so detail
// mode tests can assert WindowID propagation without false positives from
// the makeSessions() zero-value WindowID field.
func detailSessions() []session.RunningSession {
	return []session.RunningSession{
		{TabID: 1, WindowID: 101, Base: "api", Account: "work", Directory: "/home/user/api"},
		{TabID: 2, WindowID: 102, Base: "backend", Account: "work", Directory: "/home/user/backend", State: "bolt"},
		{TabID: 3, WindowID: 103, Base: "dotfiles", Account: "personal", Directory: "/home/user/dotfiles"},
	}
}

func TestDKeyOpensDetailMode(t *testing.T) {
	m := loadSessions(testModel(), detailSessions())
	want := m.filtered[m.cursor].WindowID
	updated, cmd := m.Update(keyPress("d"))
	got := updated.(Model)
	if got.mode != ModeDetail {
		t.Fatalf("expected ModeDetail after d, got %d", got.mode)
	}
	if got.detailWindowID != want {
		t.Fatalf("expected detailWindowID=%d, got %d", want, got.detailWindowID)
	}
	if !got.detailLoading {
		t.Fatal("expected detailLoading=true while query is in flight")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd after d (session detail query)")
	}
}

func TestDKeyEmptyListIsNoOp(t *testing.T) {
	m := loadSessions(testModel(), nil)
	updated, cmd := m.Update(keyPress("d"))
	got := updated.(Model)
	if got.mode != ModeList {
		t.Fatalf("expected ModeList after d on empty list, got %d", got.mode)
	}
	if cmd != nil {
		t.Fatal("expected nil cmd after d on empty list")
	}
}

func TestDKeyDismissesDeprecationBeforeEnteringDetail(t *testing.T) {
	m := loadSessions(testModel(), detailSessions())
	// Force a visible deprecation banner so keyD is consumed by the
	// banner-dismiss path on first press.
	m.deprecations = []config.Deprecation{{Key: "stale_active_threshold", Replacement: "[defaults.watchdog].active"}}
	m.deprecationDismissed = false

	m = pressKey(m, "d")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after first d (banner dismiss), got %d", m.mode)
	}
	if !m.deprecationDismissed {
		t.Fatal("expected deprecation banner to be dismissed by first d")
	}

	// Second press now opens the detail pane.
	m = pressKey(m, "d")
	if m.mode != ModeDetail {
		t.Fatalf("expected ModeDetail after second d, got %d", m.mode)
	}
}

func TestDetailEventsMsgPopulatesEvents(t *testing.T) {
	m := loadSessions(testModel(), detailSessions())
	{
		mm, _ := m.enterDetailMode(m.filtered[m.cursor].WindowID)
		m = mm.(Model)
	}

	events := []daemon.SessionEvent{
		{At: 1, Event: "PreToolUse", Action: "active", ToolName: "Bash"},
		{At: 2, Event: "PostToolUse", Action: "active", ToolName: "Bash"},
	}
	updated, _ := m.Update(detailEventsMsg{windowID: m.detailWindowID, events: events})
	got := updated.(Model)
	if got.detailLoading {
		t.Fatal("expected detailLoading=false after detailEventsMsg")
	}
	if len(got.detailEvents) != 2 {
		t.Fatalf("expected 2 events stored, got %d", len(got.detailEvents))
	}
	if got.detailEvents[0].ToolName != "Bash" {
		t.Fatalf("expected first event ToolName=Bash, got %q", got.detailEvents[0].ToolName)
	}
}

func TestDetailEventsMsgStaleIsIgnored(t *testing.T) {
	m := loadSessions(testModel(), detailSessions())
	{
		mm, _ := m.enterDetailMode(m.filtered[m.cursor].WindowID)
		m = mm.(Model)
	}

	stale := detailEventsMsg{
		windowID: m.detailWindowID + 9999, // mismatched
		events:   []daemon.SessionEvent{{At: 1, Event: "PreToolUse", Action: "active"}},
	}
	updated, _ := m.Update(stale)
	got := updated.(Model)
	if !got.detailLoading {
		t.Fatal("expected detailLoading to stay true after stale msg")
	}
	if len(got.detailEvents) != 0 {
		t.Fatalf("expected detailEvents to remain empty after stale msg, got %d", len(got.detailEvents))
	}
}

func TestDetailEventsMsgErrorPropagates(t *testing.T) {
	m := loadSessions(testModel(), detailSessions())
	{
		mm, _ := m.enterDetailMode(m.filtered[m.cursor].WindowID)
		m = mm.(Model)
	}

	updated, _ := m.Update(detailEventsMsg{
		windowID: m.detailWindowID,
		err:      errors.New("daemon offline"),
	})
	got := updated.(Model)
	if got.detailLoading {
		t.Fatal("expected detailLoading=false after error response")
	}
	if got.detailErr == nil {
		t.Fatal("expected detailErr to be set after error response")
	}
}

func TestDetailEscReturnsToListAndZerosFields(t *testing.T) {
	m := loadSessions(testModel(), detailSessions())
	{
		mm, _ := m.enterDetailMode(m.filtered[m.cursor].WindowID)
		m = mm.(Model)
	}
	m.detailEvents = []daemon.SessionEvent{{At: 1, Event: "PreToolUse", Action: "active"}}
	m.detailScroll = 3
	m.detailLoading = false

	m = pressKey(m, "esc")
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after esc from detail, got %d", m.mode)
	}
	if m.detailWindowID != 0 || m.detailScroll != 0 || m.detailLoading || m.detailEvents != nil || m.detailErr != nil {
		t.Fatalf("expected all detail fields zeroed after esc, got %+v", struct {
			id      int
			scroll  int
			loading bool
			events  int
			err     error
		}{m.detailWindowID, m.detailScroll, m.detailLoading, len(m.detailEvents), m.detailErr})
	}
}

func TestDetailDKeyToggles(t *testing.T) {
	m := loadSessions(testModel(), detailSessions())
	m = pressKey(m, "d") // open
	if m.mode != ModeDetail {
		t.Fatalf("expected ModeDetail after first d, got %d", m.mode)
	}
	m = pressKey(m, "d") // close
	if m.mode != ModeList {
		t.Fatalf("expected ModeList after second d, got %d", m.mode)
	}
	if m.detailWindowID != 0 {
		t.Fatalf("expected detailWindowID=0 after close, got %d", m.detailWindowID)
	}
}

func TestDetailScrollKeys(t *testing.T) {
	m := loadSessions(testModel(), detailSessions())
	{
		mm, _ := m.enterDetailMode(m.filtered[m.cursor].WindowID)
		m = mm.(Model)
	}
	// Pick a height that produces visible=3 (detailMinVisible) so a 6-event
	// buffer exposes a maxOffset of 3 — enough to exercise both the up and
	// down clamps without dipping into terminal-specific arithmetic.
	m.height = 14 // overlay-aware: detailVisibleHeight(m) == detailMinVisible == 3
	m.detailEvents = []daemon.SessionEvent{
		{At: 1, Event: "PreToolUse", Action: "active"},
		{At: 2, Event: "PostToolUse", Action: "active"},
		{At: 3, Event: "PreToolUse", Action: "active"},
		{At: 4, Event: "PostToolUse", Action: "active"},
		{At: 5, Event: "PreToolUse", Action: "active"},
		{At: 6, Event: "PostToolUse", Action: "active"},
	}
	m.detailLoading = false
	_, visible := detailScrollBounds(m, detailVisibleHeight(m))
	maxOffset := len(m.detailEvents) - visible

	m = pressKey(m, "j")
	if m.detailScroll != 1 {
		t.Fatalf("expected detailScroll=1 after j, got %d", m.detailScroll)
	}
	m = pressKey(m, "k")
	if m.detailScroll != 0 {
		t.Fatalf("expected detailScroll=0 after k, got %d", m.detailScroll)
	}
	// k at 0 stays at 0.
	m = pressKey(m, "k")
	if m.detailScroll != 0 {
		t.Fatalf("expected detailScroll to clamp at 0, got %d", m.detailScroll)
	}
	// j past maxOffset stays at maxOffset.
	for i := 0; i < maxOffset+3; i++ {
		m = pressKey(m, "j")
	}
	if m.detailScroll != maxOffset {
		t.Fatalf("expected detailScroll clamp at maxOffset=%d, got %d", maxOffset, m.detailScroll)
	}
}

func TestHelpListsDetailKey(t *testing.T) {
	m := loadSessions(testModel(), makeSessions())
	m.mode = ModeHelp
	m.height = 0

	out := stripAnsi(renderHelp(m))
	if !strings.Contains(out, "show session detail") {
		t.Errorf("expected help to list the d-key entry, got:\n%s", out)
	}
}

// TestTickInDetailModePreservesPaneState confirms that the refresh tick
// leaves the detail pane mode and windowID intact so the new live-refresh
// query (added in Session 5) re-targets the correct window.
func TestTickInDetailModePreservesPaneState(t *testing.T) {
	m := loadSessions(testModel(), detailSessions())
	{
		mm, _ := m.enterDetailMode(m.filtered[m.cursor].WindowID)
		m = mm.(Model)
	}
	targetID := m.detailWindowID

	updated, cmd := m.Update(tickMsg{})
	got := updated.(Model)
	if got.mode != ModeDetail {
		t.Fatalf("expected mode to remain ModeDetail after tick, got %d", got.mode)
	}
	if got.detailWindowID != targetID {
		t.Fatalf("expected detailWindowID preserved across tick, got %d (want %d)", got.detailWindowID, targetID)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd from tickMsg in ModeDetail")
	}
}

// TestDetailEventsMsgClampsScrollOnShrink pins the clamp added so that a
// refresh which shrinks the event ring (daemon GC, or an empty reply)
// cannot leave detailScroll above the new maxOffset. Without the clamp,
// the next j/k press would have to chew through dead rows before the
// viewport reacts.
func TestDetailEventsMsgClampsScrollOnShrink(t *testing.T) {
	m := loadSessions(testModel(), detailSessions())
	{
		mm, _ := m.enterDetailMode(m.filtered[m.cursor].WindowID)
		m = mm.(Model)
	}
	m.height = 14 // overlay-aware: detailVisibleHeight(m) == detailMinVisible == 3
	m.width = 100
	m.detailEvents = makeEvents(60)
	_, visible := detailScrollBounds(m, detailVisibleHeight(m))
	m.detailScroll = len(m.detailEvents) - visible // park at bottom

	// Now deliver a tiny event ring — the daemon GC'd most entries.
	small := makeEvents(2)
	updated, _ := m.Update(detailEventsMsg{windowID: m.detailWindowID, events: small})
	got := updated.(Model)

	total, vis := detailScrollBounds(got, detailVisibleHeight(got))
	maxOffset := total - vis
	if maxOffset < 0 {
		maxOffset = 0
	}
	if got.detailScroll > maxOffset {
		t.Fatalf("expected detailScroll clamped to maxOffset=%d after shrink, got %d", maxOffset, got.detailScroll)
	}
	if got.detailScroll < 0 {
		t.Fatalf("expected detailScroll >= 0 after shrink, got %d", got.detailScroll)
	}
}

// TestDetailEventsMsgIgnoredOutsideDetailMode pins the mode guard added in
// Session 5: a slow daemon roundtrip arriving after the pane was closed
// must not repopulate detailEvents. Sets detailWindowID directly so the
// windowID guard alone cannot account for the drop.
func TestDetailEventsMsgIgnoredOutsideDetailMode(t *testing.T) {
	m := loadSessions(testModel(), detailSessions())
	m.detailWindowID = m.filtered[m.cursor].WindowID
	// Mode intentionally stays ModeList — exercises the new m.mode guard.

	updated, _ := m.Update(detailEventsMsg{
		windowID: m.detailWindowID,
		events:   []daemon.SessionEvent{{At: 1, Event: "PreToolUse", Action: "active"}},
	})
	got := updated.(Model)
	if len(got.detailEvents) != 0 {
		t.Fatalf("expected detailEvents empty when mode != ModeDetail, got %d", len(got.detailEvents))
	}
	if got.detailErr != nil {
		t.Fatalf("expected detailErr unchanged when mode != ModeDetail, got %v", got.detailErr)
	}
}
