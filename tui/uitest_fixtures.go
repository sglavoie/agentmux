package tui

// This file supplies the deterministic data sources the harness driver runs
// against: built-in fixtures (config + fake Kitty windows), a no-op recording
// kitty.KittyClient that never launches a subprocess, and a safe config mode
// that stages HOME and the AMX/XDG data paths in throwaway directories so a run
// can never read or write the operator's real config, overrides, plan index,
// transcript index, or daemon socket. Fixture selection is explicit and
// discoverable so later CLI work can list and address them by name.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/overrides"
	"github.com/sglavoie/agentmux/internal/session"
	"github.com/sglavoie/agentmux/internal/workspace"
)

// UITestKittyCall records one Kitty operation the harness client received.
// Reads (ListWindows/GetText) are not recorded; every call that would mutate a
// live terminal or move focus is captured here instead of being executed, so
// scenarios can assert on the side effects the model attempted.
type UITestKittyCall struct {
	Op    string            // operation name, e.g. "LaunchTab", "CloseTab", "FocusTab"
	Match string            // match expression, when the op takes one
	Title string            // for SetTabTitle / launch titles
	Vars  map[string]string // for SetUserVars
	Opts  *kitty.LaunchOpts // for LaunchTab / LaunchOverlay
	Delta int               // for MoveTab
}

// UITestKittyClient is a kitty.KittyClient that never talks to a live Kitty
// instance. ListWindows and GetText return canned fixture data; every mutating
// or focus operation is appended to Calls. It is the client every harness run
// uses so a scenario can drive the real model without a subprocess ever
// executing.
type UITestKittyClient struct {
	windows []kitty.OSWindow
	text    string
	// Calls is the ordered log of trapped mutating/focus/overlay operations.
	Calls []UITestKittyCall
}

// compile-time assertion that the no-op client satisfies the full interface.
var _ kitty.KittyClient = (*UITestKittyClient)(nil)

// NewUITestKittyClient builds a recording client that reports the given windows
// from ListWindows and traps every mutating call.
func NewUITestKittyClient(windows []kitty.OSWindow) *UITestKittyClient {
	return &UITestKittyClient{windows: windows}
}

func (c *UITestKittyClient) ListWindows() ([]kitty.OSWindow, error) { return c.windows, nil }

func (c *UITestKittyClient) LaunchTab(opts kitty.LaunchOpts) error {
	o := opts
	c.Calls = append(c.Calls, UITestKittyCall{Op: "LaunchTab", Title: opts.Title, Opts: &o})
	return nil
}

func (c *UITestKittyClient) LaunchOverlay(opts kitty.LaunchOpts) error {
	o := opts
	c.Calls = append(c.Calls, UITestKittyCall{Op: "LaunchOverlay", Title: opts.Title, Opts: &o})
	return nil
}

func (c *UITestKittyClient) CloseTab(matchExpr string) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "CloseTab", Match: matchExpr})
	return nil
}

func (c *UITestKittyClient) MoveTab(matchExpr string, delta int) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "MoveTab", Match: matchExpr, Delta: delta})
	return nil
}

func (c *UITestKittyClient) SetTabColor(matchExpr, _, _, _, _ string) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "SetTabColor", Match: matchExpr})
	return nil
}

func (c *UITestKittyClient) SetTabTitle(matchExpr, title string) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "SetTabTitle", Match: matchExpr, Title: title})
	return nil
}

func (c *UITestKittyClient) SetUserVars(matchExpr string, vars map[string]string) error {
	cp := make(map[string]string, len(vars))
	for k, v := range vars {
		cp[k] = v
	}
	c.Calls = append(c.Calls, UITestKittyCall{Op: "SetUserVars", Match: matchExpr, Vars: cp})
	return nil
}

func (c *UITestKittyClient) FocusTab(matchExpr string) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "FocusTab", Match: matchExpr})
	return nil
}

func (c *UITestKittyClient) FocusWindow(matchExpr string) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "FocusWindow", Match: matchExpr})
	return nil
}

func (c *UITestKittyClient) GetText(_, _ string) (string, error) { return c.text, nil }

func (c *UITestKittyClient) LaunchOverlayPager(sourceMatchExpr string) error {
	c.Calls = append(c.Calls, UITestKittyCall{Op: "LaunchOverlayPager", Match: sourceMatchExpr})
	return nil
}

// CallsFor returns the recorded calls whose Op equals op, in order.
func (c *UITestKittyClient) CallsFor(op string) []UITestKittyCall {
	var out []UITestKittyCall
	for _, call := range c.Calls {
		if call.Op == op {
			out = append(out, call)
		}
	}
	return out
}

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
	// WorkspaceHome and WorkspaceSnapshot opt a fixture into the project
	// dashboard without loading a user's datastore, indexes, or Git state.
	WorkspaceHome     bool
	WorkspaceSnapshot workspace.Snapshot
}

// uitestFixtureBuilders maps a fixture name to a constructor. Each constructor
// returns freshly built values so callers can never mutate shared fixture
// state across runs.
var uitestFixtureBuilders = map[string]func() UITestFixture{
	"basic":         basicFixture,
	"actions":       actionsFixture,
	"wide_profiles": wideProfilesFixture,
}

// workspaceUITestFixtureBuilders are focused layout fixtures. They stay out
// of the long-standing public fixture picker so existing automation which
// enumerates that small compatibility set remains stable; scenarios can still
// select them by their explicit names.
var workspaceUITestFixtureBuilders = map[string]func() UITestFixture{
	"workspace_empty":     workspaceEmptyFixture,
	"workspace_attention": workspaceAttentionFixture,
	"workspace_profiles":  workspaceProfilesFixture,
	"workspace_worktrees": workspaceWorktreesFixture,
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
		build, ok = workspaceUITestFixtureBuilders[name]
	}
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

