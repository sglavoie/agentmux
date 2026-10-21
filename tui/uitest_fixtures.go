//go:build tuitest

// This file is the fixture catalog: the named, deterministic worlds a scenario
// can be driven against. It holds the session, plan, transcript, ASL run, and
// worktree data each fixture publishes, the stub services that serve it, and
// the recording Kitty client that captures what the driver asked Kitty to do.

package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
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
// live beside it in uitest_fixtures_redesign.go. The recording Kitty client they
// are all served through is at the bottom of this file.

// uitestFixture is a deterministic, self-contained data source for a harness
// run: a prepared config plus the Kitty windows a no-op client should report,
// and a default terminal size. Building a driver from a fixture never reads the
// operator's real config and never launches a subprocess.
type uitestFixture struct {
	Notices     []Toast
	Name        string
	Description string
	Size        UITestSize
	Config      *config.Config
	Windows     []kitty.OSWindow
	// Portable is the sessions a non-Kitty backend hosts, already carrying the
	// process-local IDs discovery would give them. NoKitty additionally runs
	// the dashboard with no Kitty client at all, the way it runs in any other
	// terminal: such a fixture has no Windows, and every Kitty-only command
	// has to say why it is unavailable instead of reaching for the client.
	Portable  []session.RunningSession
	NoKitty   bool
	Worktrees []actions.WorktreeView
	// States and Watchdog stand in for one daemon state query, keyed by Kitty
	// window ID. They carry the state a tab title cannot express — the busy
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
	// keyed by Kitty window ID. It exists because the real stamp comes from
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
	// Text is the scrollback every GetText read answers with, standing in for
	// one `kitten @ get-text` against the previewed window. It is one string
	// rather than a per-window map because the pane reads exactly one window at
	// a time, and a fixture that varied by window would be asserting on which
	// window was asked — which the recording client already captures.
	Text string
	// TextFailures makes the first N reads fail instead of answering with Text,
	// standing in for a `kitten @ get-text` against a window that is gone or a
	// socket that is not answering yet. It is a count rather than a flag because
	// the interesting property is recovery: a scenario that only ever failed
	// could not tell "the pane reports the error" from "the pane is stuck on it",
	// and the count is what lets a later read in the same run succeed.
	//
	// A failure is per read, not per target. Reads are counted in the order they
	// reach the client, so a fixture wanting the very first paint to fail sets
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
// discovery produced: the daemon-owned state no Kitty title can carry, the
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

// uitestTab builds a single managed tab whose one window carries amx user vars,
// mirroring how a launched session appears in `kitten @ ls`.
func uitestTab(tabID, winID int, title string, active bool, vars kitty.UserVars) kitty.Tab {
	return kitty.Tab{
		ID:       tabID,
		IsActive: active,
		Title:    title,
		Windows: []kitty.Window{{
			ID:       winID,
			IsActive: true,
			CWD:      vars.CWD,
			UserVars: vars,
		}},
	}
}

// uitestOSWindow wraps tabs in a single focused OS window.
func uitestOSWindow(tabs ...kitty.Tab) kitty.OSWindow {
	return kitty.OSWindow{ID: 1, IsActive: true, IsFocused: true, Tabs: tabs}
}

// managedVars builds the user-var block a launched session carries, populating
// both the CWD and amx_directory so directory-backed actions resolve.
func managedVars(profile, harness, project, dir string) kitty.UserVars {
	return kitty.UserVars{
		Profile:      profile,
		Harness:      harness,
		Project:      project,
		AMXDirectory: dir,
		CWD:          dir,
		LaunchID:     "uitest-" + project,
	}
}

// basicFixture is a realistic multi-profile dashboard: a couple of Claude
// projects under "work" and a Codex project under "personal".
func basicFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "~/projects/api", Profile: "work"},
			{Title: "backend", Directory: "~/projects/backend", Profile: "work"},
			{Title: "dotfiles", Directory: "~/dotfiles", Profile: "personal"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "🦊api", true, managedVars("work", "claude", "api", "~/projects/api")),
		uitestTab(2, 21, "🦊backend", false, managedVars("work", "claude", "backend", "~/projects/backend")),
		uitestTab(3, 31, "🤖dotfiles", false, managedVars("personal", "codex", "dotfiles", "~/dotfiles")),
	)}
	return uitestFixture{
		Name:        "basic",
		Description: "two Claude work projects plus a Codex personal project",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		Windows:     windows,
	}
}

// actionsFixture stresses the per-row actions overlay: a running session with a
// very long project title so the "Actions for <title>" header and box width are
// exercised.
func actionsFixture() uitestFixture {
	const longTitle = "monorepo-services-authentication-gateway"
	dir := "~/projects/" + longTitle
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		HarnessColors: map[string]string{"claude": "blue"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
		},
		Sessions: []config.Session{
			{Title: longTitle, Directory: dir, Profile: "work"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "🦊"+longTitle, true, managedVars("work", "claude", longTitle, dir)),
	)}
	return uitestFixture{
		Name:        "actions",
		Description: "single long-titled session for exercising the actions overlay width",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		Windows:     windows,
	}
}

// aslQuestionFixture is the basic dashboard with one looping agent-session-loop
// run whose supervisor has handed a mailbox question to the user (❓🔁) and a
// sibling run still working (⚡🔁). It exists so scenarios can exercise the
// human-actionable presentation — attention grouping, the "awaiting response"
// gloss, and the `n`/`N` jump that replaced the retired `]q` chord — without a
// daemon painting the state.
func aslQuestionFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "~/projects/api", Profile: "work"},
			{Title: "backend", Directory: "~/projects/backend", Profile: "work"},
			{Title: "dotfiles", Directory: "~/dotfiles", Profile: "personal"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "❓🔁🦊api", true, managedVars("work", "claude", "api", "~/projects/api")),
		uitestTab(2, 21, "⚡🔁🦊backend", false, managedVars("work", "claude", "backend", "~/projects/backend")),
		uitestTab(3, 31, "🤖dotfiles", false, managedVars("personal", "codex", "dotfiles", "~/dotfiles")),
	)}
	return uitestFixture{
		Name:        "asl_question",
		Description: "an ASL supervisor awaiting a human mailbox reply alongside an active looping run",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		Windows:     windows,
	}
}

