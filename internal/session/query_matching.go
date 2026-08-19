package session

import (
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/harness"
	"github.com/sglavoie/agentmux/internal/kitty"
)

func cwdFallbackMatches(ctx projectMatchContext, cwd string) []titleFallbackMatch {
	if ctx.cfg == nil {
		return nil
	}
	projectMatches := ctx.resolver.Matches(cwd)
	if len(projectMatches) == 0 {
		return nil
	}

	accounts := configuredAccounts(ctx.cfg)
	matches := make([]titleFallbackMatch, 0, len(projectMatches))
	for _, projectMatch := range projectMatches {
		sess := projectMatch.Project
		for _, acc := range accounts {
			if strings.EqualFold(sess.EffectiveProfile(), acc.Name) {
				matches = append(matches, titleFallbackMatch{acc: acc, sess: sess})
			}
		}
	}
	return matches
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

// windowGhostCWD deliberately ignores unrelated foreground processes. Ghost
// recovery is allowed only for a window that already has live harness
// evidence, so the harness CWD (or that Kitty window's own CWD when the process
// omits one) is the only safe project identity.
func windowGhostCWD(win kitty.Window) string {
	if cwd := windowHarnessCWD(win); cwd != "" {
		return cwd
	}
	return strings.TrimSpace(win.CWD)
}

func windowHasHarnessProcess(win kitty.Window) bool {
	for _, fp := range win.ForegroundProcesses {
		if cmdlineContainsHarness(fp.Cmdline) {
			return true
		}
	}
	return false
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
	for _, acc := range configuredAccounts(cfg) {
		for _, sess := range configuredProjects(cfg) {
			if matchFn(acc, sess) {
				matches = append(matches, titleFallbackMatch{acc: acc, sess: sess})
			}
		}
	}
	return matches
}

func configuredAccounts(cfg *config.Config) []config.Account {
	if cfg == nil {
		return nil
	}
	if len(cfg.Accounts) > 0 {
		return cfg.Accounts
	}
	return cfg.Profiles
}

func configuredProjects(cfg *config.Config) []config.Session {
	if cfg == nil {
		return nil
	}
	if len(cfg.Sessions) > 0 {
		return cfg.Sessions
	}
	return cfg.Projects
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
