package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// singleFlightSurfaces are the two dashboard entry points that offer a lifecycle
// action. The guard is one rule in the shared controller, so it must hold
// whichever of them the operator reached the action through — and across them:
// a restart begun in the menu must be seen by the palette a keystroke later.
var singleFlightSurfaces = []string{"menu", "palette"}

// beginLifecycleVia drives one lifecycle action through one entry point to
// whatever it settles on, answering the picker and the confirmation when they
// open. It returns the last command the dashboard produced, which is the
// dispatch's own when the action ran.
//
// The effect is never drained here, so it stays parked: nothing reports back,
// the in-flight record stands, and the next keystroke arrives exactly as it does
// for an operator whose restart has not finished.
func beginLifecycleVia(t *testing.T, m Model, surface string, action commands.ID, row session.RunningSession) (Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	switch surface {
	case "menu":
		m.targetSession = row
		m, cmd = rowActionRunners()[action](m)
	case "palette":
		ctx := captureCommandContext(m)
		if !ctx.HasTarget || ctx.Target.WindowID != row.WindowID {
			t.Fatalf("palette context = %+v, want window %d captured", ctx, row.WindowID)
		}
		m, cmd = commandByID(t, action).Run(m, ctx)
	default:
		t.Fatalf("unknown surface %q", surface)
	}
	if m.mode == ModeConfigPicker {
		m.configPickerCursor = accountIndexOf(t, m, "personal")
		updated, next := m.Update(keyPress(keyEnter))
		m, cmd = updated.(Model), next
	}
	if m.mode == ModeConfirm {
		updated, next := m.Update(keyPress(keyY))
		m, cmd = updated.(Model), next
	}
	return m, cmd
}

// warningToasts is what a refusal leaves behind on the dashboard.
func warningToasts(m Model) []Toast {
	var out []Toast
	for _, toast := range m.toasts {
		if toast.Severity == toastWarning {
			out = append(out, toast)
		}
	}
	return out
}