// wideProfilesFixture stresses the profile (config) picker: several profiles
// with long names and long home directories so the picker box must grow to fit
// its widest row.
func wideProfilesFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖", "copilot": "🧑"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green", "copilot": "purple"},
		Accounts: []config.Account{
			{Name: "claude-production-primary", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/claude-production-primary-workspace", Color: "blue"},
			{Name: "codex-experimental-sandbox", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/codex-experimental-sandbox-workspace", Color: "green"},
			{Name: "copilot-enterprise-review-board", Harness: "copilot", HomeDir: "~/.config/agent-mux/homes/copilot-enterprise-review-board-workspace", Color: "purple"},
		},
		Sessions: []config.Session{
			{Title: "platform-services-monorepo", Directory: "~/projects/platform-services-monorepo", Profile: "claude-production-primary"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "🦊platform-services-monorepo", true,
			managedVars("claude-production-primary", "claude", "platform-services-monorepo", "~/projects/platform-services-monorepo")),
	)}
	return uitestFixture{
		Name:        "wide_profiles",
		Description: "long profile names and home dirs for exercising the profile picker width",
		Size:        UITestSize{Width: 140, Height: 36},
		Config:      cfg,
		Windows:     windows,
	}
}

// portableTmuxFixture is the dashboard as it runs in a stock terminal: no Kitty
// client, and three sessions hosted by the private tmux server. One needs the
// operator, one is working, one is at rest, so the queue's buckets, the
// selection, and the inspector are all exercised against rows no Kitty window
// stands behind.
func portableTmuxFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.claude-work", Color: "blue"},
			{Name: "codex-work", Harness: "codex", HomeDir: "~/.codex-work", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "~/projects/api", Profile: "work"},
			{Title: "web", Directory: "~/projects/web", Profile: "work"},
			{Title: "docs", Directory: "~/projects/docs", Profile: "codex-work"},
		},
	}
	row := func(n int, project, profile, harness string) session.RunningSession {
		id := commands.PortableIDBase + n
		return session.RunningSession{
			TabID: id, WindowID: id, Title: project, Base: project, Project: project,
			Profile: profile, Account: profile, Harness: harness, Directory: "~/projects/" + project,
			Backend: runtimesession.BackendTmux,
			Group:   backend.GroupID(fmt.Sprintf("$%d", n)), Pane: backend.PaneID(fmt.Sprintf("%%%d", n)),
			RuntimeSessionID: runtimesession.ID(fmt.Sprintf("rs-%032d", n)),
		}
	}
	busySince := uitestFrozenNow.Add(-95 * time.Second).Unix()
	return uitestFixture{
		Name:        "portable_tmux",
		Description: "three tmux-hosted sessions and no Kitty client, as in a stock terminal",
		Size:        UITestSize{Width: 140, Height: 36},
		Config:      cfg,
		NoKitty:     true,
		Portable:    []session.RunningSession{row(0, "api", "work", "claude"), row(1, "web", "work", "claude"), row(2, "docs", "codex-work", "codex")},
		States: map[int]protocol.WindowStateSnapshot{
			commands.PortableIDBase:     {IconState: "key"},
			commands.PortableIDBase + 1: {IconState: "bolt", BusySinceAt: busySince},
		},
		Text: "tmux pane output line 1\ntmux pane output line 2\n",
	}
}

// runTimersFixture covers the per-row run timer across the bands it formats
// differently: a sub-minute burst, a multi-minute stretch, an hours-long
// stretch, a row deep enough into its watchdog threshold to carry the ⏳
// warning, and an idle row with no anchor at all. Every timestamp is a fixed
// offset from the driver's frozen clock, so each cell renders exact text. The
// idle row's own watchdog entry is inside the warning band on purpose: it must
// still render a bare em dash, because the warning belongs to rows that are
// actually running.
func runTimersFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "burst", Directory: "~/projects/burst", Profile: "work"},
			{Title: "review", Directory: "~/projects/review", Profile: "work"},
			{Title: "indexer", Directory: "~/projects/indexer", Profile: "work"},
			{Title: "stale", Directory: "~/projects/stale", Profile: "work"},
			{Title: "idle", Directory: "~/projects/idle", Profile: "personal"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "⚡🦊burst", true, managedVars("work", "claude", "burst", "~/projects/burst")),
		uitestTab(2, 21, "🤖🦊review", false, managedVars("work", "claude", "review", "~/projects/review")),
		uitestTab(3, 31, "🌀🦊indexer", false, managedVars("work", "claude", "indexer", "~/projects/indexer")),
		uitestTab(4, 41, "⚡🦊stale", false, managedVars("work", "claude", "stale", "~/projects/stale")),
		uitestTab(5, 51, "🛑🤖idle", false, managedVars("personal", "codex", "idle", "~/projects/idle")),
	)}
	// Anchors chosen so each row lands in a different formatting band:
	// 0:07, 3:07, 12h07, 9:41 (warning), and the em dash.
	busySince := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).Unix() }
	lastEvent := busySince
	const thresholdSec = 600
	return uitestFixture{
		Name:        "run_timers",
		Description: "busy stretches spanning the sub-minute, multi-minute, hours-long, warning, and idle timer bands",
		Size:        UITestSize{Width: 120, Height: 32},
		Config:      cfg,
		Windows:     windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "bolt", BusySinceAt: busySince(7 * time.Second)},
			21: {IconState: "subagent", BusySinceAt: busySince(3*time.Minute + 7*time.Second)},
			31: {IconState: "compact", BusySinceAt: busySince(12*time.Hour + 7*time.Minute)},
			41: {IconState: "bolt", BusySinceAt: busySince(9*time.Minute + 41*time.Second)},
			51: {IconState: "stop"},
		},
		Watchdog: map[int]protocol.WatchdogState{
			11: {LastAction: "active", LastEventAt: lastEvent(7 * time.Second), ThresholdSec: thresholdSec},
			21: {LastAction: "subagent", LastEventAt: lastEvent(3*time.Minute + 7*time.Second), ThresholdSec: thresholdSec},
			31: {LastAction: "compact", LastEventAt: lastEvent(45 * time.Second), ThresholdSec: thresholdSec},
			41: {LastAction: "active", LastEventAt: lastEvent(9*time.Minute + 41*time.Second), ThresholdSec: thresholdSec},
			51: {LastAction: "active", LastEventAt: lastEvent(22 * time.Minute), ThresholdSec: thresholdSec},
		},
	}
}

