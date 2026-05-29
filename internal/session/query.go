package session

import (
	"log/slog"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// RunningSession is a currently open managed harness tab in Kitty.
type RunningSession struct {
	TabID            int
	WindowID         int
	Key              string
	Title            string // raw Kitty tab title (may include icons)
	Base             string // title with all icons stripped (e.g., "api"; older tabs may still be "W:api")
	State            string // "bolt", "stop", "key", "warning", "error", "subagent", "compact", "question", or ""
	Starred          bool
	Locked           bool
	Muted            bool
	Project          string   // configured project title
	Profile          string   // from amx_profile / legacy cc_account user vars
	Account          string   // compatibility alias for Profile
	Harness          string   // from amx_harness or inferred from legacy vars
	HomeDir          string   // from amx_home_dir / legacy cc_config_dir
	Directory        string   // from amx_directory / legacy cc_cwd user var, or the selected managed window's CWD
	Args             string   // from amx_args / legacy cc_flags (shell-quoted)
	Flags            string   // compatibility alias for Args
	ExplicitFlags    string   // from cc_explicit_flags (shell-quoted); empty when inherited or legacy
	ExplicitFlagsSet bool     // true when cc_explicit_flags was present in Kitty user vars
	Env              string   // from amx_env / legacy cc_env (raw serialized)
	Groups           []string // decoded from cc_groups user var (\x1f-delimited)
}

func (rs RunningSession) EffectiveProfile() string {
	if rs.Profile != "" {
		return rs.Profile
	}
	return rs.Account
}

func (rs RunningSession) EffectiveProject() string {
	if rs.Project != "" {
		return rs.Project
	}
	return projectNameForProfile(rs.Base, rs.EffectiveProfile())
}

func (rs RunningSession) EffectiveArgs() string {
	if rs.Args != "" {
		return rs.Args
	}
	return rs.Flags
}

// GhostTab is a managed tab whose anchor window (the one bearing amx user vars)
// no longer exists, but whose tab title still matches a unique configured
// project. Callers can use the WindowID to re-stamp user vars onto the
// surviving window and restore normal detection.
type GhostTab struct {
	TabID    int
	WindowID int
	Profile  string
	Project  string
	Account  config.Account
	Session  config.Session
}

type titleFallbackMatch struct {
	acc  config.Account
	sess config.Session
}

// List returns all managed running sessions from Kitty.
// A tab is "managed" if any of its windows has cc_account set (non-empty).
func List(client kitty.KittyClient) ([]RunningSession, error) {
	sessions, _, err := ListWithConfig(client, nil)
	return sessions, err
}

// ListFromOSWindows returns all managed running sessions from an already-fetched
// kitten-@-ls payload. This avoids a second Kitty roundtrip for callers that
// already have the raw window list and only need session reconstruction.
func ListFromOSWindows(osWindows []kitty.OSWindow) []RunningSession {
	sessions, _ := runningSessionsFromOSWindows(osWindows, nil)
	return sessions
}

// ListWithConfig returns running sessions plus any "ghost" tabs whose anchor
// window is gone but whose tab title resolves unambiguously to a configured
// project title. Legacy "<initial>:<project>" ghost titles are still supported
// as a fallback when no exact project-title match exists. When cfg is nil, no
// fallback occurs and ghosts is always empty — behavior is identical to List.
func ListWithConfig(client kitty.KittyClient, cfg *config.Config) (sessions []RunningSession, ghosts []GhostTab, err error) {
	osWindows, err := client.ListWindows()
	if err != nil {
		return nil, nil, err
	}
	sessions, ghosts = runningSessionsFromOSWindows(osWindows, cfg)
	return sessions, ghosts, nil
}

// Find returns the first running session whose Base title equals baseTitle.
func Find(sessions []RunningSession, baseTitle string) (RunningSession, bool) {
	for _, s := range sessions {
		if s.Base == baseTitle {
			return s, true
		}
	}
	return RunningSession{}, false
}

// FindByKey returns the first running session whose logical key equals key.
func FindByKey(sessions []RunningSession, key string) (RunningSession, bool) {
	for _, s := range sessions {
		if s.Key == key {
			return s, true
		}
	}
	return RunningSession{}, false
}

// FindByWindowID returns the running session anchored to windowID.
func FindByWindowID(sessions []RunningSession, windowID int) (RunningSession, bool) {
	for _, s := range sessions {
		if s.WindowID == windowID {
			return s, true
		}
	}
	return RunningSession{}, false
}

func tabToRunningSession(tab kitty.Tab, cfg *config.Config) (rs RunningSession, ghost *GhostTab, ok bool) {
	var fallback *kitty.Window
	for i := range tab.Windows {
		win := &tab.Windows[i]
		if win.UserVars.EffectiveProfile() == "" {
			continue
		}
		if win.IsActive {
			return runningSessionFromWindow(tab, *win), nil, true
		}
		if fallback == nil {
			fallback = win
		}
	}
	if fallback != nil {
		return runningSessionFromWindow(tab, *fallback), nil, true
	}
	if cfg != nil {
		if rs, ghost, ok := titleFallback(tab, cfg); ok {
			return rs, ghost, true
		}
	}
	return RunningSession{}, nil, false
}

// titleFallback recognizes a tab by its title even when no window carries amx
// user vars (the anchor harness window was closed). It returns a fabricated
// RunningSession and a GhostTab pointing at the window that should be
// re-stamped. The match must be unique — ambiguous or unconfigured titles are
// skipped. Exact project-title matches are preferred; legacy
// "<initial>:<project>" titles are only considered when no exact match exists.
func titleFallback(tab kitty.Tab, cfg *config.Config) (RunningSession, *GhostTab, bool) {
	if len(tab.Windows) == 0 {
		return RunningSession{}, nil, false
	}
	base, state, starred, muted := palette.ParseTitleFull(tab.Title)

	matches, strategy := titleFallbackMatches(cfg, base)
	if len(matches) != 1 {
		slog.Debug("title fallback skipped",
			"tab_id", tab.ID,
			"title", tab.Title,
			"base", base,
			"strategy", strategy,
			"matches", len(matches),
		)
		return RunningSession{}, nil, false
	}
	m := matches[0]

	winID := tab.Windows[0].ID
	for i := range tab.Windows {
		if tab.Windows[i].IsActive {
			winID = tab.Windows[i].ID
			break
		}
	}

	profile := m.sess.EffectiveProfile()
	if profile == "" {
		profile = m.acc.Name
	}
	homeDir := config.ExpandTilde(m.acc.EffectiveHomeDir())
	projectDir := config.ExpandTilde(m.sess.Directory)
	args := ResolveArgs(m.sess, m.acc, cfg.Defaults)
	explicitArgs := shellQuoteArgs(m.sess.EffectiveArgs())
	ccEnv := SerializeSessionEnv(m.sess.Env)
	groups := append([]string(nil), m.sess.Groups...)

	rs := RunningSession{
		TabID:            tab.ID,
		WindowID:         winID,
		Key:              SessionKey(profile, m.sess.Title),
		Title:            tab.Title,
		Base:             base,
		State:            state,
		Starred:          starred,
		Muted:            muted,
		Project:          m.sess.Title,
		Profile:          profile,
		Account:          profile,
		Harness:          m.acc.EffectiveHarness(),
		HomeDir:          homeDir,
		Directory:        projectDir,
		Args:             args,
		Flags:            args,
		ExplicitFlags:    explicitArgs,
		ExplicitFlagsSet: len(m.sess.EffectiveArgs()) > 0,
		Env:              ccEnv,
		Groups:           groups,
	}
	ghost := &GhostTab{
		TabID:    tab.ID,
		WindowID: winID,
		Profile:  profile,
		Project:  m.sess.Title,
		Account:  m.acc,
		Session:  m.sess,
	}
	return rs, ghost, true
}

func runningSessionFromWindow(tab kitty.Tab, win kitty.Window) RunningSession {
	profile := win.UserVars.EffectiveProfile()
	base, state, starred, muted := palette.ParseTitleFull(tab.Title)
	dir := win.UserVars.EffectiveDirectory()
	if dir == "" {
		dir = win.CWD
	}
	project := win.UserVars.EffectiveProject(base)
	if win.UserVars.Project == "" {
		project = projectNameForProfile(project, profile)
	}
	args := win.UserVars.EffectiveArgs()

	return RunningSession{
		TabID:            tab.ID,
		WindowID:         win.ID,
		Key:              SessionKey(profile, project),
		Title:            tab.Title,
		Base:             base,
		State:            state,
		Starred:          starred,
		Locked:           win.UserVars.Locked,
		Muted:            muted || win.UserVars.Mute != "",
		Project:          project,
		Profile:          profile,
		Account:          profile,
		Harness:          win.UserVars.EffectiveHarness(),
		HomeDir:          win.UserVars.EffectiveHomeDir(),
		Directory:        dir,
		Args:             args,
		Flags:            args,
		ExplicitFlags:    win.UserVars.ExplicitFlags,
		ExplicitFlagsSet: win.UserVars.ExplicitFlagsSet,
		Env:              win.UserVars.EffectiveEnv(),
		Groups:           decodeGroups(win.UserVars.EffectiveGroups()),
	}
}

func runningSessionsFromOSWindows(osWindows []kitty.OSWindow, cfg *config.Config) ([]RunningSession, []GhostTab) {
	var sessions []RunningSession
	var ghosts []GhostTab
	for _, osw := range osWindows {
		s, g := runningSessionsFromTabs(osw.Tabs, cfg)
		sessions = append(sessions, s...)
		ghosts = append(ghosts, g...)
	}
	return sessions, ghosts
}

func runningSessionsFromTabs(tabs []kitty.Tab, cfg *config.Config) ([]RunningSession, []GhostTab) {
	sessions := make([]RunningSession, 0, len(tabs))
	var ghosts []GhostTab
	for _, tab := range tabs {
		rs, ghost, ok := tabToRunningSession(tab, cfg)
		if !ok {
			continue
		}
		sessions = append(sessions, rs)
		if ghost != nil {
			ghosts = append(ghosts, *ghost)
		}
	}
	return sessions, ghosts
}

func titleFallbackMatches(cfg *config.Config, base string) ([]titleFallbackMatch, string) {
	exact := configuredTitleMatches(cfg, func(acc config.Account, sess config.Session) bool {
		return strings.EqualFold(sess.EffectiveProfile(), acc.Name) && sess.Title == base
	})
	if len(exact) > 0 {
		return exact, "exact-title"
	}
	legacy := configuredTitleMatches(cfg, func(acc config.Account, sess config.Session) bool {
		project, ok := legacyProjectName(base, acc.Name)
		return ok && strings.EqualFold(sess.EffectiveProfile(), acc.Name) && sess.Title == project
	})
	return legacy, "legacy-prefix"
}

func configuredTitleMatches(cfg *config.Config, matchFn func(config.Account, config.Session) bool) []titleFallbackMatch {
	var matches []titleFallbackMatch
	for _, acc := range cfg.Accounts {
		for _, sess := range cfg.Sessions {
			if matchFn(acc, sess) {
				matches = append(matches, titleFallbackMatch{acc: acc, sess: sess})
			}
		}
	}
	return matches
}

func targetOSWindow(osWindows []kitty.OSWindow) *kitty.OSWindow {
	for _, match := range []func(kitty.OSWindow) bool{
		func(osw kitty.OSWindow) bool { return osw.IsFocused },
		func(osw kitty.OSWindow) bool { return osw.IsActive },
		func(osw kitty.OSWindow) bool { return osw.LastFocused },
	} {
		for i := range osWindows {
			if match(osWindows[i]) {
				return &osWindows[i]
			}
		}
	}
	if len(osWindows) == 0 {
		return nil
	}
	return &osWindows[0]
}

func focusedTabID(osWindows []kitty.OSWindow) int {
	if osw := targetOSWindow(osWindows); osw != nil {
		for _, tab := range osw.Tabs {
			if tab.IsActive {
				return tab.ID
			}
		}
	}
	for _, osw := range osWindows {
		for _, tab := range osw.Tabs {
			if tab.IsActive {
				return tab.ID
			}
		}
	}
	return 0
}

// SessionKey builds the dedup key for a running session. The separator (\x1f,
// ASCII Unit Separator) is chosen because it cannot appear in Kitty user var
// values (Kitty strips control characters from --var values).
func SessionKey(profileName, projectTitle string) string {
	return strings.ToLower(profileName) + "\x1f" + strings.ToLower(projectTitle)
}

func decodeGroups(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, "\x1f")
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
