package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/asl"
	"github.com/sglavoie/agentmux/tui/commands"
)

func aslRunsDriver(t *testing.T) (*uitestDriver, *uitestKittyClient) {
	t.Helper()
	d, client := newUITestFixtureDriver(aslRunsFixture(), UITestSize{})
	t.Cleanup(func() { _ = d.Close() })
	return d, client
}

func enterASLRunsDriver(t *testing.T) (*uitestDriver, *uitestKittyClient) {
	t.Helper()
	d, client := aslRunsDriver(t)
	// Named rather than typed: this helper's callers are about what the Runs
	// view does, not about which letter opened it. The literal binding keeps its
	// own coverage in the toggle test below and in the binding table's tests.
	if err := d.Command(commands.ShowRuns); err != nil {
		t.Fatalf("opening Runs: %v", err)
	}
	if got := d.Model().mode; got != ModeASLRuns {
		t.Fatalf("mode after opening Runs = %v, want ModeASLRuns", got)
	}
	if len(d.Model().aslRows) != 5 {
		t.Fatalf("loaded %d rows, want 5", len(d.Model().aslRows))
	}
	return d, client
}

// selectASLRun moves the Runs cursor onto a row by ID. Selecting by identity
// rather than by a fixed number of keystrokes keeps a test targeting the run it
// names when the fixture gains a row.
func selectASLRun(t *testing.T, d *uitestDriver, rowID string) {
	t.Helper()
	want := -1
	for i, row := range d.Model().aslRows {
		if row.RowID == rowID {
			want = i
			break
		}
	}
	if want < 0 {
		t.Fatalf("fixture has no run %q", rowID)
	}
	for d.Model().aslCursor != want {
		key := "j"
		if d.Model().aslCursor > want {
			key = "k"
		}
		if err := d.Key(key); err != nil {
			t.Fatalf("moving the Runs cursor: %v", err)
		}
	}
}

func aslRowIDs(rows []aslbridge.Row) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.RowID)
	}
	return ids
}

func TestASLRunsEntryAndExitLeaveDashboardUntouched(t *testing.T) {
	t.Parallel()

	d, _ := aslRunsDriver(t)
	if err := d.Key("j"); err != nil {
		t.Fatalf("moving the dashboard cursor: %v", err)
	}
	before := d.Model()
	wantCursor, wantFilter, wantSessions := before.cursor, before.filterText, len(before.filtered)
	if wantCursor == 0 {
		t.Fatalf("dashboard cursor did not move, so exit cannot be verified")
	}

	// `A` no longer opens the view from the dashboard — Show Runs is a palette
	// command now, and `A` survives only inside the view, as the key that closes
	// the surface it used to open.
	if err := d.Command(commands.ShowRuns); err != nil {
		t.Fatalf("entering Runs: %v", err)
	}
	if err := d.Key(keyEsc); err != nil {
		t.Fatalf("leaving Runs: %v", err)
	}

	after := d.Model()
	if after.mode != ModeList {
		t.Fatalf("mode after esc = %v, want ModeList", after.mode)
	}
	if after.cursor != wantCursor || after.filterText != wantFilter || len(after.filtered) != wantSessions {
		t.Fatalf("dashboard changed: cursor %d->%d filter %q->%q rows %d->%d",
			wantCursor, after.cursor, wantFilter, after.filterText, wantSessions, len(after.filtered))
	}
	// And `A` is inert at the dashboard rather than rebound: pressing it where it
	// used to open the view leaves the dashboard exactly where esc left it.
	if err := d.Key(keyASLRuns); err != nil {
		t.Fatalf("pressing %q at the dashboard: %v", keyASLRuns, err)
	}
	if got := d.Model().mode; got != ModeList {
		t.Fatalf("%q at the dashboard left mode %v, want ModeList", keyASLRuns, got)
	}

	// Re-entering by name, `A` still closes from inside the view.
	if err := d.Command(commands.ShowRuns); err != nil {
		t.Fatalf("re-entering Runs: %v", err)
	}
	if got := d.Model().mode; got != ModeASLRuns {
		t.Fatalf("mode after re-entering = %v, want ModeASLRuns", got)
	}
	if err := d.Key(keyASLRuns); err != nil {
		t.Fatalf("toggling Runs off: %v", err)
	}
	if got := d.Model().mode; got != ModeList {
		t.Fatalf("mode after toggling off = %v, want ModeList", got)
	}
}