// unseenStopsFixture is two finished sessions that a tab title cannot tell
// apart. Both carry 🛑; only the daemon knows the user has read one of them and
// not the other. It exists so scenarios can pin the demotion that makes Needs
// Attention drain: the unread stop leads the list, the read one falls to Idle,
// and a working row holds Active in between so all three group labels render.
// The state names alone are ambiguous here on purpose — a fixture where the
// grouping could be reproduced from the titles would prove nothing.
func unseenStopsFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "unread", Directory: "~/projects/unread", Profile: "work"},
			{Title: "read", Directory: "~/projects/read", Profile: "work"},
			{Title: "working", Directory: "~/projects/working", Profile: "personal"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "🛑🦊unread", false, managedVars("work", "claude", "unread", "~/projects/unread")),
		uitestTab(2, 21, "🛑🦊read", false, managedVars("work", "claude", "read", "~/projects/read")),
		uitestTab(3, 31, "⚡🤖working", true, managedVars("personal", "codex", "working", "~/projects/working")),
	)}
	return uitestFixture{
		Name:        "unseen_stops",
		Description: "two identical 🛑 rows the daemon separates by seenness, plus one working row",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		Windows:     windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "stop", Unseen: true},
			21: {IconState: "stop"},
			31: {IconState: "bolt", BusySinceAt: uitestFrozenNow.Add(-42 * time.Second).Unix()},
		},
	}
}

// blockedReasonsFixture is four Needs you rows that a tab title cannot tell
// apart — two 🔑 and two ❓ — where only the daemon knows what each one is
// actually waiting for. It covers the three cases the Status column has to get
// right at once:
//
//   - a Codex permission prompt and a Claude permission prompt, whose kind
//     strings are deliberately different (`command_approval` vs `permission`,
//     because the two harnesses share one untyped field) and which must
//     nonetheless both read `Needs permission`: the status names what is being
//     asked of the user, not which dialog asked it;
//   - a Codex request-for-input prompt, which reads `Needs input` rather than
//     borrowing the permission sentence;
//   - a blocked row with NO reason at all, which is the ordinary steady state
//     for a hook-raised icon no screen probe has labelled yet. It falls back to
//     `Awaiting reply` rather than going blank, because a row in Needs you with
//     an empty status would be a row that cannot say why it is there.
//
// The `working` row holds Running so the shared-column rule is visible too: it
// is not in the attention group, has nothing to explain, and still lands its
// status and time in the same cells as the rows above it.
//
// The kinds are written as bare strings rather than screenstate constants
// because that is exactly what a fixture stands in for: one daemon state query,
// whose Reason field is an untyped wire string. The dashboard's own map from
// those strings to tags is pinned separately, in package tui/dashboard.
func blockedReasonsFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "approve", Directory: "~/projects/approve", Profile: "personal"},
			{Title: "grant", Directory: "~/projects/grant", Profile: "work"},
			{Title: "choose", Directory: "~/projects/choose", Profile: "personal"},
			{Title: "silent", Directory: "~/projects/silent", Profile: "work"},
			{Title: "working", Directory: "~/projects/working", Profile: "work"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "🔑🤖approve", false, managedVars("personal", "codex", "approve", "~/projects/approve")),
		uitestTab(2, 21, "🔑🦊grant", false, managedVars("work", "claude", "grant", "~/projects/grant")),
		uitestTab(3, 31, "❓🤖choose", false, managedVars("personal", "codex", "choose", "~/projects/choose")),
		uitestTab(4, 41, "❓🦊silent", false, managedVars("work", "claude", "silent", "~/projects/silent")),
		uitestTab(5, 51, "⚡🦊working", true, managedVars("work", "claude", "working", "~/projects/working")),
	)}
	return uitestFixture{
		Name:        "blocked_reasons",
		Description: "four identically-iconed blocked rows the daemon explains with different prompt kinds, one of them unlabelled",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		Windows:     windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "key", Reason: "command_approval"},
			21: {IconState: "key", Reason: "permission"},
			31: {IconState: "question", Reason: "request_user_input"},
			41: {IconState: "question"},
			51: {IconState: "bolt"},
		},
	}
}

// worktreeBranchesFixture is the case managed worktrees make ordinary: three
// rows sitting in ONE repository and differing only by checkout, plus a fourth
// that is not a repository at all. It is the fixture for the branch because it
// is the situation a branch exists to resolve.
//
// Two of the rows are worktrees of the same project. Their directories share
// `~/projects/worktrees/api-` and diverge in the tail, which is exactly what the
// strip's left truncation surrenders first at a narrow width — so without a
// branch the two are indistinguishable at the sizes an operator actually runs
// the dashboard at.
//
// Since the queue redesign the branch is the INSPECTOR's and not the row's: it
// names the selected checkout underneath the queue rather than repeating itself
// down a column, and the rows themselves are disambiguated by name instead. The
// cases the fixture carries survive that move:
//
//   - a dirty checkout (`main*`), which is the whole of the dirty rendering: a
//     trailing marker on the branch rather than a color or a second field, so it
//     survives a monochrome terminal and the harness's own stripAnsi;
//   - a NON-REPOSITORY row, whose strip is the bare directory rather than a
//     leading separator with nothing in front of it;
//   - an activity timer on every row, so a branch and a timer are seen to be
//     different facts about the same selection.
//
// The branch strings are written as bare values for the reason the blocked
// reason kinds are: a fixture stands in for one daemon state query, whose Branch
// and Dirty fields are what internal/gitstate last read off disk. Whether a
// non-repository, a disabled probe, a timeout, or a detached HEAD produced the
// empty branch is deliberately not representable here, because it is not
// representable on the wire either — gitstate collapses all four to the same
// nothing.
func worktreeBranchesFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "~/projects/api", Profile: "work"},
			{Title: "api-feat-x", Directory: "~/projects/worktrees/api-feat-x", Profile: "work"},
			{Title: "api-hotfix", Directory: "~/projects/worktrees/api-hotfix", Profile: "work"},
			{Title: "notes", Directory: "~/notes", Profile: "personal"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "⚡🦊api", true, managedVars("work", "claude", "api", "~/projects/api")),
		uitestTab(2, 21, "⚡🦊api-feat-x", false, managedVars("work", "claude", "api-feat-x", "~/projects/worktrees/api-feat-x")),
		uitestTab(3, 31, "⚡🦊api-hotfix", false, managedVars("work", "claude", "api-hotfix", "~/projects/worktrees/api-hotfix")),
		uitestTab(4, 41, "⚡🤖notes", false, managedVars("personal", "codex", "notes", "~/notes")),
	)}
	busySince := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).Unix() }
	return uitestFixture{
		Name:        "worktree_branches",
		Description: "three rows in one repository — a dirty main checkout and two managed worktrees — plus a directory that is not a repository at all",
		Size:        UITestSize{Width: 100, Height: 32},
		Config:      cfg,
		Windows:     windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "bolt", BusySinceAt: busySince(42 * time.Second), Branch: "main", Dirty: true},
			21: {IconState: "bolt", BusySinceAt: busySince(3*time.Minute + 7*time.Second), Branch: "amx/feat-x"},
			31: {IconState: "bolt", BusySinceAt: busySince(12 * time.Second), Branch: "amx/hotfix"},
			41: {IconState: "bolt", BusySinceAt: busySince(1*time.Minute + 5*time.Second)},
		},
	}
}

