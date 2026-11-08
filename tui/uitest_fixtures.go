//go:build tuitest

// This file is the fixture catalog: the named, deterministic worlds a scenario
// can be driven against. It holds the session, plan, transcript, ASL run, and
// worktree data each fixture publishes and the stub services that serve it.

package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/flightlog"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/tui/commands"
	"github.com/sglavoie/agentmux/tui/sessionactions"
)

// This file supplies the deterministic data sources the harness driver runs
// against: the fixture type and registry, the dashboard fixtures themselves,
// and a safe config mode that stages HOME and the AMX/XDG data paths in
// throwaway directories so a run can never read or write the operator's real
// config, overrides, plan index, transcript index, or daemon socket. Fixture
// selection is explicit and discoverable so later CLI work can list and address
// them by name.
//
// The builder map below is the whole inventory; the fixtures the redesign added
// live beside it in uitest_fixtures_redesign.go.

// uitestFixture is a deterministic, self-contained data source for a harness
// run: a prepared config, tmux panes, and a default terminal size. Building a driver from a fixture never reads the
// operator's real config and never launches a subprocess.
type uitestFixture struct {
	FlightEvents []flightlog.Event
	Notices      []Toast
	Name         string
	Description  string
	Size         UITestSize
	Config       *config.Config
	PaneGroups   []fixturePaneGroup
	// Portable holds prebuilt tmux sessions with stable IDs for scenario assertions.
	Portable  []session.RunningSession
	Worktrees []actions.WorktreeView
	// States and Watchdog stand in for one daemon state query, keyed by
	// session slot. They carry the state a terminal title cannot express — the busy
	// anchor above all — so a fixture can pin the dashboard's daemon-owned
	// columns. A fixture that leaves them nil reproduces an unreachable daemon,
	// which is the honest default for a fixture with no daemon at all.
	States   map[int]protocol.WindowStateSnapshot
	Watchdog map[int]protocol.WatchdogState
	// ASLRows and ASLStatus stand in for one daemon ASL snapshot. A fixture that
	// leaves ASLStatus zero reports the integration as unavailable, which is the
	// honest default for a dashboard fixture with no agent-session-loop at all.
	ASLRows   []aslbridge.Row
	ASLStatus aslbridge.Status
	// ASLQuestionBodies is keyed by question ID. A question absent from the map
	// is refused exactly as the bridge refuses one that has already closed.
	ASLQuestionBodies map[string]aslbridge.QuestionBody
	// ASLActionOutcomes overrides the default accepted receipt per bridge method,
	// so a scenario can exercise a refusal or a consumed receipt without a bridge.
	ASLActionOutcomes map[string]uitestASLOutcome
	// Families stands in for what config-aware discovery stamped onto each row,
	// keyed by session slot. It exists because the real stamp comes from
	// projectresolve.NewWithGit, which asks Git about a directory on disk: a
	// multi-checkout repository family is not expressible in a fixture without
	// creating real repositories, and their temporary paths would then appear in
	// the directory column and in the strip, which is exactly what a committed
	// scenario cannot assert against.
	//
	// A non-nil map is AUTHORITATIVE for every discovered row, not a patch over
	// what discovery produced: a window it does not name has its family fields
	// cleared. That is what makes "this row belongs to no family" expressible at
	// all, since discovery gives a configured project its own singleton family
	// and there would otherwise be no way to say otherwise. A nil map leaves
	// discovery's own answer alone, which for a fixture pointing at directories
	// that do not exist is a singleton family per configured project.
	Families map[int]uitestFamily
	// Text is the scrollback returned for the previewed pane. A single string
	// is enough because the inspector reads one pane at a time.
	Text string
	// TextFailures makes the first N reads fail instead of answering with Text,
	// standing in for a pane that is gone or a server that is not answering yet.
	// It is a count rather than a flag because
	// the interesting property is recovery: a scenario that only ever failed
	// could not tell "the pane reports the error" from "the pane is stuck on it",
	// and the count is what lets a later read in the same run succeed.
	//
	// A failure is per read, not per target. Reads are counted in the order they
	// reach the fixture, so a fixture wanting the very first paint to fail sets
	// 1 and every read after it — a reopened inspector, a moved cursor, a poll
	// that landed — answers with Text.
	TextFailures int
	// Health stands in for one daemon/hook health refresh. A nil Health leaves
	// the hermetic default in place — nothing checked, which reads as a daemon
	// that is down — so only a fixture with something else to say carries one.
	// It is a pointer for exactly that reason: the zero HealthMsg is itself a
	// meaningful world (daemon down, hooks silent) and would otherwise be
	// indistinguishable from "this fixture has no opinion".
	Health *commands.HealthMsg
}

