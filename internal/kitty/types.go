package kitty

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/sglavoie/agentmux/internal/harness"
)

// OSWindow is the top-level object returned by kitten @ ls.
type OSWindow struct {
	ID          int   `json:"id"`
	IsActive    bool  `json:"is_active"`
	IsFocused   bool  `json:"is_focused"`
	LastFocused bool  `json:"last_focused"`
	Tabs        []Tab `json:"tabs"`
}

// Tab is a Kitty tab.
type Tab struct {
	ID             int      `json:"id"`
	IsActive       bool     `json:"is_active"`
	NeedsAttention bool     `json:"needs_attention"`
	Title          string   `json:"title"`
	Windows        []Window `json:"windows"`
}

// Window is a Kitty window (pane) within a tab.
type Window struct {
	ID                  int                 `json:"id"`
	IsActive            bool                `json:"is_active"`
	Title               string              `json:"title"`
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
	_, tab, win, ok := FindWindowLocationByID(osWindows, windowID)
	return tab, win, ok
}

// FindWindowLocationByID searches all OS windows for the window with the given
// ID. Returns the containing OS window, tab, and window if found.
func FindWindowLocationByID(osWindows []OSWindow, windowID int) (*OSWindow, *Tab, *Window, bool) {
	for i := range osWindows {
		for j := range osWindows[i].Tabs {
			tab := &osWindows[i].Tabs[j]
			for k := range tab.Windows {
				if tab.Windows[k].ID == windowID {
					return &osWindows[i], tab, &tab.Windows[k], true
				}
			}
		}
	}
	return nil, nil, nil, false
}

// DashboardLocation identifies where an existing dashboard is running.
type DashboardLocation struct {
	TabID    int
	WindowID int
}

// FindDashboardWindow scans all OS windows for a window whose foreground
// process is an amx dashboard (amx with no subcommand). excludeWindowID is
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
	if base != "amx" {
		return false
	}
	for _, arg := range cmdline[1:] {
		if isInfoOnlyRootFlag(arg) {
			return false
		}
		if !strings.HasPrefix(arg, "-") {
			return false
		}
	}
	return true
}

func isInfoOnlyRootFlag(arg string) bool {
	switch arg {
	case "-h", "--help", "--help=true", "--version", "--version=true":
		return true
	default:
		return false
	}
}

// FindKeybindingsWindow scans all OS windows for a window whose foreground
// process is amx keybindings. excludeWindowID is skipped to avoid
// self-detection (pass the current KITTY_WINDOW_ID).
func FindKeybindingsWindow(osWindows []OSWindow, excludeWindowID int) (*DashboardLocation, bool) {
	for i := range osWindows {
		for j := range osWindows[i].Tabs {
			tab := &osWindows[i].Tabs[j]
			for k := range tab.Windows {
				w := &tab.Windows[k]
				if w.ID == excludeWindowID {
					continue
				}
				for _, fp := range w.ForegroundProcesses {
					if isKeybindingsCmdline(fp.Cmdline) {
						return &DashboardLocation{TabID: tab.ID, WindowID: w.ID}, true
					}
				}
			}
		}
	}
	return nil, false
}

func isKeybindingsCmdline(cmdline []string) bool {
	if len(cmdline) < 2 {
		return false
	}
	if filepath.Base(cmdline[0]) != "amx" {
		return false
	}
	// Tolerate global flags like `--debug` before the subcommand. Without this,
	// `amx --debug keybindings` would be missed by the dedup detector and a
	// second overlay window would be opened.
	for _, arg := range cmdline[1:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg == "keybindings"
	}
	return false
}

// FindColorPickerWindow scans all OS windows for a window whose foreground
// process is amx color-picker. excludeWindowID is skipped to avoid
// self-detection (pass the current KITTY_WINDOW_ID).
func FindColorPickerWindow(osWindows []OSWindow, excludeWindowID int) (*DashboardLocation, bool) {
	for i := range osWindows {
		for j := range osWindows[i].Tabs {
			tab := &osWindows[i].Tabs[j]
			for k := range tab.Windows {
				w := &tab.Windows[k]
				if w.ID == excludeWindowID {
					continue
				}
				for _, fp := range w.ForegroundProcesses {
					if isColorPickerCmdline(fp.Cmdline) {
						return &DashboardLocation{TabID: tab.ID, WindowID: w.ID}, true
					}
				}
			}
		}
	}
	return nil, false
}

func isColorPickerCmdline(cmdline []string) bool {
	if len(cmdline) < 2 {
		return false
	}
	if filepath.Base(cmdline[0]) != "amx" {
		return false
	}
	for _, arg := range cmdline[1:] {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg == "color-picker"
	}
	return false
}

// UserVars holds Agent Mux user variables stored on a Kitty window.
type UserVars struct {
	Profile          string `json:"amx_profile"`
	Harness          string `json:"amx_harness"`
	HomeDir          string `json:"amx_home_dir"`
	Project          string `json:"amx_project"`
	Args             string `json:"amx_args"`
	AMXEnv           string `json:"amx_env"`
	AMXGroups        string `json:"amx_groups"`
	AMXDirectory     string `json:"amx_directory"`
	Account          string `json:"cc_account"`
	ConfigDir        string `json:"cc_config_dir"`
	Flags            string `json:"cc_flags"`
	ExplicitFlags    string `json:"cc_explicit_flags"`
	ExplicitFlagsSet bool   `json:"-"`
	PW               string `json:"cc_pw"`
	Env              string `json:"cc_env"`
	Groups           string `json:"cc_groups"`
	Mute             string `json:"cc_mute"`
	CWD              string `json:"cc_cwd"`
	Locked           bool   `json:"-"`
}

func (u *UserVars) UnmarshalJSON(data []byte) error {
	type alias UserVars

	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	*u = UserVars(decoded)
	_, u.ExplicitFlagsSet = raw["cc_explicit_flags"]
	if v, ok := raw["amx_locked"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			u.Locked = s == "true"
		}
	}
	return nil
}

func (u UserVars) EffectiveProfile() string {
	if u.Profile != "" {
		return u.Profile
	}
	return u.Account
}

func (u UserVars) EffectiveHarness() string {
	if u.Harness != "" {
		return harness.Normalize(u.Harness)
	}
	if u.Account != "" {
		return harness.Claude
	}
	return ""
}

func (u UserVars) EffectiveHomeDir() string {
	if u.HomeDir != "" {
		return u.HomeDir
	}
	return u.ConfigDir
}

func (u UserVars) EffectiveProject(baseTitle string) string {
	if u.Project != "" {
		return u.Project
	}
	return baseTitle
}

func (u UserVars) EffectiveArgs() string {
	if u.Args != "" {
		return u.Args
	}
	return u.Flags
}

func (u UserVars) EffectiveDirectory() string {
	if u.AMXDirectory != "" {
		return u.AMXDirectory
	}
	return u.CWD
}

func (u UserVars) EffectiveEnv() string {
	if u.AMXEnv != "" {
		return u.AMXEnv
	}
	return u.Env
}

func (u UserVars) EffectiveGroups() string {
	if u.AMXGroups != "" {
		return u.AMXGroups
	}
	return u.Groups
}