// projectFamiliesFixture is the Projects view's world: seven rows arranged into
// two multi-checkout repository families, one single-checkout family, and one
// row belonging to no family at all. It is the fixture the tree scenarios need,
// and it is the reason uitestFixture.Families exists — the real stamp comes from
// a Git probe against a directory on disk, so without an injection seam a
// multi-checkout family could only be built out of temporary repositories whose
// paths would then appear in the very columns the scenarios assert on.
//
// The four grouping cases it has to hold at once:
//
//   - `api`, three checkouts of one repository whose members sit in three
//     DIFFERENT urgency buckets. That is what makes the summary a claim rather
//     than a label: it speaks with its most urgent member's voice (🔑), and
//     folding it must not change the status line's tally, because the tally
//     counts sessions and a summary is a line;
//   - `docs`, two checkouts under a different profile and harness, so a family
//     is visibly not the same thing as a profile;
//   - `notes`, a family with exactly ONE member, which earns no summary. A
//     summary over a lone session would be decoration;
//   - `scratch`, a row with no family at all, which renders as a top-level leaf
//     indistinguishable from the singleton above it. The two arrive at the same
//     rendering by different routes, and pinning both is what stops a later
//     change from special-casing one of them.
//
// The bucket spread is deliberate and load-bearing for the count scenarios:
// two attention (a 🔑 prompt and an unread 🛑), two active, three idle. Both
// attention rows are in different families, so `!attention` leaves a family
// filtered down to one visible member — the case where a summary correctly
// disappears rather than claiming a count nobody can reach.
func projectFamiliesFixture() uitestFixture {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha", RefreshInterval: 2,
			// Spelled out because a fixture builds its Config literally and never
			// runs config.Load's defaulting pass: a zero Preview block has ratio
			// 0, which every split calculation reads as "no preview fits". These
			// are exactly the values Load would have filled in, so the summary's
			// project card is reachable from a scenario.
			Preview: config.PreviewConfig{
				Direction: "horizontal", Ratio: 0.5, RefreshMs: 500, MinCols: 100, MinRows: 30,
			},
		},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "~/projects/api", Profile: "work"},
			{Title: "api-feat-x", Directory: "~/projects/worktrees/api-feat-x", Profile: "work"},
			{Title: "api-hotfix", Directory: "~/projects/worktrees/api-hotfix", Profile: "work"},
			{Title: "docs", Directory: "~/projects/docs", Profile: "personal"},
			{Title: "docs-review", Directory: "~/projects/worktrees/docs-review", Profile: "personal"},
			{Title: "notes", Directory: "~/notes", Profile: "personal"},
			{Title: "scratch", Directory: "~/scratch", Profile: "work"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "🔑🦊api", true, managedVars("work", "claude", "api", "~/projects/api")),
		uitestTab(2, 21, "⚡🦊api-feat-x", false, managedVars("work", "claude", "api-feat-x", "~/projects/worktrees/api-feat-x")),
		uitestTab(3, 31, "🛑🦊api-hotfix", false, managedVars("work", "claude", "api-hotfix", "~/projects/worktrees/api-hotfix")),
		uitestTab(4, 41, "⚡🤖docs", false, managedVars("personal", "codex", "docs", "~/projects/docs")),
		uitestTab(5, 51, "🛑🤖docs-review", false, managedVars("personal", "codex", "docs-review", "~/projects/worktrees/docs-review")),
		uitestTab(6, 61, "🛑🤖notes", false, managedVars("personal", "codex", "notes", "~/notes")),
		uitestTab(7, 71, "🛑🦊scratch", false, managedVars("work", "claude", "scratch", "~/scratch")),
	)}
	busySince := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).Unix() }
	// The keys are opaque to everything downstream — the dashboard groups by
	// them and must never render them — so they are written as short readable
	// strings rather than as the canonical common-dir paths discovery mints.
	// A scenario that depended on their shape would be pinning the resolver,
	// which is tested against real temporary repositories in internal/session.
	return uitestFixture{
		Name:        "project_families",
		Description: "two multi-checkout repository families spanning three urgency buckets, plus a single-checkout family and an unfamilied row",
		Size:        UITestSize{Width: 120, Height: 32},
		Config:      cfg,
		Windows:     windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "key", Reason: "permission", Branch: "main", Dirty: true},
			21: {IconState: "bolt", BusySinceAt: busySince(3*time.Minute + 7*time.Second), Branch: "amx/feat-x"},
			31: {IconState: "stop", Branch: "amx/hotfix"},
			41: {IconState: "bolt", BusySinceAt: busySince(42 * time.Second), Branch: "main"},
			51: {IconState: "stop", Branch: "amx/review"},
			61: {IconState: "stop", Unseen: true},
			71: {IconState: "stop"},
		},
		Families: map[int]uitestFamily{
			11: {Key: "git:api", Label: "api", IsRepository: true, PrimaryRoot: "~/projects/api"},
			21: {Key: "git:api", Label: "api", IsRepository: true, PrimaryRoot: "~/projects/api"},
			31: {Key: "git:api", Label: "api", IsRepository: true, PrimaryRoot: "~/projects/api"},
			41: {Key: "git:docs", Label: "docs", IsRepository: true, PrimaryRoot: "~/projects/docs"},
			51: {Key: "git:docs", Label: "docs", IsRepository: true, PrimaryRoot: "~/projects/docs"},
			61: {Key: "project:notes", Label: "notes", PrimaryRoot: "~/notes"},
			// 71 (scratch) is deliberately absent: an authoritative Families map
			// clears what it does not name, which is the only way to say that a
			// discovered row belongs to no family.
		},
	}
}