// uitestFamily is one row's repository-family identity, mirroring the four
// family fields on session.RunningSession. It is spelled out here rather than
// reusing the session type so a fixture states only the facts a family is, with
// no room to set an unrelated session field through the same map.
type uitestFamily struct {
	Key          string
	Label        string
	IsRepository bool
	PrimaryRoot  string
}

// uitestSessionOverlay is everything a fixture injects onto the session list
// discovery produced: the daemon-owned state no terminal title can carry, the
// watchdog block that goes with it, and the repository-family identity a real
// Git probe would have stamped.
//
// The three travel together because they must be replayed together. The
// scenario harness applies them at the initial load AND on every refresh tick,
// so a `tick` action cannot quietly wipe half the fixture's world; a signature
// that took them one at a time is a signature a later injection point can be
// wired into having forgotten.
type uitestSessionOverlay struct {
	States   map[int]protocol.WindowStateSnapshot
	Watchdog map[int]protocol.WatchdogState
	Families map[int]uitestFamily
}

// overlay collects the fixture's injected facts for the driver's two
// application points.
func (f uitestFixture) overlay() uitestSessionOverlay {
	return uitestSessionOverlay{States: f.States, Watchdog: f.Watchdog, Families: f.Families}
}

// apply overlays the fixture's injected facts onto a freshly discovered session
// list, in place. Call it before sorting: the sort reads state, and the family
// fields decide how the Projects projection groups the result.
func (o uitestSessionOverlay) apply(sessions []session.RunningSession) {
	if o.States != nil {
		commands.ApplyDaemonStates(sessions, o.States)
	}
	if o.Families == nil {
		return
	}
	for i := range sessions {
		fam := o.Families[sessions[i].WindowID]
		sessions[i].FamilyKey = fam.Key
		sessions[i].FamilyLabel = fam.Label
		sessions[i].FamilyIsRepository = fam.IsRepository
		sessions[i].FamilyPrimaryRoot = fam.PrimaryRoot
	}
}

// uitestASLOutcome is one scripted result for a submitted mutation. ErrorCode
// wins when both are set, and is the bridge refusal code the UI maps to a fixed
// operator-facing sentence.
type uitestASLOutcome struct {
	Status    string
	ErrorCode string
}

// uitestFixtureBuilders maps a fixture name to a constructor. Each constructor
// returns freshly built values so callers can never mutate shared fixture
// state across runs.
var uitestFixtureBuilders = map[string]func() uitestFixture{
	"action_feedback":    actionFeedbackFixture,
	"basic":              basicFixture,
	"actions":            actionsFixture,
	"asl_question":       aslQuestionFixture,
	"wide_profiles":      wideProfilesFixture,
	"worktrees":          worktreesFixture,
	"worktree_lifecycle": worktreeLifecycleFixture,
	"asl_runs":           aslRunsFixture,
	"run_timers":         runTimersFixture,
	"unseen_stops":       unseenStopsFixture,
	"pinned_sessions":    pinnedSessionsFixture,
	"blocked_reasons":    blockedReasonsFixture,
	"worktree_branches":  worktreeBranchesFixture,
	"project_families":   projectFamiliesFixture,
	"triage_integration": triageIntegrationFixture,
	"triage_vertical":    triageVerticalFixture,
	"family_unread":      familyUnreadFixture,
	"duplicate_names":    duplicateNamesFixture,
	"paused_output":      pausedOutputFixture,
	"health_states":      healthStatesFixture,
	"attention_overlap":  attentionOverlapFixture,
	"no_sessions":        noSessionsFixture,
	"setup_profile":      setupProfileFixture,
	"setup_project":      setupProjectFixture,
	"paused_queue":       pausedQueueFixture,
	"preview_failure":    previewFailureFixture,
	"portable_tmux":      portableTmuxFixture,
	"flight_log":         flightLogFixture,
}

