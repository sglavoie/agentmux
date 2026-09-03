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
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/plans"
	"github.com/sglavoie/agentmux/internal/transcript"
	"github.com/sglavoie/agentmux/internal/worktree"
)

// This file supplies the deterministic data sources the harness driver runs
// against: the fixture type and registry, the dashboard fixtures themselves,
// and a safe config mode that stages HOME and the AMX/XDG data paths in
// throwaway directories so a run can never read or write the operator's real
// config, overrides, plan index, transcript index, or daemon socket. Fixture
// selection is explicit and discoverable so later CLI work can list and address
// them by name.
//
// The larger fixtures live beside this one: worktrees in
// uitest_fixtures_worktrees.go and agent-session-loop runs in
// uitest_fixtures_asl.go. The recording Kitty client they are all served
// through is in uitest_kitty_client.go.

// uitestFixture is a deterministic, self-contained data source for a harness
// run: a prepared config plus the Kitty windows a no-op client should report,
// and a default terminal size. Building a driver from a fixture never reads the
// operator's real config and never launches a subprocess.
type uitestFixture struct {
	Name        string
	Description string
	Size        UITestSize
	Config      *config.Config
	Windows     []kitty.OSWindow
	Worktrees   []actions.WorktreeView
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
// gloss, and the `]q` jump — without a daemon painting the state.
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

// blockedReasonsFixture is four Needs Attention rows that a tab title cannot
// tell apart — two 🔑 and two ❓ — where only the daemon knows what each one is
// actually waiting for. It covers the three cases the reason column has to get
// right at once:
//
//   - a Codex permission prompt and a Claude permission prompt, whose kind
//     strings are deliberately different (`command_approval` vs `permission`,
//     because the two harnesses share one untyped field) and which must
//     nonetheless read as the same `perm` tag: the tag names what is being asked
//     of the user, not which dialog asked it;
//   - a Codex request-for-input prompt, whose `input` tag is the widest in the
//     view and therefore the one that sets the column width for every row;
//   - a blocked row with NO reason at all, which is the ordinary steady state
//     for a hook-raised icon no screen probe has labelled yet. Its cell must pad
//     to the same width as the others so the column stays a column.
//
// The `working` row holds Active so the reserved-for-the-whole-pass rule is
// visible too: it is not in the attention group, has nothing to explain, and
// still keeps its badge and title in the same columns as the rows above it.
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
	fx.Worktrees = []actions.WorktreeView{live, multi, resume, fresh, profile, clean, deleteMerged, unmergedEntry, dirty, looping, partial, missing, invalid}
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
	}
	if err := os.MkdirAll(filepath.Dir(transcriptMeta.Path), 0o700); err != nil {
		return hermeticUITestSnapshot{}, "", fmt.Errorf("uitest: creating transcript source: %w", err)
	}
	if err := os.WriteFile(transcriptMeta.Path, []byte(`{"type":"user","timestamp":"2026-01-02T14:04:05Z","message":{"role":"user","content":"deterministic transcript fixture"}}`+"\n"), 0o600); err != nil {
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

// This file is the harness's stand-in for Kitty. Reads answer from canned
// fixture data and every operation that would mutate a live terminal or move
// focus is recorded instead of executed, so a scenario can assert on what the
// model attempted without a subprocess ever running.

// uitestKittyCall records one Kitty operation the harness client received.
// Reads (ListWindows/GetText) are not recorded; every call that would mutate a
// live terminal or move focus is captured here instead of being executed, so
// scenarios can assert on the side effects the model attempted.
type uitestKittyCall struct {
	Op    string            // operation name, e.g. "LaunchTab", "CloseTab", "FocusTab"
	Match string            // match expression, when the op takes one
	Title string            // for SetTabTitle / launch titles
	Vars  map[string]string // for SetUserVars
	Opts  *kitty.LaunchOpts // for LaunchTab / LaunchOverlay
	Delta int               // for MoveTab
}

// uitestKittyClient is a kitty.KittyClient that never talks to a live Kitty
// instance. ListWindows and GetText return canned fixture data; every mutating
// or focus operation is appended to Calls. It is the client every harness run
// uses so a scenario can drive the real model without a subprocess ever
// executing.
type uitestKittyClient struct {
	windows []kitty.OSWindow
	text    string
	// Calls is the ordered log of trapped mutating/focus/overlay operations.
	Calls []uitestKittyCall
}

// compile-time assertion that the no-op client satisfies the full interface.
var _ kitty.KittyClient = (*uitestKittyClient)(nil)

// newUITestKittyClient builds a recording client that reports the given windows
// from ListWindows and traps every mutating call.
func newUITestKittyClient(windows []kitty.OSWindow) *uitestKittyClient {
	return &uitestKittyClient{windows: windows}
}

func (c *uitestKittyClient) ListWindows() ([]kitty.OSWindow, error) { return c.windows, nil }

func (c *uitestKittyClient) LaunchTab(opts kitty.LaunchOpts) error {
	o := opts
	c.Calls = append(c.Calls, uitestKittyCall{Op: "LaunchTab", Title: opts.Title, Opts: &o})
	return nil
}

func (c *uitestKittyClient) LaunchOverlay(opts kitty.LaunchOpts) error {
	o := opts
	c.Calls = append(c.Calls, uitestKittyCall{Op: "LaunchOverlay", Title: opts.Title, Opts: &o})
	return nil
}

func (c *uitestKittyClient) CloseTab(matchExpr string) error {
	c.Calls = append(c.Calls, uitestKittyCall{Op: "CloseTab", Match: matchExpr})
	return nil
}

func (c *uitestKittyClient) MoveTab(matchExpr string, delta int) error {
	c.Calls = append(c.Calls, uitestKittyCall{Op: "MoveTab", Match: matchExpr, Delta: delta})
	return nil
}

func (c *uitestKittyClient) SetTabColor(matchExpr, _, _, _, _ string) error {
	c.Calls = append(c.Calls, uitestKittyCall{Op: "SetTabColor", Match: matchExpr})
	return nil
}

func (c *uitestKittyClient) SetTabTitle(matchExpr, title string) error {
	c.Calls = append(c.Calls, uitestKittyCall{Op: "SetTabTitle", Match: matchExpr, Title: title})
	return nil
}

func (c *uitestKittyClient) SetUserVars(matchExpr string, vars map[string]string) error {
	cp := make(map[string]string, len(vars))
	for k, v := range vars {
		cp[k] = v
	}
	c.Calls = append(c.Calls, uitestKittyCall{Op: "SetUserVars", Match: matchExpr, Vars: cp})
	return nil
}

func (c *uitestKittyClient) FocusTab(matchExpr string) error {
	c.Calls = append(c.Calls, uitestKittyCall{Op: "FocusTab", Match: matchExpr})
	return nil
}

func (c *uitestKittyClient) FocusWindow(matchExpr string) error {
	c.Calls = append(c.Calls, uitestKittyCall{Op: "FocusWindow", Match: matchExpr})
	return nil
}

func (c *uitestKittyClient) GetText(_, _ string) (string, error) { return c.text, nil }

func (c *uitestKittyClient) LaunchOverlayPager(sourceMatchExpr string) error {
	c.Calls = append(c.Calls, uitestKittyCall{Op: "LaunchOverlayPager", Match: sourceMatchExpr})
	return nil
}

// CallsFor returns the recorded calls whose Op equals op, in order.
func (c *uitestKittyClient) CallsFor(op string) []uitestKittyCall {
	var out []uitestKittyCall
	for _, call := range c.Calls {
		if call.Op == op {
			out = append(out, call)
		}
	}
	return out
}
