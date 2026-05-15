package session

import (
	"strings"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

// RunningSession is a currently open managed harness tab in Kitty.
type RunningSession struct {
	TabID     int
	WindowID  int
	Key       string
	Title     string   // raw Kitty tab title (may include icons)
	Base      string   // title with all icons stripped (e.g., "W:api")
	State     string   // "bolt", "stop", "key", "error", or ""
	Starred   bool
	Project   string   // configured project title
	Profile   string   // from amx_profile / legacy cc_account user vars
	Account   string   // compatibility alias for Profile
	Harness   string   // from amx_harness or inferred from legacy vars
	HomeDir   string   // from amx_home_dir / legacy cc_config_dir
	Directory string   // from amx_directory / legacy cc_cwd user var, or first window's CWD
	Args      string   // from amx_args / legacy cc_flags
	Flags     string   // compatibility alias for Args
	ExplicitFlags    string   // from cc_explicit_flags; empty when inherited or legacy
	ExplicitFlagsSet bool     // true when cc_explicit_flags was present in Kitty user vars
	Env       string   // from amx_env / legacy cc_env (raw serialized)
	Groups    []string // decoded from cc_groups user var (\x1f-delimited)
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
	return projectName(rs.Base)
}

func (rs RunningSession) EffectiveArgs() string {
	if rs.Args != "" {
		return rs.Args
	}
	return rs.Flags
}

// List returns all managed running sessions from Kitty.
// A tab is "managed" if any of its windows has cc_account set (non-empty).
func List(client kitty.KittyClient) ([]RunningSession, error) {
	osWindows, err := client.ListWindows()
	if err != nil {
		return nil, err
	}
	var sessions []RunningSession
	for _, osw := range osWindows {
		for _, tab := range osw.Tabs {
			if rs, ok := tabToRunningSession(tab); ok {
				sessions = append(sessions, rs)
			}
		}
	}
	return sessions, nil
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

func tabToRunningSession(tab kitty.Tab) (RunningSession, bool) {
	for _, win := range tab.Windows {
		profile := win.UserVars.EffectiveProfile()
		if profile == "" {
			continue
		}
		base, state, starred := palette.ParseTitle(tab.Title)
		dir := win.UserVars.EffectiveDirectory()
		if dir == "" {
			dir = win.CWD
		}
		project := projectName(base)
		if uvProject := win.UserVars.EffectiveProject(project); uvProject != "" {
			project = uvProject
		}
		args := win.UserVars.EffectiveArgs()
		return RunningSession{
			TabID:     tab.ID,
			WindowID:  win.ID,
			Key:       SessionKey(profile, project),
			Title:     tab.Title,
			Base:      base,
			State:     state,
			Starred:   starred,
			Project:   project,
			Profile:   profile,
			Account:   profile,
			Harness:   win.UserVars.EffectiveHarness(),
			HomeDir:   win.UserVars.EffectiveHomeDir(),
			Directory: dir,
			Args:      args,
			Flags:     args,
			ExplicitFlags:    win.UserVars.ExplicitFlags,
			ExplicitFlagsSet: win.UserVars.ExplicitFlagsSet,
			Env:       win.UserVars.EffectiveEnv(),
			Groups:    decodeGroups(win.UserVars.EffectiveGroups()),
		}, true
	}
	return RunningSession{}, false
}

// SessionKey returns the logical identity of a configured project under a
// profile. It is used for dedup and launch-time running detection.
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
