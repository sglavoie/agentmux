package tui

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

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/aslbridge"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/overrides"
)

// UITestFixture is a deterministic, self-contained data source for a harness
// run: a prepared config plus the Kitty windows a no-op client should report,
// and a default terminal size. Building a driver from a fixture never reads the
// operator's real config and never launches a subprocess.
type UITestFixture struct {
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
	States   map[int]daemon.WindowStateSnapshot
	Watchdog map[int]daemon.WatchdogState
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
	ASLActionOutcomes map[string]UITestASLOutcome
}

// UITestASLOutcome is one scripted result for a submitted mutation. ErrorCode
// wins when both are set, and is the bridge refusal code the UI maps to a fixed
// operator-facing sentence.
type UITestASLOutcome struct {
	Status    string
	ErrorCode string
}

// uitestFixtureBuilders maps a fixture name to a constructor. Each constructor
// returns freshly built values so callers can never mutate shared fixture
// state across runs.
var uitestFixtureBuilders = map[string]func() UITestFixture{
	"basic":              basicFixture,
	"actions":            actionsFixture,
	"asl_question":       aslQuestionFixture,
	"wide_profiles":      wideProfilesFixture,
	"worktrees":          worktreesFixture,
	"worktree_lifecycle": worktreeLifecycleFixture,
	"asl_runs":           aslRunsFixture,
	"run_timers":         runTimersFixture,
}

// UITestFixtureError reports a request for an unknown built-in fixture and
// lists the names that are available.
type UITestFixtureError struct {
	Name  string
	Known []string
}

func (e *UITestFixtureError) Error() string {
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

// UITestFixtureByName builds the named fixture, or returns a structured
// *UITestFixtureError when the name is not recognized.
func UITestFixtureByName(name string) (UITestFixture, error) {
	build, ok := uitestFixtureBuilders[name]
	if !ok {
		return UITestFixture{}, &UITestFixtureError{Name: name, Known: UITestFixtureNames()}
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
func basicFixture() UITestFixture {
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
	return UITestFixture{
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
func actionsFixture() UITestFixture {
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
	return UITestFixture{
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
func aslQuestionFixture() UITestFixture {
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
	return UITestFixture{
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
func wideProfilesFixture() UITestFixture {
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
	return UITestFixture{
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
func runTimersFixture() UITestFixture {
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
	return UITestFixture{
		Name:        "run_timers",
		Description: "busy stretches spanning the sub-minute, multi-minute, hours-long, warning, and idle timer bands",
		Size:        UITestSize{Width: 120, Height: 32},
		Config:      cfg,
		Windows:     windows,
		States: map[int]daemon.WindowStateSnapshot{
			11: {IconState: "bolt", BusySinceAt: busySince(7 * time.Second)},
			21: {IconState: "subagent", BusySinceAt: busySince(3*time.Minute + 7*time.Second)},
			31: {IconState: "compact", BusySinceAt: busySince(12*time.Hour + 7*time.Minute)},
			41: {IconState: "bolt", BusySinceAt: busySince(9*time.Minute + 41*time.Second)},
			51: {IconState: "stop"},
		},
		Watchdog: map[int]daemon.WatchdogState{
			11: {LastAction: "active", LastEventAt: lastEvent(7 * time.Second), ThresholdSec: thresholdSec},
			21: {LastAction: "subagent", LastEventAt: lastEvent(3*time.Minute + 7*time.Second), ThresholdSec: thresholdSec},
			31: {LastAction: "compact", LastEventAt: lastEvent(45 * time.Second), ThresholdSec: thresholdSec},
			41: {LastAction: "active", LastEventAt: lastEvent(9*time.Minute + 41*time.Second), ThresholdSec: thresholdSec},
			51: {LastAction: "active", LastEventAt: lastEvent(22 * time.Minute), ThresholdSec: thresholdSec},
		},
	}
}

// UITestConfigEnv captures the throwaway locations a config-backed harness run
// redirects HOME and the AMX/XDG data paths to. Staging these before loading a
// config guarantees a run can never read or write the operator's real config,
// overrides, plan index, transcript index, or daemon socket.
type UITestConfigEnv struct {
	Root       string            // temp root containing every staged location
	Home       string            // staged $HOME
	ConfigPath string            // staged config.toml the run loads
	Vars       map[string]string // env var → value applied via the setenv callback
}

// StageUITestConfigEnv stages an isolated environment under root and, when
// srcConfigPath is non-empty, copies that config into it. It applies HOME,
// AMX_CONFIG, AMX_OVERRIDES_PATH, XDG_DATA_HOME, and XDG_STATE_HOME through
// setenv so every user-path helper (config, overrides, plans, transcript,
// daemon) resolves under root. Pass t.Setenv in tests and os.Setenv in the CLI.
func StageUITestConfigEnv(root, srcConfigPath string, setenv func(string, string) error) (UITestConfigEnv, error) {
	home := filepath.Join(root, "home")
	dataHome := filepath.Join(root, "xdg-data")
	stateHome := filepath.Join(root, "xdg-state")
	cfgDir := filepath.Join(home, ".config", "agent-mux")
	for _, dir := range []string{home, dataHome, stateHome, cfgDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return UITestConfigEnv{}, fmt.Errorf("uitest: staging %s: %w", dir, err)
		}
	}

	configPath := filepath.Join(cfgDir, "config.toml")
	if srcConfigPath != "" {
		data, err := os.ReadFile(srcConfigPath)
		if err != nil {
			return UITestConfigEnv{}, fmt.Errorf("uitest: reading config %s: %w", srcConfigPath, err)
		}
		if err := os.WriteFile(configPath, data, 0o644); err != nil {
			return UITestConfigEnv{}, fmt.Errorf("uitest: staging config: %w", err)
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
			return UITestConfigEnv{}, fmt.Errorf("uitest: setting %s: %w", k, err)
		}
	}

	return UITestConfigEnv{Root: root, Home: home, ConfigPath: configPath, Vars: vars}, nil
}