func TestASLRunsCommandPaletteEntryOpensTheView(t *testing.T) {
	t.Parallel()

	d, _ := aslRunsDriver(t)
	m := d.Model()
	updated, cmd := commandByID(t, commands.ShowRuns).Run(m, captureCommandContext(m))
	m = updated
	if m.mode != ModeASLRuns {
		t.Fatalf("palette command left mode at %v, want ModeASLRuns", m.mode)
	}
	if cmd == nil {
		t.Fatal("palette command did not start a snapshot fetch")
	}
}

func TestASLRunsSortNeedsActionThenUrgencyThenActivity(t *testing.T) {
	t.Parallel()

	d, _ := enterASLRunsDriver(t)
	want := []string{"row-reply", "row-paused", "row-orphan", "row-verify", "row-active"}
	if got := aslRowIDs(d.Model().aslRows); !equalStrings(got, want) {
		t.Fatalf("sorted rows = %v, want %v", got, want)
	}

	// Ties on group and rank fall back to recent activity, then label, then
	// plan directory, so equal rows keep a stable order across refreshes.
	now := time.Now().UTC()
	tied := []aslbridge.Row{
		{RowID: "b", Label: "beta", UrgencyGroup: "in-progress", UrgencyRank: 50, PlanDir: "/plans/b"},
		{RowID: "recent", Label: "alpha", UrgencyGroup: "in-progress", UrgencyRank: 50, LastActivityAt: now.Format(time.RFC3339)},
		{RowID: "a", Label: "alpha", UrgencyGroup: "in-progress", UrgencyRank: 50, PlanDir: "/plans/a"},
	}
	if got := aslRowIDs(asl.SortRows(tied)); !equalStrings(got, []string{"recent", "a", "b"}) {
		t.Fatalf("tie-broken rows = %v, want [recent a b]", got)
	}
}

func TestASLRunsRefreshKeepsSelectionAndNeverNavigates(t *testing.T) {
	t.Parallel()

	d, _ := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-verify")
	selected := d.Model().aslRows[d.Model().aslCursor].RowID
	if selected != "row-verify" {
		t.Fatalf("selected row = %q, want row-verify", selected)
	}

	// A snapshot that reorders the rows must follow the selection rather than
	// leave the cursor on whichever run happens to land at that index.
	m := d.Model()
	reordered := append([]aslbridge.Row(nil), m.aslRows...)
	reordered[0], reordered[2] = reordered[2], reordered[0]
	d.Inject(aslSnapshotLoadedMsg{
		seq:      m.aslRefreshSeq,
		snapshot: aslbridge.Snapshot{Rows: reordered},
		meta:     aslbridge.SnapshotMeta{ReceivedAt: time.Now()},
		status:   aslRunsFixture().ASLStatus,
	})
	after := d.Model()
	if got := after.aslRows[after.aslCursor].RowID; got != selected {
		t.Fatalf("selection after refresh = %q, want %q", got, selected)
	}

	// A stale response must not overwrite the rows the view already holds.
	d.Inject(aslSnapshotLoadedMsg{seq: m.aslRefreshSeq - 1, status: aslbridge.Status{}})
	if got := aslRowIDs(d.Model().aslRows); !equalStrings(got, aslRowIDs(after.aslRows)) {
		t.Fatalf("stale response replaced rows: %v", got)
	}
}