// TestReviewLifecycleDispatchSingleFlight is the single-flight contract seen
// from the dashboard: at most one close, restart, or profile replacement may be
// in flight against one tab, and the answer does not depend on whether the
// repeat came from the per-row menu or from the palette.
//
// The sequence the finding names is a,r,a,r on one row with confirm_destructive
// off. Both restarts used to validate the same unchanged cached row and both
// used to run, and since a restart launches its replacement before closing the
// original, one row became two.
func TestReviewLifecycleDispatchSingleFlight(t *testing.T) {
	lifecycle := []commands.ID{commands.CloseSession, commands.RestartSession, commands.OverrideProfile}

	assertRefused := func(t *testing.T, m Model, wantInFlight int) {
		t.Helper()
		if got := m.sessionActions.InFlight(); len(got) != wantInFlight {
			t.Fatalf("in flight = %+v, want %d dispatch(es)", got, wantInFlight)
		}
		warnings := warningToasts(m)
		if len(warnings) == 0 {
			t.Fatalf("toasts = %+v, want a warning explaining the refusal", m.toasts)
		}
		if !strings.Contains(warnings[0].Text, sessionactions.ReasonLifecycleInFlight) {
			t.Fatalf("warning %q does not say a lifecycle action is already running", warnings[0].Text)
		}
		if m.mode != ModeList {
			t.Fatalf("mode = %v, want %v: a refusal opens no surface", m.mode, ModeList)
		}
	}

	for _, confirms := range []bool{false, true} {
		for _, first := range singleFlightSurfaces {
			for _, second := range singleFlightSurfaces {
				name := first + "-then-" + second
				if confirms {
					name += "-confirming"
				}
				t.Run("repeat restart "+name, func(t *testing.T) {
					row := sessionActionRow()
					m := sessionActionModel(confirms, row)

					m, cmd := beginLifecycleVia(t, m, first, commands.RestartSession, row)
					if cmd == nil || len(m.sessionActions.InFlight()) != 1 {
						t.Fatalf("first restart: cmd = %v, in flight = %+v, want one dispatch", cmd != nil, m.sessionActions.InFlight())
					}

					// Reopening the surface and beginning again is the case the
					// existing double-Confirm test does not reach: that one
					// answers the same prompt twice, this one starts over.
					for attempt := 0; attempt < 3; attempt++ {
						m, _ = beginLifecycleVia(t, m, second, commands.RestartSession, row)
						assertRefused(t, m, 1)
					}
				})
			}
		}
	}

	// Close, restart, and profile replacement exclude each other. All three end
	// or replace the same harness process, so the second one is not a different
	// request against the row — it is a second replacement of it.
	t.Run("conflicting lifecycle actions", func(t *testing.T) {
		for _, confirms := range []bool{false, true} {
			for _, first := range lifecycle {
				for _, second := range lifecycle {
					name := string(first) + "-then-" + string(second)
					if confirms {
						name += "-confirming"
					}
					t.Run(name, func(t *testing.T) {
						row := sessionActionRow()
						m := sessionActionModel(confirms, row)

						m, cmd := beginLifecycleVia(t, m, "menu", first, row)
						if cmd == nil || len(m.sessionActions.InFlight()) != 1 {
							t.Fatalf("%s: cmd = %v, in flight = %+v, want one dispatch", first, cmd != nil, m.sessionActions.InFlight())
						}
						m, _ = beginLifecycleVia(t, m, "palette", second, row)
						assertRefused(t, m, 1)
					})
				}
			}
		}
	})

	// The close recovery is reserved at dispatch, so a second close that reached
	// an effect would reserve a second undo for one session. Completing every
	// request the dashboard actually minted is what makes the count meaningful:
	// with the guard there is one to complete, without it there were two.
	t.Run("no duplicate close recovery", func(t *testing.T) {
		row := sessionActionRow()
		m := sessionActionModel(false, row)

		m, first := beginLifecycleVia(t, m, "menu", commands.CloseSession, row)
		drainMsgs(t, first)
		m, second := beginLifecycleVia(t, m, "palette", commands.CloseSession, row)
		drainMsgs(t, second)

		// The recovery is asserted before anything else this subtest could fail
		// on, because the duplicate undo is what the duplicate dispatch COSTS:
		// two entries for one session, and an operator who pressed u twice would
		// relaunch it twice.
		for _, dispatch := range m.sessionActions.InFlight() {
			updated, _ := m.Update(commands.ActionDoneMsg{Request: uint64(dispatch.Request)})
			m = updated.(Model)
		}
		if got := m.activeUndoEntries(); len(got) != 1 {
			t.Fatalf("undo entries = %d, want exactly one recovery for one closed session", len(got))
		}
		client := m.client.(*uitestKittyClient)
		if calls := client.CallsFor("CloseTab"); len(calls) != 1 {
			t.Fatalf("CloseTab calls = %+v, want exactly one close to have reached Kitty", calls)
		}
		warnings := warningToasts(m)
		if len(warnings) == 0 || !strings.Contains(warnings[0].Text, sessionactions.ReasonLifecycleInFlight) {
			t.Fatalf("toasts = %+v, want a warning explaining the refused second close", m.toasts)
		}
	})

	// Control: independent targets stay usable. Serializing every lifecycle
	// action across the whole dashboard would be a worse regression than the
	// double dispatch.
	t.Run("distinct rows are independent", func(t *testing.T) {
		first := sessionActionRow()
		second := session.RunningSession{
			TabID: 23, WindowID: 203, Base: "backend", Project: "backend", Profile: "work",
			LaunchID: "launch-2", HarnessSessionID: "sess-2",
		}
		m := sessionActionModel(false, first, second)

		m, _ = beginLifecycleVia(t, m, "menu", commands.RestartSession, first)
		m, _ = beginLifecycleVia(t, m, "menu", commands.CloseSession, second)

		if got := m.sessionActions.InFlight(); len(got) != 2 {
			t.Fatalf("in flight = %+v, want both rows dispatched", got)
		}
		if got := warningToasts(m); len(got) != 0 {
			t.Fatalf("warnings = %+v, want none: the rows are different tabs", got)
		}
	})

	// Control: launching another session ends nothing, so an in-flight restart
	// neither blocks it nor is blocked by it. Two of them are two deliberate
	// launches rather than one repeated keystroke.
	t.Run("launch another stays available", func(t *testing.T) {
		row := sessionActionRow()
		m := sessionActionModel(true, row)

		m, _ = beginLifecycleVia(t, m, "menu", commands.RestartSession, row)
		if got := m.sessionActions.InFlight(); len(got) != 1 {
			t.Fatalf("in flight = %+v, want the restart", got)
		}
		// The model is assigned rather than redeclared, so the second launch runs
		// against the state the first one left. A `:=` here would have shadowed
		// m inside the loop and thrown each launch away, which would have made
		// this a test of one launch run twice from the same start.
		for attempt := 0; attempt < 2; attempt++ {
			var cmd tea.Cmd
			m, cmd = beginLifecycleVia(t, m, "menu", commands.LaunchAnother, row)
			if cmd == nil {
				t.Fatalf("launch another #%d dispatched nothing", attempt+1)
			}
			if got := warningToasts(m); len(got) != 0 {
				t.Fatalf("launch another #%d was refused: %+v", attempt+1, got)
			}
		}
		// Each launch is recorded, but a launch holds nothing: the guard reads
		// only the lifecycle entries, so the restart is still what holds the tab
		// and the close is still refused. Counting the whole record here instead
		// would have measured the launches rather than the guard.
		m, _ = beginLifecycleVia(t, m, "menu", commands.CloseSession, row)
		warnings := warningToasts(m)
		if len(warnings) == 0 || !strings.Contains(warnings[len(warnings)-1].Text, sessionactions.ReasonLifecycleInFlight) {
			t.Fatalf("toasts = %+v, want the close refused by the parked restart", m.toasts)
		}
		if got := lifecycleRecordsFor(m, row.TabID); got != 1 {
			t.Fatalf("lifecycle records on tab %d = %d, want only the parked restart", row.TabID, got)
		}
	})

	// The guard is released by the completion, not by success: a close that
	// failed must leave the row closable again, or one Kitty error would lock a
	// session out of every lifecycle action until the dashboard was restarted.
	t.Run("a reported completion frees the row", func(t *testing.T) {
		for name, done := range map[string]commands.ActionDoneMsg{
			"succeeded":   {},
			"failed":      {Err: errors.New("kitty refused")},
			"unconfirmed": {Unconfirmed: true},
		} {
			t.Run(name, func(t *testing.T) {
				row := sessionActionRow()
				m := sessionActionModel(false, row)

				m, _ = beginLifecycleVia(t, m, "menu", commands.CloseSession, row)
				inflight := m.sessionActions.InFlight()
				if len(inflight) != 1 {
					t.Fatalf("in flight = %+v, want the close", inflight)
				}
				done.Request = uint64(inflight[0].Request)
				updated, _ := m.Update(done)
				m = updated.(Model)
				if got := m.sessionActions.InFlight(); len(got) != 0 {
					t.Fatalf("in flight = %+v, want the completion to have retired the record", got)
				}

				m, cmd := beginLifecycleVia(t, m, "palette", commands.RestartSession, row)
				if cmd == nil {
					t.Fatalf("retry after %s dispatched nothing", name)
				}
				if got := m.sessionActions.InFlight(); len(got) != 1 {
					t.Fatalf("in flight = %+v, want the retry dispatched", got)
				}
			})
		}
	})
}

