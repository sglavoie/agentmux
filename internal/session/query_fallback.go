package session

import (
	"log/slog"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func tabToRunningSession(tab kitty.Tab, ctx projectMatchContext) (rs RunningSession, ghost *GhostTab, ok bool) {
	var fallback *kitty.Window
	for i := range tab.Windows {
		win := &tab.Windows[i]
		if win.UserVars.EffectiveProfile() == "" {
			continue
		}
		// Config-aware discovery can encounter metadata that a previous ghost
		// recovery stamped onto an overlay or ordinary shell. Modern launches
		// carry a launch ID and remain authoritative through brief Kitty
		// process-list gaps. Recovered and legacy anchors do not, so require
		// live harness evidence before treating them as running.
		if ctx.cfg != nil && win.UserVars.LaunchID == "" && !windowHasHarnessProcess(*win) {
			continue
		}
		if win.IsActive {
			if ctx.cfg != nil {
				if rs, ghost, ok := managedWindowOverride(tab, *win, ctx); ok {
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
		if ctx.cfg != nil {
			if rs, ghost, ok := managedWindowOverride(tab, *fallback, ctx); ok {
				return rs, ghost, true
			}
		}
		return runningSessionFromWindow(tab, *fallback), nil, true
	}
	if ctx.cfg != nil {
		if rs, ghost, ok := titleFallback(tab, ctx.cfg); ok {
			return rs, ghost, true
		}
		if rs, ghost, ok := cwdFallback(tab, ctx); ok {
			return rs, ghost, true
		}
	}
	return RunningSession{}, nil, false
}

// titleFallback recognizes a tab by its title even when no window carries amx
// user vars (the anchor harness window was closed), provided another live
// harness window survives. It returns a fabricated RunningSession and a
// GhostTab pointing at that window. The match must be unique — ambiguous or
// unconfigured titles are skipped. Exact project-title matches are preferred;
// legacy "<initial>:<project>" titles are only considered when no exact match
// exists.
func titleFallback(tab kitty.Tab, cfg *config.Config) (RunningSession, *GhostTab, bool) {
	if len(tab.Windows) == 0 {
		return RunningSession{}, nil, false
	}
	candidates := fallbackWindowsByPriority(tab)
	if len(candidates) == 0 {
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

	// Re-anchor the actual harness pane, never an active dashboard overlay or
	// unrelated shell that merely shares the tab.
	return fallbackSessionFromMatch(tab, candidates[0].ID, m, matchBase, state, starred, muted, cfg)
}

// managedWindowOverride repairs a window that still has amx user vars, but
// where those vars no longer describe the live harness session in that window.
// This happens when a user reuses a Kitty window for a new Codex/Claude session:
// Kitty can keep the old user vars and even an old window CWD, while the
// foreground harness process has moved to the new project.
func managedWindowOverride(tab kitty.Tab, win kitty.Window, ctx projectMatchContext) (RunningSession, *GhostTab, bool) {
	cwd := windowHarnessCWD(win)
	if cwd != "" {
		matches := cwdFallbackMatches(ctx, cwd)
		if len(matches) != 1 {
			return RunningSession{}, nil, false
		}
		match := matches[0]
		if windowMatchesConfigSession(win, match) {
			return RunningSession{}, nil, false
		}

		_, state, starred, muted := palette.ParseTitleFull(tab.Title)
		return fallbackSessionFromMatch(tab, win.ID, match, match.sess.Title, state, starred, muted, ctx.cfg)
	}

	if match, base, state, starred, muted, ok := matchTabTitleFallback(tab, ctx.cfg); ok {
		if !windowMatchesConfigSession(win, match) {
			return fallbackSessionFromMatch(tab, win.ID, match, base, state, starred, muted, ctx.cfg)
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

// cwdFallback recognizes a tab by the working directory of a live harness
// window when no window carries amx user vars and the title no longer
// identifies the configured project. The CWD must resolve to exactly one
// configured project.
func cwdFallback(tab kitty.Tab, ctx projectMatchContext) (RunningSession, *GhostTab, bool) {
	if len(tab.Windows) == 0 {
		return RunningSession{}, nil, false
	}

	for _, win := range fallbackWindowsByPriority(tab) {
		cwd := windowGhostCWD(win)
		if strings.TrimSpace(cwd) == "" {
			continue
		}
		matches := cwdFallbackMatches(ctx, cwd)
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
		return fallbackSessionFromMatch(tab, win.ID, m, m.sess.Title, state, starred, muted, ctx.cfg)
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
	appendMatching(func(win kitty.Window) bool { return win.IsActive && windowHasHarnessProcess(win) })
	appendMatching(windowHasHarnessProcess)
	return windows
}

func windowMatchesConfigSession(win kitty.Window, m titleFallbackMatch) bool {
	// The profile stored on a managed window is the runtime launch identity.
	// It may legitimately differ from the configured project's default after a
	// one-time launch override or dashboard profile switch, so profile drift is
	// not evidence that the window was reused for another project.
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
		Looping:          palette.ParseTitleParts(tab.Title).Looping,
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