// triageIntegrationFixture is the release-level dashboard world. Unlike the
// narrower fixtures above, it deliberately composes every triage input in one
// place: every group/subtype rank, zero/equal/distinct presentation clocks,
// case-folded project identity across profiles and repository families, a label
// that needs quoted filter syntax, and enough canonical labels to make the
// project switcher scroll in a short terminal.
func triageIntegrationFixture() uitestFixture {
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha", RefreshInterval: 2,
			Preview: config.PreviewConfig{
				Direction: "horizontal", Ratio: 0.4, RefreshMs: 500, MinCols: 100, MinRows: 20,
			},
		},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "release-guard", Directory: "~/projects/release-guard", Profile: "work"},
			{Title: "portal-work", Directory: "~/projects/portal-work", Profile: "work"},
			{Title: "portal-personal", Directory: "~/projects/portal-personal", Profile: "personal"},
			{Title: "finished-work", Directory: "~/projects/finished-work", Profile: "personal"},
			{Title: "watchtower", Directory: "~/projects/watchtower", Profile: "work"},
			{Title: "build-farm", Directory: "~/projects/build-farm", Profile: "work"},
			{Title: "agent-fleet", Directory: "~/projects/agent-fleet", Profile: "personal"},
			{Title: "compact-ops", Directory: "~/projects/compact-ops", Profile: "personal"},
			{Title: "quiet-queue", Directory: "~/projects/quiet-queue", Profile: "work"},
			{Title: "archive", Directory: "~/projects/archive", Profile: "personal"},
			{Title: "legacy-lab", Directory: "~/projects/legacy-lab", Profile: "work"},
			{Title: "spare-parts", Directory: "~/projects/spare-parts", Profile: "personal"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "❗🦊release-guard", true, managedVars("work", "claude", "release-guard", "~/projects/release-guard")),
		uitestTab(2, 21, "🔑🦊portal-work", false, managedVars("work", "claude", "portal-work", "~/projects/portal-work")),
		uitestTab(3, 31, "❓🤖portal-personal", false, managedVars("personal", "codex", "portal-personal", "~/projects/portal-personal")),
		uitestTab(4, 41, "🛑🤖finished-work", false, managedVars("personal", "codex", "finished-work", "~/projects/finished-work")),
		uitestTab(5, 51, "⚠️🦊watchtower", false, managedVars("work", "claude", "watchtower", "~/projects/watchtower")),
		uitestTab(6, 61, "⚡🦊build-farm", false, managedVars("work", "claude", "build-farm", "~/projects/build-farm")),
		uitestTab(7, 71, "🤖🤖agent-fleet", false, managedVars("personal", "codex", "agent-fleet", "~/projects/agent-fleet")),
		uitestTab(8, 81, "🌀🤖compact-ops", false, managedVars("personal", "codex", "compact-ops", "~/projects/compact-ops")),
		uitestTab(9, 91, "🦊quiet-queue", false, managedVars("work", "claude", "quiet-queue", "~/projects/quiet-queue")),
		uitestTab(10, 101, "🛑🤖archive", false, managedVars("personal", "codex", "archive", "~/projects/archive")),
		uitestTab(11, 111, "🦊legacy-lab", false, managedVars("work", "claude", "legacy-lab", "~/projects/legacy-lab")),
		uitestTab(12, 121, "🛑🤖spare-parts", false, managedVars("personal", "codex", "spare-parts", "~/projects/spare-parts")),
	)}
	changed := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).UnixMilli() }
	return uitestFixture{
		Name:        "triage_integration",
		Description: "all triage ranks and presentation-clock tie cases across a scrollable case-folded project scope set",
		Size:        UITestSize{Width: 140, Height: 16},
		Config:      cfg,
		Windows:     windows,
		States: map[int]protocol.WindowStateSnapshot{
			11:  {IconState: "error", PresentationChangedAtMS: changed(1 * time.Second)},
			21:  {IconState: "key", Reason: "permission", PresentationChangedAtMS: changed(2 * time.Second)},
			31:  {IconState: "question", Reason: "request_user_input", PresentationChangedAtMS: changed(3 * time.Second)},
			41:  {IconState: "stop", Unseen: true, PresentationChangedAtMS: changed(4 * time.Second)},
			51:  {IconState: "warning", PresentationChangedAtMS: changed(5 * time.Second)},
			61:  {IconState: "bolt", PresentationChangedAtMS: changed(6 * time.Second)},
			71:  {IconState: "subagent", PresentationChangedAtMS: changed(6 * time.Second)},
			81:  {IconState: "compact"},
			91:  {},
			101: {IconState: "stop", PresentationChangedAtMS: changed(7 * time.Second)},
			111: {IconState: "legacy", PresentationChangedAtMS: changed(8 * time.Second)},
			121: {IconState: "stop"},
		},
		Families: map[int]uitestFamily{
			11:  {Key: "git:release", Label: "Release Guard", IsRepository: true, PrimaryRoot: "~/projects/release-guard"},
			21:  {Key: "git:portal-work", Label: "Client Portal", IsRepository: true, PrimaryRoot: "~/projects/portal-work"},
			31:  {Key: "git:portal-personal", Label: "client portal", IsRepository: true, PrimaryRoot: "~/projects/portal-personal"},
			41:  {Key: "git:finished", Label: "Finished Work", IsRepository: true, PrimaryRoot: "~/projects/finished-work"},
			51:  {Key: "git:watchtower", Label: "Watchtower", IsRepository: true, PrimaryRoot: "~/projects/watchtower"},
			61:  {Key: "git:build", Label: "Build Farm", IsRepository: true, PrimaryRoot: "~/projects/build-farm"},
			71:  {Key: "git:agents", Label: "Agent Fleet", IsRepository: true, PrimaryRoot: "~/projects/agent-fleet"},
			81:  {Key: "git:compact", Label: "Compact Ops", IsRepository: true, PrimaryRoot: "~/projects/compact-ops"},
			91:  {Key: "git:quiet", Label: "Quiet Queue", IsRepository: true, PrimaryRoot: "~/projects/quiet-queue"},
			101: {Key: "git:archive", Label: "Archive", IsRepository: true, PrimaryRoot: "~/projects/archive"},
			111: {Key: "git:legacy", Label: "Legacy Lab", IsRepository: true, PrimaryRoot: "~/projects/legacy-lab"},
			121: {Key: "git:spare", Label: "Spare Parts", IsRepository: true, PrimaryRoot: "~/projects/spare-parts"},
		},
	}
}

// triageVerticalFixture keeps the same integrated world and changes only the
// configured preview direction, which the dashboard no longer reads: the
// inspector is a side-by-side split or it is full screen, and there is no
// stacked form for the setting to select. The fixture earns its place by
// proving exactly that — same data, `direction = "vertical"`, and a screen the
// horizontal scenarios would recognize.
//
// The setting is not dead, which is why this is a claim about the dashboard
// rather than about the configuration: it still governs the launcher's own
// preview pane.
func triageVerticalFixture() uitestFixture {
	fx := triageIntegrationFixture()
	fx.Name = "triage_vertical"
	fx.Description = "triage_integration with the preview configured below the list"
	fx.Config.Defaults.Preview.Direction = "vertical"
	return fx
}