// dashboardPressureTabs is how many distinct rows the correlation-pressure cases
// drive: above the controller's sixteen-entry record and inside the plan's 8–20
// session target, so the state it reaches is a busy morning rather than a load
// test.
const dashboardPressureTabs = 20

// pressureRows are twenty live rows, each on its own tab, window, and launch.
// They all run under a configured profile, because a row whose profile is not in
// the configuration has no recovery snapshot and the close cases below are about
// the recovery surviving.
func pressureRows() []session.RunningSession {
	out := make([]session.RunningSession, 0, dashboardPressureTabs)
	for i := 0; i < dashboardPressureTabs; i++ {
		out = append(out, session.RunningSession{
			TabID:            300 + i,
			WindowID:         3000 + i,
			Base:             fmt.Sprintf("proj%02d", i+1),
			Project:          fmt.Sprintf("proj%02d", i+1),
			Profile:          "work",
			LaunchID:         fmt.Sprintf("launch-%d", i+1),
			HarnessSessionID: fmt.Sprintf("sess-%d", i+1),
		})
	}
	return out
}

// selectRow puts the dashboard cursor on one row, which is where the palette
// captures its target from. The menu route takes its target from the model field
// beginLifecycleVia sets, so this is what makes the two routes interchangeable
// for a row that is not the first one.
func selectRow(t *testing.T, m Model, row session.RunningSession) Model {
	t.Helper()
	for i := 0; i < m.items.Len(); i++ {
		if s, ok := m.items.SessionAt(i); ok && s.WindowID == row.WindowID {
			m.cursor = i
			return m
		}
	}
	t.Fatalf("window %d is not in the dashboard list", row.WindowID)
	return m
}

