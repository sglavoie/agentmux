package tui

// This file binds a built-in fixture to a driver: the fixture's windows, plans,
// worktrees, and agent-session-loop rows become the answers the offline command
// seams give back. The seams themselves are declared in
// uitest_driver_hermetic.go and the fixtures in uitest_fixtures.go.

import (
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/worktree"
)

// NewUITestFixtureDriver builds a driver from a built-in fixture: it constructs
// the model with the fixture's config and a no-op recording Kitty client (which
// reports the fixture's windows), applies size, and loads the fixture's running
// sessions synchronously so the list is populated without the refresh ticker
// (whose tea.Tick commands the harness skips before launch). Each zero dimension falls back
// independently to the fixture's own default for that dimension, so a caller can
// override width alone (or height alone) without zeroing the other. The recording
// client is returned so callers can assert on the Kitty calls the model attempted.
func NewUITestFixtureDriver(fx UITestFixture, size UITestSize) (*UITestDriver, *UITestKittyClient) {
	if size.Width == 0 {
		size.Width = fx.Size.Width
	}
	if size.Height == 0 {
		size.Height = fx.Size.Height
	}
	client := NewUITestKittyClient(fx.Windows)
	d := NewUITestDriver(fx.Config, client, size)
	d.model.backgroundCommands.initialFetch = hermeticInitialFetch(fx.States, fx.Watchdog)
	fixtureWorktrees := append([]actions.WorktreeView(nil), fx.Worktrees...)
	d.model.backgroundCommands.worktrees = func(_ Model, seq int) tea.Cmd {
		return func() tea.Msg {
			return worktreesLoadedMsg{seq: seq, entries: append([]actions.WorktreeView(nil), fixtureWorktrees...)}
		}
	}
	d.model.backgroundCommands.worktreeAction = func(_ Model, id actions.ID, request actions.Request) tea.Cmd {
		return fixtureWorktreeActionCmd(client, &fixtureWorktrees, id, request)
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
	d.model.backgroundCommands.aslWorkbench = func(_ Model, rowID, planDir, focus string) tea.Cmd {
		return func() tea.Msg {
			d.recordWorkbenchOpen(UITestWorkbenchOpen{RowID: rowID, Focus: focus})
			if planDir == "" {
				return aslWorkbenchDoneMsg{rowID: rowID, focus: focus, err: errASLWorkbenchOpen}
			}
			return aslWorkbenchDoneMsg{rowID: rowID, focus: focus}
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
			d.recordASLAction(UITestASLAction{
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
	d.loadSessionsFrom(client, fx.Config, fx.States, fx.Watchdog)
	return d, client
}

func fixtureWorktreeActionCmd(client *UITestKittyClient, entries *[]actions.WorktreeView, id actions.ID, request actions.Request) tea.Cmd {
	return func() tea.Msg {
		entry := request.Worktree
		if entry == nil {
			return worktreeActionDoneMsg{action: id, err: &actions.Error{Code: "not_found", Action: id, Message: "fixture worktree target is missing"}}
		}
		done := func(result actions.Result, err error) tea.Msg {
			result.Action = id
			return worktreeActionDoneMsg{action: id, result: result, err: err}
		}
		switch id {
		case actions.WorktreeReopen:
			switch entry.ID {
			case "wt-live":
				_ = client.FocusTab(kitty.MatchTabByID(71))
				return done(actions.Result{Changed: true, Message: "focused live worktree session"}, nil)
			case "wt-multi":
				if candidate := request.Parameters["session"]; candidate != "" {
					_ = client.FocusTab("id:" + candidate)
					return done(actions.Result{Changed: true, Message: "focused selected live worktree session"}, nil)
				}
				return done(actions.Result{}, &actions.Error{Code: "ambiguous_live_sessions", Action: id, Message: "multiple live sessions are inside this worktree", Candidates: []actions.Candidate{{ID: "81", DisplayName: "multi-project (work)"}, {ID: "82", DisplayName: "multi-project (personal)"}}})
			case "wt-resume":
				_ = client.LaunchTab(kitty.LaunchOpts{Title: "resume-project"})
				return done(actions.Result{Changed: true, Message: "resumed newest worktree conversation", Data: map[string]any{"continuation": "resumed"}}, nil)
			case "wt-fresh":
				_ = client.LaunchTab(kitty.LaunchOpts{Title: "fresh-project"})
				return done(actions.Result{Changed: true, Message: "launched fresh worktree session", Data: map[string]any{"continuation": "fresh"}}, nil)
			case "wt-profile":
				if request.Parameters["profile"] == "" {
					return done(actions.Result{}, &actions.Error{Code: "profile_required", Action: id, Message: "the recorded worktree profile is no longer configured", MissingParameters: []actions.ParameterSchema{{Name: "profile", Type: "string", Required: true}}})
				}
				_ = client.LaunchTab(kitty.LaunchOpts{Title: "profile-project"})
				return done(actions.Result{Changed: true, Message: "launched fresh worktree session with replacement profile"}, nil)
			}
			return done(actions.Result{Changed: true, Message: "launched fresh worktree session"}, nil)
		case actions.WorktreeRemove:
			mutation := worktree.MutationResult{Entry: *entry, CheckoutRemoved: true, MetadataForgotten: true}
			if request.Parameters["delete_branch"] == "true" {
				mutation.BranchDeleted = true
			}
			if entry.ID == "wt-partial" {
				mutation.MetadataForgotten = false
				for index := range *entries {
					if (*entries)[index].ID == entry.ID {
						(*entries)[index].Exists = false
						(*entries)[index].Classification = worktree.ClassificationMissing
						(*entries)[index].Actionable = false
					}
				}
				result := actions.Result{Changed: true, Message: "removed managed worktree partially", Data: map[string]any{"mutation": mutation}}
				return done(result, &actions.PartialResultError{ActionError: &actions.Error{Code: "metadata_invalid", Action: id, Message: "metadata removal failed"}})
			}
			removeFixtureWorktree(entries, entry.ID)
			return done(actions.Result{Changed: true, Message: "removed managed worktree", Data: map[string]any{"mutation": mutation}}, nil)
		case actions.WorktreeForget:
			mutation := worktree.MutationResult{Entry: *entry, MetadataForgotten: true}
			removeFixtureWorktree(entries, entry.ID)
			return done(actions.Result{Changed: true, Message: "forgot worktree metadata", Data: map[string]any{"mutation": mutation}}, nil)
		default:
			return done(actions.Result{Changed: true, Message: "fixture worktree action completed"}, nil)
		}
	}
}

func removeFixtureWorktree(entries *[]actions.WorktreeView, id string) {
	kept := (*entries)[:0]
	for _, entry := range *entries {
		if entry.ID != id {
			kept = append(kept, entry)
		}
	}
	*entries = kept
}
