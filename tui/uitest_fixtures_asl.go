package tui

// The agent-session-loop runs fixture. It stands in for one daemon ASL
// snapshot: the projected rows, the capability block, and the question bodies
// the bridge would hand back one at a time. The tokens here are deliberately
// per-question rather than per-row, because that is what a form has to re-read
// between closures.

import (
	"time"

	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// aslRunsFixture covers the four focus outcomes the Runs view has to tell
// apart: an exactly bound run, a run whose workspace and harness match several
// live sessions, a run bound through a second exactly matching session, and a
// run with no live session at all.
func aslRunsFixture() UITestFixture {
	const root = "/tmp/uitest/runs"
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: root + "/api", Profile: "work"},
			{Title: "gateway", Directory: root + "/gateway", Profile: "work"},
			{Title: "dotfiles", Directory: root + "/dotfiles", Profile: "personal"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "🦊api", true, managedVars("work", "claude", "api", root+"/api")),
		uitestTab(2, 21, "🦊gateway-a", false, managedVars("work", "claude", "gateway", root+"/gateway")),
		uitestTab(3, 22, "🦊gateway-b", false, managedVars("work", "claude", "gateway", root+"/gateway")),
		uitestTab(4, 31, "🤖dotfiles", false, managedVars("personal", "codex", "dotfiles", root+"/dotfiles")),
	)}
	started := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	rows := []aslbridge.Row{
		{
			RowID: "row-active", PlanDir: "/tmp/uitest/plans/dotfiles", Label: "dotfiles-sync",
			RunID: "run-active", SessionID: "sess-dotfiles", PrimaryState: "running",
			UrgencyRank: 60, UrgencyGroup: "in-progress", ActivityLabel: "working",
			Phase: "implement", Harness: "codex", WorkspaceRoot: root + "/dotfiles",
			Window: &aslbridge.Window{KittyWindowID: 31}, RunStartedAt: started,
			LastActivityAt: time.Now().Add(-3 * time.Minute).UTC().Format(time.RFC3339),
		},
		{
			RowID: "row-reply", PlanDir: "/tmp/uitest/plans/api", Label: "api-refactor",
			RunID: "run-reply", SessionID: "sess-api", PrimaryState: "paused-for-reply",
			UrgencyRank: 10, UrgencyGroup: "needs-action", RecommendedAction: "reply",
			ActivityLabel: "waiting for reply", Phase: "review", Harness: "claude",
			WorkspaceRoot: root + "/api", Window: &aslbridge.Window{KittyWindowID: 11},
			RunStartedAt: started, LastActivityAt: time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339),
			PauseReason: "awaiting an answer to the migration question",
			Question: &aslbridge.Question{
				ContextKind: "live-wait", QuestionID: "q-api-1", UnansweredCount: 2,
				QuestionIDs: []string{"q-api-1", "q-api-2"}, SessionID: "sess-api",
				AskedAt:   time.Now().Add(-1 * time.Minute).UTC().Format(time.RFC3339),
				TurnIndex: 4, WaitingStatus: "waiting",
			},
			Actions: []string{
				aslbridge.MethodQuestionReply, aslbridge.MethodQuestionResolve,
				aslbridge.MethodRunStopAfterCurrent, aslbridge.MethodRunAbort,
			},
			ActionToken: "token-reply",
		},
		{
			RowID: "row-paused", PlanDir: "/tmp/uitest/plans/paused", Label: "paused-migration",
			RunID: "run-paused", SessionID: "sess-paused", PrimaryState: "paused",
			UrgencyRank: 15, UrgencyGroup: "needs-action", RecommendedAction: "resume",
			ActivityLabel: "paused", Phase: "plan", Harness: "claude",
			WorkspaceRoot: root + "/paused", RunStartedAt: started,
			LastActivityAt: time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339),
			PauseReason:    "paused waiting for operator direction",
			Question: &aslbridge.Question{
				ContextKind: "pause", QuestionID: "q-paused", UnansweredCount: 1,
				QuestionIDs: []string{"q-paused"}, SessionID: "sess-paused",
			},
			Actions:     []string{aslbridge.MethodRunResume},
			ActionToken: "token-paused",
		},
		{
			RowID: "row-orphan", PlanDir: "/tmp/uitest/plans/orphan", Label: "orphan-cleanup",
			RunID: "run-orphan", SessionID: "sess-orphan", PrimaryState: "failed",
			UrgencyRank: 20, UrgencyGroup: "needs-action", RecommendedAction: "inspect",
			ActivityLabel: "failed", Phase: "verify", Harness: "claude",
			WorkspaceRoot: root + "/orphan", RunStartedAt: started,
			LastActivityAt: time.Now().Add(-20 * time.Minute).UTC().Format(time.RFC3339),
			PauseReason:    "verification command exited non-zero",
		},
		{
			RowID: "row-verify", PlanDir: "/tmp/uitest/plans/gateway", Label: "gateway-upgrade",
			RunID: "run-verify", SessionID: "sess-gateway", PrimaryState: "verifying",
			UrgencyRank: 50, UrgencyGroup: "in-progress", ActivityLabel: "verifying",
			Phase: "verify", Harness: "claude", WorkspaceRoot: root + "/gateway",
			RunStartedAt: started, LastActivityAt: time.Now().Add(-8 * time.Minute).UTC().Format(time.RFC3339),
			DisabledReasons: map[string]string{
				aslbridge.MethodQuestionReply: "this run has no open question",
				aslbridge.MethodRunAbort:      "this run has no in-flight turn",
			},
			Actions:     []string{aslbridge.MethodRunStopAfterCurrent},
			ActionToken: "token-verify",
		},
	}
	return UITestFixture{
		Name:        "asl_runs",
		Description: "agent-session-loop runs spanning exact, ambiguous, and unmatched focus targets",
		Size:        UITestSize{Width: 132, Height: 34},
		Config:      cfg,
		Windows:     windows,
		ASLRows:     rows,
		ASLStatus: aslbridge.Status{
			Mode: string(aslbridge.ModeAuto), Available: true, MutationsEnabled: true,
			Protocol: aslbridge.ProtocolVersion, Version: "uitest",
		},
		ASLQuestionBodies: map[string]aslbridge.QuestionBody{
			"q-api-1": {
				PlanDir: "/tmp/uitest/plans/api", RowID: "row-reply", RunID: "run-reply",
				QuestionID: "q-api-1", ContextKind: "live-wait",
				Body:      "Should the migration run before or after the cutover?",
				BodyBytes: 52, ActionToken: "token-reply",
			},
			"q-api-2": {
				PlanDir: "/tmp/uitest/plans/api", RowID: "row-reply", RunID: "run-reply",
				QuestionID: "q-api-2", ContextKind: "live-wait",
				Body: "Which staging database should the rehearsal target?",
				// Rotated relative to the row: closing q-api-1 changes the run's
				// unanswered set, so ASL mints a different token for what is
				// left. Only a form that re-reads it can submit the second one.
				BodyBytes: 51, ActionToken: "token-reply-2",
			},
			"q-paused": {
				PlanDir: "/tmp/uitest/plans/paused", RowID: "row-paused", RunID: "run-paused",
				QuestionID: "q-paused", ContextKind: "pause",
				Body:      "This run is paused. Reply to resume it.",
				BodyBytes: 39, ActionToken: "token-paused",
			},
		},
	}
}
