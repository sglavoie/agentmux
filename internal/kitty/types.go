package kitty

import (
	"path/filepath"
	"strings"
)

// OSWindow is the top-level object returned by kitten @ ls.
type OSWindow struct {
	ID              int    `json:"id"`
	IsActive        bool   `json:"is_active"`
	IsFocused       bool   `json:"is_focused"`
	LastFocused     bool   `json:"last_focused"`
	Tabs            []Tab  `json:"tabs"`
}

// Tab is a Kitty tab.
type Tab struct {
	ID       int      `json:"id"`
	IsActive bool     `json:"is_active"`
	Title    string   `json:"title"`
	Windows  []Window `json:"windows"`
}

// Window is a Kitty window (pane) within a tab.
type Window struct {
	ID       int      `json:"id"`
	IsActive bool     `json:"is_active"`
	Title    string   `json:"title"`
	CWD                 string              `json:"cwd"`
	Cmdline             []string            `json:"cmdline"`
	UserVars            UserVars            `json:"user_vars"`
	ForegroundProcesses []ForegroundProcess `json:"foreground_processes"`
}

// ForegroundProcess is a process currently in the foreground of a Kitty window.
type ForegroundProcess struct {
	PID     int      `json:"pid"`
	Cmdline []string `json:"cmdline"`
	CWD     string   `json:"cwd"`
}

// FindTabByWindowID searches all OS windows for the tab containing the window
// with the given ID. Returns the tab, the window, and true if found.
func FindTabByWindowID(osWindows []OSWindow, windowID int) (*Tab, *Window, bool) {
	for i := range osWindows {
		for j := range osWindows[i].Tabs {
			tab := &osWindows[i].Tabs[j]
			for k := range tab.Windows {
				if tab.Windows[k].ID == windowID {
					return tab, &tab.Windows[k], true
				}
			}
		}
	}
	return nil, nil, false
}

// DashboardLocation identifies where an existing dashboard is running.
type DashboardLocation struct {
	TabID    int
	WindowID int
}

// FindDashboardWindow scans all OS windows for a window whose foreground
// process is a ccm dashboard (ccm with no subcommand). excludeWindowID is
// skipped to avoid self-detection (pass the current KITTY_WINDOW_ID).
func FindDashboardWindow(osWindows []OSWindow, excludeWindowID int) (*DashboardLocation, bool) {
	for i := range osWindows {
		for j := range osWindows[i].Tabs {
			tab := &osWindows[i].Tabs[j]
			for k := range tab.Windows {
				w := &tab.Windows[k]
				if w.ID == excludeWindowID {
					continue
				}
				for _, fp := range w.ForegroundProcesses {
					if isDashboardCmdline(fp.Cmdline) {
						return &DashboardLocation{TabID: tab.ID, WindowID: w.ID}, true
					}
				}
			}
		}
	}
	return nil, false
}

func isDashboardCmdline(cmdline []string) bool {
	if len(cmdline) == 0 {
		return false
	}
	base := filepath.Base(cmdline[0])
	if base != "ccm" {
		return false
	}
	for _, arg := range cmdline[1:] {
		if !strings.HasPrefix(arg, "-") {
			return false
		}
	}
	return true
}

// UserVars holds cc-manager user variables stored on a Kitty window.
type UserVars struct {
	Account   string `json:"cc_account"`
	ConfigDir string `json:"cc_config_dir"`
	Flags     string `json:"cc_flags"`
	PW        string `json:"cc_pw"`
	Env       string `json:"cc_env"`
	Groups    string `json:"cc_groups"`
	Mute      string `json:"cc_mute"`
	CWD       string `json:"cc_cwd"`
}