func TestASLSnapshotArrivingOnDashboardDoesNotNavigate(t *testing.T) {
	t.Parallel()

	d, client := aslRunsDriver(t)
	before := d.Model()
	d.Inject(aslSnapshotLoadedMsg{
		seq:      before.aslRefreshSeq,
		snapshot: aslbridge.Snapshot{Rows: aslRunsFixture().ASLRows},
		meta:     aslbridge.SnapshotMeta{ReceivedAt: time.Now()},
		status:   aslRunsFixture().ASLStatus,
	})
	after := d.Model()
	if after.mode != ModeList {
		t.Fatalf("mode after snapshot = %v, want ModeList", after.mode)
	}
	if after.cursor != before.cursor {
		t.Fatalf("cursor moved from %d to %d on a snapshot", before.cursor, after.cursor)
	}
	if calls := client.CallsFor("FocusTab"); len(calls) != 0 {
		t.Fatalf("snapshot focused Kitty: %v", calls)
	}
	if len(d.WorkbenchOpens()) != 0 {
		t.Fatalf("snapshot opened the Workbench: %v", d.WorkbenchOpens())
	}
}

func TestASLRunsStaleBannerThenUnavailable(t *testing.T) {
	t.Parallel()

	d, _ := enterASLRunsDriver(t)
	m := d.Model()
	m.aslMeta = aslbridge.SnapshotMeta{ReceivedAt: time.Now().Add(-3 * time.Second), Stale: true}

	if got := aslView(m); got != asl.ViewStale {
		t.Fatalf("view inside the stale window = %v, want asl.ViewStale", got)
	}
	rendered := renderASLRuns(m)
	if !strings.Contains(rendered, aslBannerText(m)) {
		t.Fatalf("stale render is missing the read-only banner:\n%s", rendered)
	}
	if !strings.Contains(rendered, "api-refactor") {
		t.Fatalf("stale render dropped the retained rows:\n%s", rendered)
	}

	m.aslMeta.ReceivedAt = time.Now().Add(-aslbridge.StaleWindow - time.Second)
	if got := aslView(m); got != asl.ViewUnavailable {
		t.Fatalf("view past the stale window = %v, want asl.ViewUnavailable", got)
	}
	if rendered := renderASLRuns(m); !strings.Contains(rendered, "unavailable") {
		t.Fatalf("expired render does not say unavailable:\n%s", rendered)
	}
}

func TestASLRunsUnavailableRatherThanEmpty(t *testing.T) {
	t.Parallel()

	fx := aslRunsFixture()
	fx.ASLRows = nil
	fx.ASLStatus = aslbridge.Status{Reason: "the agent-session-loop bridge is not running"}
	d, _ := newUITestFixtureDriver(fx, UITestSize{})
	defer func() { _ = d.Close() }()
	if err := d.Command(commands.ShowRuns); err != nil {
		t.Fatalf("entering Runs: %v", err)
	}

	m := d.Model()
	if got := aslView(m); got != asl.ViewUnavailable {
		t.Fatalf("view without a snapshot = %v, want asl.ViewUnavailable", got)
	}
	rendered := renderASLRuns(m)
	if !strings.Contains(rendered, "unavailable") || !strings.Contains(rendered, fx.ASLStatus.Reason) {
		t.Fatalf("unavailable render is missing the verdict or its reason:\n%s", rendered)
	}
	if strings.Contains(rendered, "0 need action") {
		t.Fatalf("unavailable render shows a count as if the list were simply empty:\n%s", rendered)
	}
}

func TestASLMarkersRequireAnExactBinding(t *testing.T) {
	t.Parallel()

	d, _ := enterASLRunsDriver(t)
	m := d.Model()

	byWindow := make(map[int]session.RunningSession, len(m.sessions))
	for _, candidate := range m.sessions {
		byWindow[candidate.WindowID] = candidate
	}
	if len(byWindow) != 4 {
		t.Fatalf("fixture produced %d live sessions, want 4", len(byWindow))
	}

	if marker, ok := aslMarkerFor(m, byWindow[11]); !ok || marker != asl.StateMarker("paused-for-reply") {
		t.Fatalf("exactly bound session marker = (%q, %v), want the paused-for-reply marker", marker, ok)
	}
	if marker, ok := aslMarkerFor(m, byWindow[31]); !ok || marker != asl.StateMarker("running") {
		t.Fatalf("exactly bound running session marker = (%q, %v)", marker, ok)
	}
	// row-verify matches both gateway sessions on workspace and harness, which
	// is exactly the guess that must not be painted onto a dashboard row.
	for _, windowID := range []int{21, 22} {
		if marker, ok := aslMarkerFor(m, byWindow[windowID]); ok {
			t.Fatalf("window %d took marker %q from an inexact match", windowID, marker)
		}
	}

	// An unavailable integration must not mark anything at all.
	m.aslStatus = aslbridge.Status{}
	m.aslMeta = aslbridge.SnapshotMeta{ReceivedAt: time.Now().Add(-time.Hour)}
	if marker, ok := aslMarkerFor(m, byWindow[11]); ok {
		t.Fatalf("unavailable integration still marked a row with %q", marker)
	}
}