// familyUnreadFixture is the one world the unread-completion marker cannot be
// proved in anywhere else: an unseen stop sitting INSIDE a multi-checkout
// family, where folding the family is what decides whether the news is said on
// the summary or on the leaf.
//
// It is a separate fixture rather than an edit to project_families on purpose.
// That fixture's ordering, bucket spread, and count matrix are pinned by nine
// scenarios and read as a single load-bearing claim; adding a second unread
// member to it would have moved counts those scenarios assert exactly, and
// relaxing them to accommodate the marker would have spent grouping evidence to
// buy marker evidence. This fixture buys the marker evidence separately.
//
// The six rows are chosen so every way the marker can go wrong is visible in
// one screen:
//
//   - `svc`, a THREE-member family holding the unread stop (`svc-review`)
//     beside a SEEN stop (`svc-legacy`) and an active checkout (`svc`). The seen
//     sibling is the control: a marker driven by the stop state alone rather
//     than by the unseen bit would mark it too, and a collapsed summary that
//     aggregated "any stopped member" rather than "any unread member" would
//     still light up after the unread one was read;
//   - `web`, a two-member family with NO unread member at all, whose summary
//     must stay unmarked in both folds. Without it, a summary that marked every
//     family would pass every assertion `svc` can make;
//   - `tmp`, an unfamilied seen stop, which renders as a top-level leaf and
//     charges the gutter nothing.
//
// Every row is a stop or an active row and none carries a busy anchor that
// would pulse, so the gutter's contents are decided by the unseen bit alone:
// the pulse arm of the notice rule cannot fire here and cannot be mistaken for
// the dot. The surrounding rows are what make an alignment regression legible —
// five unmarked rows share the gutter with the marked one, so a marker rendered
// inside the row content instead of in front of it shifts exactly one line out
// of six.
func familyUnreadFixture() uitestFixture {
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha", RefreshInterval: 2},
		HarnessEmojis: map[string]string{"claude": "🦊", "codex": "🤖"},
		HarnessColors: map[string]string{"claude": "blue", "codex": "green"},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", HomeDir: "~/.config/agent-mux/homes/work", Color: "blue"},
			{Name: "personal", Harness: "codex", HomeDir: "~/.config/agent-mux/homes/personal", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "svc", Directory: "~/projects/svc", Profile: "work"},
			{Title: "svc-legacy", Directory: "~/projects/worktrees/svc-legacy", Profile: "work"},
			{Title: "svc-review", Directory: "~/projects/worktrees/svc-review", Profile: "work"},
			{Title: "web", Directory: "~/projects/web", Profile: "personal"},
			{Title: "web-beta", Directory: "~/projects/worktrees/web-beta", Profile: "personal"},
			{Title: "tmp", Directory: "~/tmp", Profile: "personal"},
		},
	}
	windows := []kitty.OSWindow{uitestOSWindow(
		uitestTab(1, 11, "⚡🦊svc", true, managedVars("work", "claude", "svc", "~/projects/svc")),
		uitestTab(2, 21, "🛑🦊svc-legacy", false, managedVars("work", "claude", "svc-legacy", "~/projects/worktrees/svc-legacy")),
		uitestTab(3, 31, "🛑🦊svc-review", false, managedVars("work", "claude", "svc-review", "~/projects/worktrees/svc-review")),
		uitestTab(4, 41, "⚡🤖web", false, managedVars("personal", "codex", "web", "~/projects/web")),
		uitestTab(5, 51, "🛑🤖web-beta", false, managedVars("personal", "codex", "web-beta", "~/projects/worktrees/web-beta")),
		uitestTab(6, 61, "🛑🤖tmp", false, managedVars("personal", "codex", "tmp", "~/tmp")),
	)}
	busySince := func(d time.Duration) int64 { return uitestFrozenNow.Add(-d).Unix() }
	return uitestFixture{
		Name:        "family_unread",
		Description: "an unread stop folded inside a three-checkout family, beside a seen sibling, a family with nothing unread, and an unfamilied leaf",
		Size:        UITestSize{Width: 120, Height: 32},
		Config:      cfg,
		Windows:     windows,
		States: map[int]protocol.WindowStateSnapshot{
			11: {IconState: "bolt", BusySinceAt: busySince(1*time.Minute + 12*time.Second), Branch: "main"},
			21: {IconState: "stop", Branch: "amx/legacy"},
			31: {IconState: "stop", Unseen: true, Branch: "amx/review"},
			41: {IconState: "bolt", BusySinceAt: busySince(24 * time.Second), Branch: "main"},
			51: {IconState: "stop", Branch: "amx/beta"},
			61: {IconState: "stop"},
		},
		Families: map[int]uitestFamily{
			11: {Key: "git:svc", Label: "svc", IsRepository: true, PrimaryRoot: "~/projects/svc"},
			21: {Key: "git:svc", Label: "svc", IsRepository: true, PrimaryRoot: "~/projects/svc"},
			31: {Key: "git:svc", Label: "svc", IsRepository: true, PrimaryRoot: "~/projects/svc"},
			41: {Key: "git:web", Label: "web", IsRepository: true, PrimaryRoot: "~/projects/web"},
			51: {Key: "git:web", Label: "web", IsRepository: true, PrimaryRoot: "~/projects/web"},
			// 61 (tmp) is deliberately absent, exactly as scratch is in
			// project_families: an authoritative Families map clears what it does
			// not name, which is the only way to say a row belongs to no family.
		},
	}
}

// uitestConfigEnv captures the throwaway locations a config-backed harness run
// redirects HOME and the AMX/XDG data paths to. Staging these before loading a
// config guarantees a run can never read or write the operator's real config,
// overrides, plan index, transcript index, or daemon socket.
type uitestConfigEnv struct {
	Root       string            // temp root containing every staged location
	Home       string            // staged $HOME
	ConfigPath string            // staged config.toml the run loads
	Vars       map[string]string // env var → value applied via the setenv callback
}

// IsolateUITestEnv points every user-path helper at an empty environment
// under root before a harness run builds a model. A fixture needs it as much
// as a staged config does: constructing a model loads the launch overrides
// and prunes the entries its projects do not name, and with the operator's
// own HOME that is the operator's file, emptied by a fixture's projects.
func IsolateUITestEnv(root string, setenv func(string, string) error) error {
	_, err := stageUITestConfigEnv(root, "", setenv)
	return err
}