// lifecycleRecordsFor counts the in-flight dispatches holding one tab's lifecycle
// exclusion. It is the evidence a refusal actually refused: a second effect would
// have minted a second request against the same tab, whether or not its command
// was ever run.
func lifecycleRecordsFor(m Model, tabID int) int {
	held := 0
	for _, dispatch := range m.sessionActions.InFlight() {
		if sessionactions.Destructive(dispatch.Action) && dispatch.Target.TabID() == tabID {
			held++
		}
	}
	return held
}

// applyDashboardPressure accumulates more dispatches than the correlation record
// holds, without completing any of them: every other row's own lifecycle action,
// through both entry points in turn, and then a run of churn the guard does not
// read — Open session, which ends nothing, and Launch another, which is recorded
// like any dispatch but is deliberately outside the single-flight set.
//
// withCloses says whether the other rows may be CLOSED. The recovery stack has a
// bound of its own, and nineteen other closes would reach it; the cases that
// assert on the held close's recovery pass false so that the only bookkeeping
// under pressure is the controller's, which is what the finding is about.
func applyDashboardPressure(t *testing.T, m Model, rows []session.RunningSession, held session.RunningSession, withCloses bool) Model {
	t.Helper()
	lifecycle := []commands.ID{commands.RestartSession, commands.OverrideProfile}
	if withCloses {
		lifecycle = append(lifecycle, commands.CloseSession)
	}
	drove := 0
	for i, row := range rows {
		if row.WindowID == held.WindowID {
			continue
		}
		surface := singleFlightSurfaces[i%len(singleFlightSurfaces)]
		action := lifecycle[i%len(lifecycle)]
		m = selectRow(t, m, row)
		var cmd tea.Cmd
		m, cmd = beginLifecycleVia(t, m, surface, action, row)
		if cmd == nil {
			t.Fatalf("%s on tab %d via the %s dispatched nothing", action, row.TabID, surface)
		}
		if got := lifecycleRecordsFor(m, row.TabID); got != 1 {
			t.Fatalf("tab %d holds %d lifecycle records after its own %s, want 1", row.TabID, got, action)
		}
		drove++
	}
	// Churn on the held row itself. Neither action ends or replaces the process,
	// so neither is refused and neither holds the tab — but both take a place in
	// the record the held operation is correlated through.
	m = selectRow(t, m, held)
	for i := 0; i < maxDashboardChurn; i++ {
		churn := commands.FocusSession
		if i%4 == 3 {
			churn = commands.LaunchAnother
		}
		var cmd tea.Cmd
		m, cmd = beginLifecycleVia(t, m, singleFlightSurfaces[i%len(singleFlightSurfaces)], churn, held)
		if cmd == nil {
			t.Fatalf("%s #%d on the held row dispatched nothing", churn, i+1)
		}
		if got := warningToasts(m); len(got) != 0 {
			t.Fatalf("%s #%d on the held row was refused: %+v", churn, i+1, got)
		}
		drove++
	}
	if drove <= 16 {
		t.Fatalf("the pressure drove %d dispatches, want more than the controller's record holds", drove)
	}
	return m
}

// maxDashboardChurn is enough non-lifecycle work on the held row to fill the
// correlation record several times over on its own.
const maxDashboardChurn = 18