func TestASLFocusExactBinding(t *testing.T) {
	t.Parallel()

	d, client := enterASLRunsDriver(t)
	if err := d.Key(keyF); err != nil {
		t.Fatalf("focusing: %v", err)
	}
	calls := client.CallsFor("FocusTab")
	if len(calls) != 1 {
		t.Fatalf("FocusTab calls = %d, want 1 (%v)", len(calls), calls)
	}
	if d.Model().mode != ModeASLRuns {
		t.Fatalf("exact focus changed mode to %v", d.Model().mode)
	}
	if d.Model().aslErr != nil {
		t.Fatalf("exact focus reported %v", d.Model().aslErr)
	}
}

// TestASLFocusOnEnter keeps Runs in line with every other list, where Enter
// acts on the selected row: here that action is focus, the same as f.
func TestASLFocusOnEnter(t *testing.T) {
	t.Parallel()

	d, client := enterASLRunsDriver(t)
	if err := d.Key(keyEnter); err != nil {
		t.Fatalf("focusing with enter: %v", err)
	}
	if calls := client.CallsFor("FocusTab"); len(calls) != 1 {
		t.Fatalf("FocusTab calls = %d, want 1 (%v)", len(calls), calls)
	}
	if d.Model().mode != ModeASLRuns {
		t.Fatalf("enter focus changed mode to %v", d.Model().mode)
	}
}

func TestASLFocusFallsBackToASingleWorkspaceAndHarnessMatch(t *testing.T) {
	t.Parallel()

	d, client := enterASLRunsDriver(t)
	m := d.Model()
	// row-verify carries no window binding; leaving one gateway session live
	// makes the workspace and harness fallback unambiguous.
	kept := m.sessions[:0]
	for _, candidate := range m.sessions {
		if candidate.WindowID != 22 {
			kept = append(kept, candidate)
		}
	}
	m.sessions = kept
	for i, row := range m.aslRows {
		if row.RowID == "row-verify" {
			m.aslCursor = i
		}
	}
	if m.aslRows[m.aslCursor].RowID != "row-verify" {
		t.Fatalf("cursor row = %q, want row-verify", m.aslRows[m.aslCursor].RowID)
	}

	updated, cmd := m.focusSelectedASLRun()
	m = updated.(Model)
	if m.mode != ModeASLRuns {
		t.Fatalf("a single fallback match raised the picker: mode %v", m.mode)
	}
	if cmd == nil {
		t.Fatal("a single fallback match did not focus anything")
	}
	cmd()
	if calls := client.CallsFor("FocusTab"); len(calls) != 1 {
		t.Fatalf("FocusTab calls = %d, want 1 (%v)", len(calls), calls)
	}
}

func TestASLFocusAmbiguousOpensPicker(t *testing.T) {
	t.Parallel()

	d, client := enterASLRunsDriver(t)
	selectASLRun(t, d, "row-verify")
	if err := d.Key(keyF); err != nil {
		t.Fatalf("focusing the ambiguous run: %v", err)
	}
	m := d.Model()
	if m.mode != ModeLiveSessionPicker {
		t.Fatalf("mode after an ambiguous focus = %v, want ModeLiveSessionPicker", m.mode)
	}
	if len(m.livePickerCandidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(m.livePickerCandidates))
	}
	if m.livePickerPurpose != LivePickerASLFocus {
		t.Fatalf("picker purpose = %v, want LivePickerASLFocus", m.livePickerPurpose)
	}
	if calls := client.CallsFor("FocusTab"); len(calls) != 0 {
		t.Fatalf("ambiguous focus guessed a session: %v", calls)
	}

	if err := d.Key(keyEnter); err != nil {
		t.Fatalf("choosing a candidate: %v", err)
	}
	if calls := client.CallsFor("FocusTab"); len(calls) != 1 {
		t.Fatalf("FocusTab calls after choosing = %d, want 1 (%v)", len(calls), calls)
	}
	if got := d.Model().mode; got != ModeASLRuns {
		t.Fatalf("mode after choosing = %v, want ModeASLRuns", got)
	}
}