// stageUITestConfigEnv stages an isolated environment under root and, when
// srcConfigPath is non-empty, copies that config into it. It applies HOME,
// AMX_CONFIG, AMX_OVERRIDES_PATH, XDG_DATA_HOME, and XDG_STATE_HOME through
// setenv so every user-path helper (config, overrides, plans, transcript,
// daemon) resolves under root. Pass t.Setenv in tests and os.Setenv in the CLI.
func stageUITestConfigEnv(root, srcConfigPath string, setenv func(string, string) error) (uitestConfigEnv, error) {
	home := filepath.Join(root, "home")
	dataHome := filepath.Join(root, "xdg-data")
	stateHome := filepath.Join(root, "xdg-state")
	cfgDir := filepath.Join(home, ".config", "agent-mux")
	for _, dir := range []string{home, dataHome, stateHome, cfgDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return uitestConfigEnv{}, fmt.Errorf("uitest: staging %s: %w", dir, err)
		}
	}

	configPath := filepath.Join(cfgDir, "config.toml")
	if srcConfigPath != "" {
		data, err := os.ReadFile(srcConfigPath)
		if err != nil {
			return uitestConfigEnv{}, fmt.Errorf("uitest: reading config %s: %w", srcConfigPath, err)
		}
		if err := os.WriteFile(configPath, data, 0o644); err != nil {
			return uitestConfigEnv{}, fmt.Errorf("uitest: staging config: %w", err)
		}
	}

	vars := map[string]string{
		"HOME":               home,
		"AMX_CONFIG":         configPath,
		overrides.PathEnvVar: filepath.Join(home, ".local", "share", "agent-mux", "launch_overrides.json"),
		"XDG_DATA_HOME":      dataHome,
		"XDG_STATE_HOME":     stateHome,
	}
	// Apply in a stable order so a setenv failure is attributed deterministically.
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := setenv(k, vars[k]); err != nil {
			return uitestConfigEnv{}, fmt.Errorf("uitest: setting %s: %w", k, err)
		}
	}

	return uitestConfigEnv{Root: root, Home: home, ConfigPath: configPath, Vars: vars}, nil
}

// The agent-session-loop runs fixture. It stands in for one daemon ASL
// snapshot: the projected rows, the capability block, and the question bodies
// the bridge would hand back one at a time. The tokens here are deliberately
// per-question rather than per-row, because that is what a form has to re-read
// between closures.