// TestReviewLifecycleGuardSurvivesCorrelationPressure is the dashboard's half of
// the bound's contract: twenty rows' worth of accumulated, uncompleted work must
// not release the exclusion the operator's own restart holds, and must not lose
// the correlation its completion arrives under.
//
// The controller's record used to drop its oldest entry at sixteen whatever that
// entry was. On this surface that meant a second restart RAN — leaving two
// replacements for one row, since a restart launches before it closes — and the
// first one's result arrived as a completion nobody claimed, so the close it may
// have been never offered its undo.
func TestReviewLifecycleGuardSurvivesCorrelationPressure(t *testing.T) {
	lifecycle := []commands.ID{commands.CloseSession, commands.RestartSession, commands.OverrideProfile}

	// Every lifecycle action held against every lifecycle retry, reached through
	// both entry points, under both confirmation settings.
	for _, confirms := range []bool{false, true} {
		for _, held := range lifecycle {
			for _, retry := range lifecycle {
				for _, surface := range singleFlightSurfaces {
					name := string(held) + "-then-" + string(retry) + "-via-" + surface
					if confirms {
						name += "-confirming"
					}
					t.Run(name, func(t *testing.T) {
						rows := pressureRows()
						m := sessionActionModel(confirms, rows...)
						row := rows[0]

						m = selectRow(t, m, row)
						m, cmd := beginLifecycleVia(t, m, "menu", held, row)
						if cmd == nil {
							t.Fatalf("the held %s dispatched nothing", held)
						}
						m = applyDashboardPressure(t, m, rows, row, held != commands.CloseSession)

						if got := lifecycleRecordsFor(m, row.TabID); got != 1 {
							t.Fatalf("the held row holds %d lifecycle records after the pressure, want the one %s", got, held)
						}
						for attempt := 0; attempt < 3; attempt++ {
							m = selectRow(t, m, row)
							m, _ = beginLifecycleVia(t, m, surface, retry, row)
							if got := lifecycleRecordsFor(m, row.TabID); got != 1 {
								t.Fatalf("retry #%d minted a second lifecycle record: %d held for tab %d", attempt+1, got, row.TabID)
							}
							warnings := warningToasts(m)
							if len(warnings) == 0 || !strings.Contains(warnings[len(warnings)-1].Text, sessionactions.ReasonLifecycleInFlight) {
								t.Fatalf("retry #%d: toasts = %+v, want a warning naming the in-flight operation", attempt+1, m.toasts)
							}
							if m.mode != ModeList {
								t.Fatalf("retry #%d left mode %v, want %v: a refusal opens no surface", attempt+1, m.mode, ModeList)
							}
						}
					})
				}
			}
		}
	}

	// The correlation is the other half of what eviction took, and the close's
	// recovery is where its loss would be visible to an operator: a completion
	// the record can no longer name is discarded, so the undo for a close that
	// really happened would never be offered.
	t.Run("the held close keeps its recovery", func(t *testing.T) {
		rows := pressureRows()
		m := sessionActionModel(false, rows...)
		row := rows[0]

		m = selectRow(t, m, row)
		m, cmd := beginLifecycleVia(t, m, "menu", commands.CloseSession, row)
		if cmd == nil {
			t.Fatal("the held close dispatched nothing")
		}
		inflight := m.sessionActions.InFlight()
		if len(inflight) != 1 {
			t.Fatalf("in flight = %+v, want the close", inflight)
		}
		request := inflight[0].Request

		m = applyDashboardPressure(t, m, rows, row, false)

		updated, _ := m.Update(commands.ActionDoneMsg{Request: uint64(request)})
		m = updated.(Model)
		entries := m.activeUndoEntries()
		if len(entries) != 1 {
			t.Fatalf("undo entries = %d, want exactly one recovery for the one closed session", len(entries))
		}
		if got := lifecycleRecordsFor(m, row.TabID); got != 0 {
			t.Fatalf("the completed close left %d lifecycle records on tab %d, want none", got, row.TabID)
		}
		// And the row is usable again, which is the same fact seen from the
		// operator's side.
		m = selectRow(t, m, row)
		m, retry := beginLifecycleVia(t, m, "palette", commands.RestartSession, row)
		if retry == nil {
			t.Fatal("the retry after the completed close dispatched nothing")
		}
		if got := lifecycleRecordsFor(m, row.TabID); got != 1 {
			t.Fatalf("the retry left %d lifecycle records on tab %d, want one", got, row.TabID)
		}
	})

	// A second close refused under pressure must not reserve a second recovery:
	// the duplicate undo is what the duplicate dispatch costs, and an operator
	// pressing u twice would relaunch one session twice.
	t.Run("no duplicate close recovery under pressure", func(t *testing.T) {
		rows := pressureRows()
		m := sessionActionModel(false, rows...)
		row := rows[0]

		m = selectRow(t, m, row)
		m, _ = beginLifecycleVia(t, m, "menu", commands.CloseSession, row)
		m = applyDashboardPressure(t, m, rows, row, false)
		m = selectRow(t, m, row)
		m, _ = beginLifecycleVia(t, m, "palette", commands.CloseSession, row)

		for _, dispatch := range m.sessionActions.InFlight() {
			if dispatch.Action != commands.CloseSession {
				continue
			}
			updated, _ := m.Update(commands.ActionDoneMsg{Request: uint64(dispatch.Request)})
			m = updated.(Model)
		}
		if got := m.activeUndoEntries(); len(got) != 1 {
			t.Fatalf("undo entries = %d, want exactly one recovery for one closed session", len(got))
		}
	})

	// Release is by completion under pressure exactly as it is without it, for
	// every outcome the host reports. A failed restart that could not be retried
	// would be a worse answer than the duplicate the guard refuses.
	t.Run("a reported completion frees the row", func(t *testing.T) {
		for name, done := range map[string]commands.ActionDoneMsg{
			"succeeded":   {},
			"failed":      {Err: errors.New("kitty refused")},
			"unconfirmed": {Unconfirmed: true},
		} {
			t.Run(name, func(t *testing.T) {
				rows := pressureRows()
				m := sessionActionModel(false, rows...)
				row := rows[0]

				m = selectRow(t, m, row)
				m, _ = beginLifecycleVia(t, m, "menu", commands.RestartSession, row)
				inflight := m.sessionActions.InFlight()
				if len(inflight) != 1 {
					t.Fatalf("in flight = %+v, want the restart", inflight)
				}
				done.Request = uint64(inflight[0].Request)

				m = applyDashboardPressure(t, m, rows, row, true)

				updated, _ := m.Update(done)
				m = updated.(Model)
				if got := lifecycleRecordsFor(m, row.TabID); got != 0 {
					t.Fatalf("a %s completion left %d lifecycle records on tab %d, want none", name, got, row.TabID)
				}
				m = selectRow(t, m, row)
				m, cmd := beginLifecycleVia(t, m, "palette", commands.CloseSession, row)
				if cmd == nil {
					t.Fatalf("the retry after a %s restart dispatched nothing", name)
				}
				if got := lifecycleRecordsFor(m, row.TabID); got != 1 {
					t.Fatalf("the retry after a %s restart left %d lifecycle records, want one", name, got)
				}
			})
		}
	})

	// Control: the pressure is nineteen other rows' work and it stays theirs.
	// Every one of them holds exactly its own exclusion, and completing one frees
	// that row alone.
	t.Run("independent rows stay usable", func(t *testing.T) {
		rows := pressureRows()
		m := sessionActionModel(false, rows...)
		held := rows[0]

		m = selectRow(t, m, held)
		m, _ = beginLifecycleVia(t, m, "menu", commands.RestartSession, held)
		m = applyDashboardPressure(t, m, rows, held, true)

		for _, row := range rows {
			if got := lifecycleRecordsFor(m, row.TabID); got != 1 {
				t.Fatalf("tab %d holds %d lifecycle records, want exactly its own", row.TabID, got)
			}
		}

		other := rows[len(rows)-1]
		var request uint64
		for _, dispatch := range m.sessionActions.InFlight() {
			if sessionactions.Destructive(dispatch.Action) && dispatch.Target.TabID() == other.TabID {
				request = uint64(dispatch.Request)
			}
		}
		if request == 0 {
			t.Fatalf("tab %d holds no lifecycle record to complete", other.TabID)
		}
		updated, _ := m.Update(commands.ActionDoneMsg{Request: request})
		m = updated.(Model)

		m = selectRow(t, m, other)
		m, cmd := beginLifecycleVia(t, m, "menu", commands.RestartSession, other)
		if cmd == nil {
			t.Fatalf("tab %d dispatched nothing after its own completion", other.TabID)
		}
		if got := lifecycleRecordsFor(m, held.TabID); got != 1 {
			t.Fatalf("the held row holds %d lifecycle records, want the one restart it started with", got)
		}
	})
}