func TestASLFocusWithoutAnyCandidateExplainsItself(t *testing.T) {
	t.Parallel()

	d, client := enterASLRunsDriver(t)
	if err := d.Key("j"); err != nil { // row-orphan has no live workspace
		t.Fatalf("moving: %v", err)
	}
	if err := d.Key(keyF); err != nil {
		t.Fatalf("focusing the unmatched run: %v", err)
	}
	m := d.Model()
	if m.mode != ModeASLRuns {
		t.Fatalf("mode after an unmatched focus = %v, want ModeASLRuns", m.mode)
	}
	if m.aslErr == nil || !strings.Contains(m.aslErr.Error(), "no live session") {
		t.Fatalf("unmatched focus reason = %v, want an explanation", m.aslErr)
	}
	if calls := client.CallsFor("FocusTab"); len(calls) != 0 {
		t.Fatalf("unmatched focus still called Kitty: %v", calls)
	}
	if !strings.Contains(renderStatusBar(m), "no live session") {
		t.Fatalf("status bar hides the focus reason: %q", renderStatusBar(m))
	}
}

func TestASLWorkbenchOpensStateSpecificLinkWithoutRetainingIt(t *testing.T) {
	t.Parallel()

	d, _ := enterASLRunsDriver(t)
	if err := d.Key(keyASLWorkbench); err != nil {
		t.Fatalf("opening the Workbench: %v", err)
	}
	opens := d.WorkbenchOpens()
	if len(opens) != 1 {
		t.Fatalf("Workbench opens = %d, want 1", len(opens))
	}
	if opens[0].RowID != "row-reply" || opens[0].Focus != aslbridge.FocusReply {
		t.Fatalf("open = %+v, want row-reply focused on the reply", opens[0])
	}

	// The verifying run has to map to a different Workbench view.
	selectASLRun(t, d, "row-verify")
	if err := d.Key(keyASLWorkbench); err != nil {
		t.Fatalf("opening the Workbench: %v", err)
	}
	opens = d.WorkbenchOpens()
	if len(opens) != 2 || opens[1].Focus != aslbridge.FocusVerification {
		t.Fatalf("second open = %+v, want the verification view", opens)
	}
}

func TestASLWorkbenchFailureStaysGenericAndLeaksNothing(t *testing.T) {
	t.Parallel()

	d, _ := enterASLRunsDriver(t)
	m := d.Model()
	updated, _ := m.handleASLWorkbenchDone(aslWorkbenchDoneMsg{
		rowID: "row-reply", focus: aslbridge.FocusReply, err: errASLWorkbenchOpen,
	})
	m = updated.(Model)
	if m.aslErr == nil {
		t.Fatal("a failed Workbench open reported nothing")
	}
	surfaces := []string{m.aslErr.Error(), renderStatusBar(m), renderASLRuns(m)}
	for _, surface := range surfaces {
		for _, secret := range []string{"http://", "https://", "token", "Bearer"} {
			if strings.Contains(surface, secret) {
				t.Fatalf("surface leaks %q: %s", secret, surface)
			}
		}
	}
}

func TestOpenBrowserURLRefusesNonHTTPSchemes(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"file:///etc/passwd", "javascript:alert(1)", "", "https://", "not a url"} {
		err := commands.OpenBrowserURL(raw)
		if err == nil {
			t.Fatalf("openBrowserURL(%q) accepted the input", raw)
		}
		if strings.Contains(err.Error(), raw) && raw != "" {
			t.Fatalf("openBrowserURL(%q) echoed the input back: %v", raw, err)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
