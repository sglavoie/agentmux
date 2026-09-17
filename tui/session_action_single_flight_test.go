package tui

import (
	"errors"
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
		for attempt := 0; attempt < 2; attempt++ {
			m, cmd := beginLifecycleVia(t, m, "menu", commands.LaunchAnother, row)
			if cmd == nil {
				t.Fatalf("launch another #%d dispatched nothing", attempt+1)
			}
			if got := warningToasts(m); len(got) != 0 {
				t.Fatalf("launch another #%d was refused: %+v", attempt+1, got)
			}
		}
		// The launches are not part of the record the guard reads, so the
		// restart is still the only thing holding the tab.
		m, _ = beginLifecycleVia(t, m, "menu", commands.CloseSession, row)
		if got := m.sessionActions.InFlight(); len(got) != 1 {
			t.Fatalf("in flight = %+v, want the close refused and the restart still held", got)
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
