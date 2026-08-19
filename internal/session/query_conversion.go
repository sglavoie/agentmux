package session

import (
	"sort"
	"strings"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func runningSessionFromWindow(tab kitty.Tab, win kitty.Window) RunningSession {
	profile := win.UserVars.EffectiveProfile()
	parts := palette.ParseTitleParts(tab.Title)
	base, state, starred, muted := parts.Base, parts.State, parts.Starred, parts.Muted
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
		Looping:          parts.Looping,
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
		LaunchID:         win.UserVars.LaunchID,
		NativeCoverage:   win.UserVars.NativeCoverage,
		NativeLogPath:    win.UserVars.NativeLogPath,
		NativeLogLevel:   win.UserVars.NativeLogLevel,
		NativeOverride:   win.UserVars.NativeOverride,
		HarnessSessionID: win.UserVars.HarnessSessionID,
		ASLRunID:         win.UserVars.ASLRunID,
	}
}

func runningSessionsFromOSWindows(osWindows []kitty.OSWindow, cfg *config.Config) ([]RunningSession, []GhostTab) {
	ctx := newProjectMatchContext(cfg)
	var sessions []RunningSession
	var ghosts []GhostTab
	for _, osw := range osWindows {
		s, g := runningSessionsFromTabs(osw.Tabs, ctx)
		sessions = append(sessions, s...)
		ghosts = append(ghosts, g...)
	}
	return sessions, ghosts
}

func runningSessionsFromTabs(tabs []kitty.Tab, ctx projectMatchContext) ([]RunningSession, []GhostTab) {
	sessions := make([]RunningSession, 0, len(tabs))
	var ghosts []GhostTab
	for i, tab := range tabs {
		rs, ghost, ok := tabToRunningSession(tab, ctx)
		if !ok {
			continue
		}
		// Kitty's user-facing tab number is the tab's 1-based physical
		// position within its OS window. Assign it before managed tabs are
		// filtered so unmanaged tabs still occupy their visible positions.
		rs.TabNumber = i + 1
		sessions = append(sessions, rs)
		if ghost != nil {
			ghosts = append(ghosts, *ghost)
		}
	}
	return sessions, ghosts
}

// tabNumberByID returns the 1-based physical position of tabID within one
// Kitty OS window. A zero result means the tab was not present.
func tabNumberByID(tabs []kitty.Tab, tabID int) int {
	for i, tab := range tabs {
		if tab.ID == tabID {
			return i + 1
		}
	}
	return 0
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
	// Longest emoji first (ties broken lexicographically) so overlapping
	// prefixes yield the same candidate order on every run — map iteration
	// order must not decide which configured session a ghost tab resolves to.
	emojis := make([]string, 0, len(cfg.HarnessEmojis))
	for _, emoji := range cfg.HarnessEmojis {
		emojis = append(emojis, emoji)
	}
	sort.Slice(emojis, func(i, j int) bool {
		if len(emojis[i]) != len(emojis[j]) {
			return len(emojis[i]) > len(emojis[j])
		}
		return emojis[i] < emojis[j]
	})
	for _, emoji := range emojis {
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
