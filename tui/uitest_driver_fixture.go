//go:build tuitest

package tui

import (
	"errors"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/tui/commands"
)

// This file binds a built-in fixture to a driver: the fixture's windows, plans,
// worktrees, and agent-session-loop rows become the answers the offline command
// seams give back. The seams themselves are declared in
// uitest_driver_hermetic.go and the fixtures in uitest_fixtures.go.

// newUITestFixtureDriver builds a driver from a built-in fixture: it constructs
// the model with the fixture's config, applies size, and loads the fixture's running
// sessions synchronously so the list is populated without the refresh ticker
// (whose tea.Tick commands the harness skips before launch). Each zero dimension falls back
// independently to the fixture's own default for that dimension, so a caller can
// override width alone (or height alone) without zeroing the other.
func newUITestFixtureDriver(fx uitestFixture, size UITestSize) *uitestDriver {
	if size.Width == 0 {
		size.Width = fx.Size.Width
	}
	if size.Height == 0 {
		size.Height = fx.Size.Height
	}
	d := newUITestDriver(fx.Config, nil, size)
	d.model.flightRead = fixtureFlightReader(fx.FlightEvents)
	remainingReads := fx.TextFailures
	var readMu sync.Mutex
	d.model.backgroundCommands.scrollback = func(int) (string, error) {
		readMu.Lock()
		defer readMu.Unlock()
		if remainingReads > 0 {
			remainingReads--
			return "", errUITestPreviewRead
		}
		return fx.Text, nil
	}
	rows := fx.runningSessions()
	rows = append(rows, fx.Portable...)
	d.model.backgroundCommands.topologyRefresh = hermeticPortableRefresh(fx.overlay(), rows)
	if fx.Health != nil {
		// Honor the two flags the way commands.Health does, so a fixture cannot
		// make a daemon-only refresh silently deliver a hook summary. The model
		// stamps each section's checked-at clock from the flag it asked with,
		// and a reply that answered more than it was asked would make the
		// staleness rule untestable.
		fixtureHealth := *fx.Health
		d.model.backgroundCommands.health = func(_ Model, checkDaemon, checkHooks bool) tea.Cmd {
			return func() tea.Msg {
				msg := commands.HealthMsg{DaemonChecked: checkDaemon, HookChecked: checkHooks}
				if checkDaemon {
					msg.DaemonAlive = fixtureHealth.DaemonAlive
				}
				if checkHooks {
					msg.HookSummary = fixtureHealth.HookSummary
					msg.HookErr = fixtureHealth.HookErr
				}
				return msg
			}
		}
	}
	fixtureWorktrees := append([]actions.WorktreeView(nil), fx.Worktrees...)
	// No terminal exists to hand a pager, so what would have been paged is
	// kept for the test to read and the pager returns at once.
	d.model.backgroundCommands.pageText = func(text string, done func(error) tea.Msg) tea.Cmd {
		d.pagedText = append(d.pagedText, text)
		return func() tea.Msg { return done(nil) }
	}
	d.model.backgroundCommands.worktrees = func(_ Model, seq int) tea.Cmd {
		return func() tea.Msg {
			return worktreesLoadedMsg{seq: seq, entries: append([]actions.WorktreeView(nil), fixtureWorktrees...)}
		}
	}
	d.model.backgroundCommands.worktreeAction = func(_ Model, id actions.ID, request actions.Request) tea.Cmd {
		return fixtureWorktreeActionCmd(d.recordTerminalAction, &fixtureWorktrees, id, request)
	}
	fixtureRows := append([]aslbridge.Row(nil), fx.ASLRows...)
	fixtureStatus := fx.ASLStatus
	d.model.backgroundCommands.aslSnapshot = func(_ Model, seq int) tea.Cmd {
		return func() tea.Msg {
			if !fixtureStatus.Available {
				return aslSnapshotLoadedMsg{seq: seq, status: fixtureStatus}
			}
			return aslSnapshotLoadedMsg{
				seq:      seq,
				snapshot: aslbridge.Snapshot{Rows: append([]aslbridge.Row(nil), fixtureRows...)},
				meta:     aslbridge.SnapshotMeta{ReceivedAt: time.Now()},
				status:   fixtureStatus,
			}
		}
	}
	fixtureBodies := fx.ASLQuestionBodies
	d.model.backgroundCommands.aslQuestion = func(_ Model, seq int, planDir, questionID string) tea.Cmd {
		return func() tea.Msg {
			body, ok := fixtureBodies[questionID]
			if !ok {
				return aslQuestionLoadedMsg{seq: seq, err: errors.New("no-open-question: fixture has no such question")}
			}
			if body.PlanDir == "" {
				body.PlanDir = planDir
			}
			if body.QuestionID == "" {
				body.QuestionID = questionID
			}
			return aslQuestionLoadedMsg{seq: seq, body: body}
		}
	}
	fixtureOutcomes := fx.ASLActionOutcomes
	d.model.backgroundCommands.aslAction = func(_ Model, seq int, control bool, rowID string, req aslbridge.ActionRequest) tea.Cmd {
		return func() tea.Msg {
			d.recordASLAction(uitestASLAction{
				RowID:       rowID,
				Method:      req.Method,
				OperationID: req.OperationID,
				QuestionID:  req.QuestionID,
				ActionToken: req.ActionToken,
			})
			msg := aslActionDoneMsg{
				seq: seq, control: control, rowID: rowID, method: req.Method,
				operationID: req.OperationID, status: aslbridge.OperationAccepted,
			}
			// The fixture holds the token the real bridge would demand for this
			// question, so a submission carrying a spent one is refused here
			// rather than silently accepted.
			if body, ok := fixtureBodies[req.QuestionID]; ok && req.ActionToken != body.ActionToken {
				msg.err = errors.New(aslbridge.CodeStaleActionToken + ": fixture refusal")
				return msg
			}
			if outcome, ok := fixtureOutcomes[req.Method]; ok {
				if outcome.ErrorCode != "" {
					msg.err = errors.New(outcome.ErrorCode + ": fixture refusal")
					return msg
				}
				if outcome.Status != "" {
					msg.status = outcome.Status
				}
			}
			return msg
		}
	}
	d.loadSessionsWith(fx.overlay(), rows)
	for _, notice := range fx.Notices {
		d.model.pushToast(notice)
	}
	return d
}
