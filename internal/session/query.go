package session

import (
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
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

// TitleParts returns the title components for rebuilds that need to preserve
// markers not stored as first-class RunningSession fields yet.
func (rs RunningSession) TitleParts() palette.TitleParts {
	parts := palette.TitleParts{
		Base:    rs.Base,
		State:   rs.State,
		Starred: rs.Starred,
		Muted:   rs.Muted,
	}
	if rs.Title == "" {
		return parts
	}

	parsed := palette.ParseTitleParts(rs.Title)
	if parts.Base == "" {
		parts.Base = parsed.Base
	}
	if parts.State == "" {
		parts.State = parsed.State
	}
	parts.Starred = parts.Starred || parsed.Starred
	parts.Muted = parts.Muted || parsed.Muted
	parts.Looping = parsed.Looping
	return parts
}

// GhostTab is a managed tab whose anchor window (the one bearing amx user vars)
// no longer exists, but whose tab still matches a unique configured project by
// title or working directory. Callers can use the WindowID to re-stamp user
// vars onto the surviving window and restore normal detection.
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
// window is gone but whose tab resolves unambiguously to a configured project.
// Title matching is tried first, including legacy "<initial>:<project>"
// titles. If that fails, the active window's CWD can recover a tab whose title
// no longer identifies the project. When cfg is nil, no fallback occurs and
// ghosts is always empty — behavior is identical to List.
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
			if cfg != nil {
				if rs, ghost, ok := managedWindowOverride(tab, *win, cfg); ok {
					return rs, ghost, true
				}
			}
			return runningSessionFromWindow(tab, *win), nil, true
		}
		if fallback == nil {
			fallback = win
		}
	}
	if fallback != nil {
		if cfg != nil {
			if rs, ghost, ok := managedWindowOverride(tab, *fallback, cfg); ok {
				return rs, ghost, true
			}
		}
		return runningSessionFromWindow(tab, *fallback), nil, true
	}
	if cfg != nil {
		if rs, ghost, ok := titleFallback(tab, cfg); ok {
			return rs, ghost, true
		}
		if rs, ghost, ok := cwdFallback(tab, cfg); ok {
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

	matches, strategy, matchBase := titleFallbackMatches(cfg, base)
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

	return fallbackSessionFromMatch(tab, winID, m, matchBase, state, starred, muted, cfg)
}

// managedWindowOverride repairs a window that still has amx user vars, but
// where those vars no longer describe the live harness session in that window.
// This happens when a user reuses a Kitty window for a new Codex/Claude session:
// Kitty can keep the old user vars and even an old window CWD, while the
// foreground harness process has moved to the new project.
func managedWindowOverride(tab kitty.Tab, win kitty.Window, cfg *config.Config) (RunningSession, *GhostTab, bool) {
	cwd := windowHarnessCWD(win)
	if cwd != "" {
		matches := cwdFallbackMatches(cfg, cwd)
		if len(matches) != 1 {
			return RunningSession{}, nil, false
		}
		match := matches[0]
		if windowMatchesConfigSession(win, match) {
			return RunningSession{}, nil, false
		}

		_, state, starred, muted := palette.ParseTitleFull(tab.Title)
		return fallbackSessionFromMatch(tab, win.ID, match, match.sess.Title, state, starred, muted, cfg)
	}

	if match, base, state, starred, muted, ok := matchTabTitleFallback(tab, cfg); ok {
		if !windowMatchesConfigSession(win, match) {
			return fallbackSessionFromMatch(tab, win.ID, match, base, state, starred, muted, cfg)
		}
	}
	return RunningSession{}, nil, false
}

func matchTabTitleFallback(tab kitty.Tab, cfg *config.Config) (titleFallbackMatch, string, string, bool, bool, bool) {
	base, state, starred, muted := palette.ParseTitleFull(tab.Title)
	matches, _, matchBase := titleFallbackMatches(cfg, base)
	if len(matches) != 1 {
		return titleFallbackMatch{}, "", "", false, false, false
	}
	return matches[0], matchBase, state, starred, muted, true
}

// cwdFallback recognizes a tab by the working directory of its active window
// when no window carries amx user vars and the title no longer identifies the
// configured project. The CWD must resolve to exactly one configured project.
func cwdFallback(tab kitty.Tab, cfg *config.Config) (RunningSession, *GhostTab, bool) {
	if len(tab.Windows) == 0 {
		return RunningSession{}, nil, false
	}

	for _, win := range fallbackWindowsByPriority(tab) {
		cwd := windowFallbackCWD(win)
		if strings.TrimSpace(cwd) == "" {
			continue
		}
		matches := cwdFallbackMatches(cfg, cwd)
		if len(matches) != 1 {
			slog.Debug("cwd fallback skipped",
				"tab_id", tab.ID,
				"window_id", win.ID,
				"title", tab.Title,
				"cwd", cwd,
				"matches", len(matches),
			)
			if len(matches) > 1 {
				return RunningSession{}, nil, false
			}
			continue
		}

		_, state, starred, muted := palette.ParseTitleFull(tab.Title)
		m := matches[0]
		return fallbackSessionFromMatch(tab, win.ID, m, m.sess.Title, state, starred, muted, cfg)
	}

	return RunningSession{}, nil, false
}

func fallbackWindowsByPriority(tab kitty.Tab) []kitty.Window {
	windows := make([]kitty.Window, 0, len(tab.Windows))
	seen := make(map[int]bool, len(tab.Windows))
	appendMatching := func(matchFn func(kitty.Window) bool) {
		for _, win := range tab.Windows {
			if seen[win.ID] || !matchFn(win) {
				continue
			}
			windows = append(windows, win)
			seen[win.ID] = true
		}
	}
	appendMatching(func(win kitty.Window) bool { return win.IsActive && windowHarnessCWD(win) != "" })
	appendMatching(func(win kitty.Window) bool { return windowHarnessCWD(win) != "" })
	appendMatching(func(win kitty.Window) bool { return win.IsActive })
	appendMatching(func(kitty.Window) bool { return true })
	return windows
}

func windowMatchesConfigSession(win kitty.Window, m titleFallbackMatch) bool {
	profile := m.sess.EffectiveProfile()
	if profile == "" {
		profile = m.acc.Name
	}
	if current := win.UserVars.EffectiveProfile(); current != "" && !strings.EqualFold(current, profile) {
		return false
	}
	if win.UserVars.Project != "" && win.UserVars.Project != m.sess.Title {
		return false
	}
	if dir := win.UserVars.EffectiveDirectory(); dir != "" {
		expected := config.ExpandTilde(m.sess.Directory)
		currentDir, okCurrent := canonicalFallbackDir(dir)
		expectedDir, okExpected := canonicalFallbackDir(expected)
		if okCurrent && okExpected && currentDir != expectedDir {
			return false
		}
	}
	return true
}

func fallbackSessionFromMatch(tab kitty.Tab, winID int, m titleFallbackMatch, base, state string, starred, muted bool, cfg *config.Config) (RunningSession, *GhostTab, bool) {
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
		dir = windowFallbackCWD(win)
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

func titleFallbackMatches(cfg *config.Config, base string) ([]titleFallbackMatch, string, string) {
	bases := titleFallbackBases(cfg, base)
	for _, candidate := range bases {
		exact := configuredSessionMatches(cfg, func(acc config.Account, sess config.Session) bool {
			return strings.EqualFold(sess.EffectiveProfile(), acc.Name) && sess.Title == candidate
		})
		if len(exact) > 0 {
			return exact, "exact-title", candidate
		}
	}
	for _, candidate := range bases {
		legacy := configuredSessionMatches(cfg, func(acc config.Account, sess config.Session) bool {
			project, ok := legacyProjectName(candidate, acc.Name)
			return ok && strings.EqualFold(sess.EffectiveProfile(), acc.Name) && sess.Title == project
		})
		if len(legacy) > 0 {
			return legacy, "legacy-prefix", candidate
		}
	}
	return nil, "legacy-prefix", base
}

func titleFallbackBases(cfg *config.Config, base string) []string {
	bases := []string{base}
	seen := map[string]bool{base: true}
	if cfg == nil {
		return bases
	}
	for _, emoji := range cfg.HarnessEmojis {
		if emoji == "" || !strings.HasPrefix(base, emoji) {
			continue
		}
		trimmed := strings.TrimPrefix(base, emoji)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		bases = append(bases, trimmed)
		seen[trimmed] = true
	}
	return bases
}

func cwdFallbackMatches(cfg *config.Config, cwd string) []titleFallbackMatch {
	target, ok := canonicalFallbackDir(cwd)
	if !ok {
		return nil
	}
	return configuredSessionMatches(cfg, func(acc config.Account, sess config.Session) bool {
		if !strings.EqualFold(sess.EffectiveProfile(), acc.Name) {
			return false
		}
		dir, ok := canonicalFallbackDir(sess.Directory)
		return ok && dir == target
	})
}

func canonicalFallbackDir(path string) (string, bool) {
	path = strings.TrimSpace(config.ExpandTilde(path))
	if path == "" {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path), true
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return filepath.Clean(abs), true
}

func windowFallbackCWD(win kitty.Window) string {
	if cwd := windowHarnessCWD(win); cwd != "" {
		return cwd
	}
	for _, fp := range win.ForegroundProcesses {
		if cwd := strings.TrimSpace(fp.CWD); cwd != "" {
			return cwd
		}
	}
	return win.CWD
}

func windowHarnessCWD(win kitty.Window) string {
	for _, fp := range win.ForegroundProcesses {
		if !cmdlineContainsHarness(fp.Cmdline) {
			continue
		}
		if cwd := strings.TrimSpace(fp.CWD); cwd != "" {
			return cwd
		}
	}
	return ""
}

func cmdlineContainsHarness(cmdline []string) bool {
	for _, arg := range cmdline {
		base := strings.TrimSpace(filepath.Base(strings.Trim(arg, `"'`)))
		base = strings.TrimSuffix(base, ".exe")
		if base == "" {
			continue
		}
		if harness.Valid(base) {
			return true
		}
	}
	return false
}

func configuredSessionMatches(cfg *config.Config, matchFn func(config.Account, config.Session) bool) []titleFallbackMatch {
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
