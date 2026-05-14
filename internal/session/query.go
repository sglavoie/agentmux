package session

import (
	"strings"

	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
	"github.com/sglavoie/cc-manager-kitty/internal/palette"
)

// RunningSession is a currently open Claude session tab in Kitty.
type RunningSession struct {
	TabID     int
	WindowID  int
	Title     string   // raw Kitty tab title (may include icons)
	Base      string   // title with all icons stripped (e.g., "W:api")
	State     string   // "bolt", "stop", "key", "error", or ""
	Starred   bool
	Account   string   // from cc_account user var
	Directory string   // from cc_cwd user var, or first window's CWD
	Flags     string   // from cc_flags user var
	Env       string   // from cc_env user var (raw serialized)
	Groups    []string // decoded from cc_groups user var (\x1f-delimited)
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

func tabToRunningSession(tab kitty.Tab) (RunningSession, bool) {
	for _, win := range tab.Windows {
		if win.UserVars.Account == "" {
			continue
		}
		base, state, starred := palette.ParseTitle(tab.Title)
		dir := win.UserVars.CWD
		if dir == "" {
			dir = win.CWD
		}
		return RunningSession{
			TabID:     tab.ID,
			WindowID:  win.ID,
			Title:     tab.Title,
			Base:      base,
			State:     state,
			Starred:   starred,
			Account:   win.UserVars.Account,
			Directory: dir,
			Flags:     win.UserVars.Flags,
			Env:       win.UserVars.Env,
			Groups:    decodeGroups(win.UserVars.Groups),
		}, true
	}
	return RunningSession{}, false
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