func actionFeedbackFixture() uitestFixture {
	fixture := basicFixture()
	fixture.Name = "action_feedback"
	fixture.Description = "retained action outcomes with exact targets and recovery guidance"
	fixture.Notices = []Toast{
		{Severity: toastError, Text: "Profile setup failed. Open configuration to review the selected profile.", Occurred: time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)},
		{Severity: toastWarning, Unconfirmed: true, Text: "Restart outcome unconfirmed. Inspect the session before repeating the action.", Occurred: time.Date(2026, 10, 16, 9, 1, 0, 0, time.UTC),
			Target: sessionactions.Capture(session.RunningSession{TabID: 1, WindowID: 11, Base: "api", Harness: "claude"})},
	}
	return fixture
}

// uitestFixtureError reports a request for an unknown built-in fixture and
// lists the names that are available.
type uitestFixtureError struct {
	Name  string
	Known []string
}

func (e *uitestFixtureError) Error() string {
	return fmt.Sprintf("uitest: unknown fixture %q (known: %s)", e.Name, strings.Join(e.Known, ", "))
}

// UITestFixtureNames returns the built-in fixture names in sorted order so CLI
// listing and error messages are stable.
func UITestFixtureNames() []string {
	names := make([]string, 0, len(uitestFixtureBuilders))
	for name := range uitestFixtureBuilders {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// uitestFixtureByName builds the named fixture, or returns a structured
// *uitestFixtureError when the name is not recognized.
func uitestFixtureByName(name string) (uitestFixture, error) {
	build, ok := uitestFixtureBuilders[name]
	if !ok {
		return uitestFixture{}, &uitestFixtureError{Name: name, Known: UITestFixtureNames()}
	}
	return build(), nil
}

// fixturePaneGroup groups panes so existing fixture definitions stay compact.
type fixturePaneGroup struct{ Panes []backend.Pane }

func uitestTab(tabID, winID int, title string, active bool, vars map[string]string) backend.Pane {
	return backend.Pane{
		Ref: backend.PaneID(fmt.Sprint(winID)), Group: backend.GroupID(fmt.Sprint(tabID)),
		GroupTitle: title, CWD: vars["amx_directory"], Vars: vars,
		Active: true, GroupActive: active,
	}
}

func uitestPaneGroup(panes ...backend.Pane) fixturePaneGroup {
	return fixturePaneGroup{Panes: panes}
}

func managedVars(profile, harness, project, dir string) map[string]string {
	return map[string]string{
		"amx_profile": profile, "amx_harness": harness,
		"amx_project": project, "amx_directory": dir,
		"amx_launch_id": "uitest-" + project,
	}
}

func (fx uitestFixture) panes() []backend.Pane {
	var panes []backend.Pane
	for _, window := range fx.PaneGroups {
		panes = append(panes, window.Panes...)
	}
	return panes
}

func (fx uitestFixture) runningSessions() []session.RunningSession {
	rows := session.RunningSessionsFromPanes(runtimesession.BackendTmux, fx.panes(), fx.Config)
	for i := range rows {
		rows[i].TabID, _ = strconv.Atoi(rows[i].Group.ID)
		rows[i].WindowID, _ = strconv.Atoi(rows[i].Pane.ID)
		if fx.Config != nil {
			rows[i].Base = strings.TrimPrefix(rows[i].Base, fx.Config.HarnessEmojis[rows[i].Harness])
		}
	}
	return rows
}