// aslRunsFixture covers the four focus outcomes the Runs view has to tell
// apart: an exactly bound run, a run whose workspace and harness match several
// live sessions, a run bound through a second exactly matching session, and a
// run with no live session at all.
func aslRunsFixture() uitestFixture {
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
	return uitestFixture{
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

// Worktree fixtures. Between them they cover every state the Worktrees view has
// to tell apart — clean, dirty, live, looping, missing, metadata-invalid — plus
// the lifecycle outcomes the harness scripts against them in
// uitest_driver_fixture.go: reopen, cleanup, partial failure, and refusal.

func worktreesFixture() uitestFixture {
	fx := basicFixture()
	fx.Name = "worktrees"
	fx.Description = "managed worktrees spanning clean, dirty, live, looping, missing, and invalid states"
	fx.Size = UITestSize{Width: 120, Height: 32}
	now := time.Now().Add(-2 * time.Hour)
	merged := true
	base := func(id, project, branch, path string) actions.WorktreeView {
		return actions.WorktreeView{Entry: worktree.Entry{
			ID: id, Classification: worktree.ClassificationValid,
			ProjectLabel: project, Branch: branch, Path: path,
			Profile: "work", Harness: "claude", CreatedAt: now,
			Exists: true, GitAvailable: true, StatusAvailable: true,
			Registered: true, Actionable: true,
		}, LiveSessions: []actions.WorktreeLiveSession{}, Warnings: []string{}}
	}
	clean := base("wt-clean", "agentmux", "amx/clean", "/tmp/uitest/worktrees/clean")
	clean.ReachableFromSource = &merged
	dirty := base("wt-dirty", "api", "amx/dirty", "/tmp/uitest/worktrees/dirty")
	dirty.Dirty = true
	live := base("wt-live", "backend", "amx/live", "/tmp/uitest/worktrees/live")
	live.Usage.Live = true
	live.LiveSessions = []actions.WorktreeLiveSession{{ID: "7", TabID: 7, Directory: live.Path}}
	looping := base("wt-loop", "worker", "amx/looping", "/tmp/uitest/worktrees/looping")
	looping.Usage.Looping = true
	looping.LoopCount = 1
	missing := base("wt-missing", "legacy", "amx/missing", "/tmp/uitest/worktrees/missing")
	missing.Classification = worktree.ClassificationMissing
	missing.Exists, missing.GitAvailable, missing.StatusAvailable, missing.Actionable = false, false, false, false
	invalid := base("wt-invalid", "broken", "amx/invalid", "/tmp/uitest/worktrees/invalid")
	invalid.Classification = worktree.ClassificationMetadataInvalid
	invalid.Actionable = false
	invalid.Warnings = []string{"metadata checksum mismatch"}
	fx.Worktrees = []actions.WorktreeView{clean, dirty, live, looping, missing, invalid}
	return fx
}

func worktreeLifecycleFixture() uitestFixture {
	fx := basicFixture()
	fx.Name = "worktree_lifecycle"
	fx.Description = "deterministic worktree reopen, cleanup, recovery, and refusal outcomes"
	fx.Size = UITestSize{Width: 132, Height: 34}
	now := time.Now().Add(-90 * time.Minute)
	merged, unmerged := true, false
	base := func(id, project string) actions.WorktreeView {
		path := "/tmp/uitest/worktrees/" + id
		entry := worktree.Entry{
			ID: id, Classification: worktree.ClassificationValid,
			ProjectLabel: project, Branch: "amx/" + strings.TrimPrefix(id, "wt-"), Path: path,
			SourceRoot: "/tmp/uitest/sources/" + project, SourceBranch: "main",
			Profile: "work", Harness: "claude", CreatedAt: now,
			Exists: true, GitAvailable: true, StatusAvailable: true, Registered: true, Actionable: true,
		}
		fx.Config.Sessions = append(fx.Config.Sessions, config.Session{Title: project, Directory: entry.SourceRoot, Profile: "work"})
		return actions.WorktreeView{Entry: entry, LiveSessions: []actions.WorktreeLiveSession{}, Warnings: []string{}}
	}
	live := base("wt-live", "live-project")
	live.Usage.Live = true
	live.LiveSessions = []actions.WorktreeLiveSession{{ID: "71", TabID: 71, WindowID: 711, Project: live.ProjectLabel, Profile: "work", Harness: "claude", HarnessSessionID: "native-live-71", Directory: live.Path}}
	multi := base("wt-multi", "multi-project")
	multi.Usage.Live = true
	multi.LiveSessions = []actions.WorktreeLiveSession{
		{ID: "81", TabID: 81, WindowID: 811, Project: multi.ProjectLabel, Profile: "work", Harness: "claude", HarnessSessionID: "native-multi-81", Directory: multi.Path},
		{ID: "82", TabID: 82, WindowID: 822, Project: multi.ProjectLabel, Profile: "personal", Harness: "codex", Directory: multi.Path},
	}
	resume := base("wt-resume", "resume-project")
	fresh := base("wt-fresh", "fresh-project")
	profile := base("wt-profile", "profile-project")
	profile.Profile = "removed-profile"
	clean := base("wt-cleanup", "cleanup-project")
	clean.ReachableFromSource = &merged
	deleteMerged := base("wt-delete", "delete-project")
	deleteMerged.ReachableFromSource = &merged
	unmergedEntry := base("wt-unmerged", "unmerged-project")
	unmergedEntry.ReachableFromSource = &unmerged
	two, none := 2, 0
	unmergedEntry.Ahead, unmergedEntry.Behind = &two, &none
	clean.Ahead, clean.Behind = &none, &none
	diverged := base("wt-diverged", "diverged-project")
	diverged.ReachableFromSource = &unmerged
	diverged.Ahead, diverged.Behind = &two, &two
	dirty := base("wt-dirty", "dirty-project")
	dirty.Dirty = true
	looping := base("wt-looping", "looping-project")
	looping.Usage.Looping, looping.LoopCount = true, 1
	partial := base("wt-partial", "partial-project")
	partial.ReachableFromSource = &merged
	missing := base("wt-missing", "missing-project")
	missing.Classification = worktree.ClassificationMissing
	missing.Exists, missing.GitAvailable, missing.StatusAvailable, missing.Actionable = false, false, false, false
	invalid := base("wt-invalid", "invalid-project")
	invalid.Classification, invalid.Actionable = worktree.ClassificationMetadataInvalid, false
	invalid.Warnings = []string{"metadata checksum mismatch"}
	fx.Worktrees = []actions.WorktreeView{live, multi, resume, fresh, profile, clean, deleteMerged, unmergedEntry, diverged, dirty, looping, partial, missing, invalid}
	return fx
}

type hermeticUITestSnapshot struct {
	transcriptIndex   *transcript.Index
	transcriptReaders map[string]transcript.Reader
	transcriptResults []transcript.Result
	planIndex         *plans.Index
}

// newHermeticUITestSnapshot creates filesystem-backed transcript and plan
// fixtures beneath a private temporary directory. Nothing consults HOME or
// opens the operator's caches.
func newHermeticUITestSnapshot(parent string) (hermeticUITestSnapshot, string, error) {
	root, err := os.MkdirTemp(parent, "agentmux-uitest-")
	if err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: creating fixture root: %w", err)
	}
	cleanupRoot := true
	defer func() {
		if cleanupRoot {
			_ = os.RemoveAll(root)
		}
	}()

	planRoot := filepath.Join(root, "plans")
	planDir := filepath.Join(planRoot, "fixture plan-aabb112233cc")
	if err := os.MkdirAll(planDir, 0o700); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: creating fixture plan: %w", err)
	}
	if err := os.WriteFile(filepath.Join(planDir, "manifest.md"), []byte("# Fixture plan\n\nDeterministic UI harness content.\n"), 0o600); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: writing fixture plan: %w", err)
	}

	now := time.Date(2026, time.January, 2, 15, 4, 5, 0, time.UTC)
	transcriptMeta := transcript.SessionMeta{
		ID: "fixture-conversation", Harness: "claude", Profile: "work", Profiles: []string{"work"},
		Project: "api", CWD: filepath.Join(root, "projects", "api"),
		SessionClass: transcript.SessionClassMain,
		StartedAt:    now.Add(-time.Hour), LastAt: now,
		Path:         filepath.Join(root, "transcripts", "fixture.jsonl"),
		MessageCount: 2, Snippet: "deterministic transcript fixture",
		Ending: "fixture conversation wrapped up",
	}
	if err := os.MkdirAll(filepath.Dir(transcriptMeta.Path), 0o700); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: creating transcript source: %w", err)
	}
	if err := os.WriteFile(transcriptMeta.Path, []byte(`{"type":"user","timestamp":"2026-01-02T14:04:05Z","message":{"role":"user","content":"deterministic transcript fixture"}}`+"\n"+
		`{"type":"assistant","timestamp":"2026-01-02T15:04:05Z","message":{"role":"assistant","content":[{"type":"text","text":"fixture conversation wrapped up"}]}}`+"\n"), 0o600); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: writing transcript source: %w", err)
	}
	transcriptPath := filepath.Join(root, "data", "transcripts.json")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: creating transcript cache: %w", err)
	}
	cacheData, err := json.Marshal(struct {
		Entries map[string]transcript.SessionMeta `json:"entries"`
	}{Entries: map[string]transcript.SessionMeta{
		transcript.IndexKey(transcriptMeta.Harness, transcriptMeta.ID): transcriptMeta,
	}})
	if err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: encoding transcript cache: %w", err)
	}
	if err := os.WriteFile(transcriptPath, cacheData, 0o600); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: seeding transcript cache: %w", err)
	}

	readers := buildTranscriptReaders()
	transcriptIndex, err := transcript.LoadIndexAt(transcriptPath)
	if err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: attaching transcript fixture: %w", err)
	}
	if transcriptIndex == nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: attaching transcript fixture: nil index")
	}
	planIndex, err := plans.LoadIndexAt(filepath.Join(root, "data", "plans.json"))
	if err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: attaching plan fixture: %w", err)
	}
	if planIndex == nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: attaching plan fixture: nil index")
	}
	if err := planIndex.Refresh(planRoot); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: indexing plan fixture: %w", err)
	}

	results, searchErr := transcript.Search(transcriptIndex, readers, transcript.Query{MainOnly: true})
	if searchErr != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: loading transcript fixture: %w", searchErr)
	}

	snapshot := hermeticUITestSnapshot{
		transcriptIndex: transcriptIndex, transcriptReaders: readers,
		planIndex: planIndex, transcriptResults: results,
	}
	cleanupRoot = false
	return snapshot, root, nil
}