func workspaceFixtureConfig() *config.Config {
	return &config.Config{
		Defaults: config.Defaults{Sort: "alpha", RefreshInterval: 2},
		Accounts: []config.Account{
			{Name: "work", Harness: "claude", Color: "blue"},
			{Name: "review", Harness: "codex", Color: "green"},
		},
		Sessions: []config.Session{
			{Title: "api", Directory: "/work/api", Profile: "work"},
			{Title: "frontend", Directory: "/work/frontend", Profile: "review"},
		},
	}
}

func workspaceEmptyFixture() UITestFixture {
	return UITestFixture{
		Name:              "workspace_empty",
		Description:       "empty project workspace home",
		Size:              UITestSize{Width: 100, Height: 24},
		Config:            workspaceFixtureConfig(),
		WorkspaceHome:     true,
		WorkspaceSnapshot: workspace.Snapshot{},
	}
}

func workspaceAttentionFixture() UITestFixture {
	now := time.Now()
	api := workspace.WorkspaceSnapshot{
		ID:          "api",
		DisplayName: "api",
		Urgency:     workspace.UrgencyAttention,
		LiveSessions: []workspace.LiveSession{
			{Session: session.RunningSession{TabID: 1, WindowID: 11, Base: "api", Project: "api", Profile: "work", Account: "work", State: "key"}},
			{Session: session.RunningSession{TabID: 2, WindowID: 12, Base: "worker", Project: "api", Profile: "work", Account: "work", State: "error"}},
			{Session: session.RunningSession{TabID: 3, WindowID: 13, Base: "server", Project: "api", Profile: "work", Account: "work", State: "bolt"}},
		},
		Conversations: []workspace.Conversation{{ID: "conv-api", Harness: "claude", Profile: "work", Project: "api"}},
		Plans:         []workspace.LinkedPlan{{ID: "plan-api", Name: "api migration", Directory: "/plans/api", Files: []string{"plan.md"}}},
		Activity: []workspace.ActivityItem{
			{ID: 2, Harness: "claude", SessionID: "conv-api", WindowID: 11, Kind: "attention_permission", Summary: "Permission requested", OccurredAt: now.Add(-time.Minute), Unread: true},
			{ID: 1, Harness: "claude", SessionID: "conv-api", WindowID: 11, Kind: "prompt", Summary: "Prompt submitted", OccurredAt: now.Add(-2 * time.Minute), Unread: true},
		},
		UnreadCount:           2,
		ActivityLoadedThrough: now.Add(-time.Minute),
		LastDurableActivityAt: now.Add(-time.Minute),
	}
	frontend := workspace.WorkspaceSnapshot{
		ID:          "frontend",
		DisplayName: "frontend",
		Urgency:     workspace.UrgencyActive,
		LiveSessions: []workspace.LiveSession{
			{Session: session.RunningSession{TabID: 4, WindowID: 14, Base: "frontend", Project: "frontend", Profile: "review", Account: "review", State: "bolt"}},
		},
	}
	return UITestFixture{
		Name:              "workspace_attention",
		Description:       "attention-heavy workspace with live, recent, and plan detail sections",
		Size:              UITestSize{Width: 120, Height: 30},
		Config:            workspaceFixtureConfig(),
		WorkspaceHome:     true,
		WorkspaceSnapshot: workspace.Snapshot{Workspaces: []workspace.WorkspaceSnapshot{api, frontend}},
	}
}

func workspaceProfilesFixture() UITestFixture {
	base := workspaceAttentionFixture()
	base.Name = "workspace_profiles"
	base.Description = "workspace with multiple configured launch profiles"
	base.WorkspaceSnapshot.Workspaces = []workspace.WorkspaceSnapshot{{
		ID:            "platform",
		DisplayName:   "platform",
		Urgency:       workspace.UrgencyRecent,
		DefaultLaunch: config.Session{Title: "platform", Directory: "/work/platform", Profile: "work"},
		LaunchVariants: []config.Session{
			{Title: "platform", Directory: "/work/platform", Profile: "review"},
			{Title: "platform", Directory: "/work/platform", Profile: "work", Args: []string{"--safe"}},
		},
		LiveSessions: []workspace.LiveSession{
			{Session: session.RunningSession{TabID: 8, WindowID: 18, Base: "platform", Project: "platform", Profile: "review", Account: "review", State: "bolt"}},
		},
	}}
	return base
}

func workspaceWorktreesFixture() UITestFixture {
	base := workspaceAttentionFixture()
	base.Name = "workspace_worktrees"
	base.Description = "workspace with primary and linked worktree contexts"
	base.WorkspaceSnapshot.Workspaces = []workspace.WorkspaceSnapshot{{
		ID:          "monorepo",
		DisplayName: "monorepo",
		Urgency:     workspace.UrgencyActive,
		Worktrees: []workspace.WorktreeContext{
			{ID: "primary", WorkspaceID: "monorepo", CanonicalPath: "/work/monorepo", IsPrimary: true, Branch: "main"},
			{ID: "feature", WorkspaceID: "monorepo", CanonicalPath: "/work/monorepo-feature", Branch: "feature/workspace"},
		},
		LiveSessions: []workspace.LiveSession{
			{Session: session.RunningSession{TabID: 9, WindowID: 19, Base: "feature", Project: "monorepo", Profile: "work", Account: "work", State: "bolt"}, WorktreePath: "/work/monorepo-feature"},
		},
	}}
	return base
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
