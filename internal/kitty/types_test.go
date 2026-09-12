package kitty

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/sglavoie/agentmux/internal/harness"
)

func loadDashboardFixture(t *testing.T) []OSWindow {
	t.Helper()
	data, err := os.ReadFile("../../testdata/kitty_ls_dashboard.json")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	var osWindows []OSWindow
	if err := json.Unmarshal(data, &osWindows); err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	return osWindows
}

func TestFindDashboardWindow(t *testing.T) {
	osWindows := loadDashboardFixture(t)

	t.Run("finds dashboard", func(t *testing.T) {
		loc, found := FindDashboardWindow(osWindows, 0)
		if !found {
			t.Fatal("expected to find dashboard")
		}
		if loc.TabID != 10 {
			t.Errorf("TabID = %d, want 10", loc.TabID)
		}
		if loc.WindowID != 100 {
			t.Errorf("WindowID = %d, want 100", loc.WindowID)
		}
	})

	t.Run("self-exclusion", func(t *testing.T) {
		_, found := FindDashboardWindow(osWindows, 100)
		if found {
			t.Fatal("expected self-exclusion to prevent match")
		}
	})

	t.Run("not found in empty", func(t *testing.T) {
		_, found := FindDashboardWindow(nil, 0)
		if found {
			t.Fatal("expected no match in nil slice")
		}
	})

	t.Run("not found when only subcommands", func(t *testing.T) {
		windows := []OSWindow{{
			ID: 1,
			Tabs: []Tab{{
				ID: 1,
				Windows: []Window{{
					ID: 1,
					ForegroundProcesses: []ForegroundProcess{
						{Cmdline: []string{"amx", "daemon", "start"}},
					},
				}},
			}},
		}}
		_, found := FindDashboardWindow(windows, 0)
		if found {
			t.Fatal("subcommand invocations should not match")
		}
	})
}

func TestFindWindowLocationByID(t *testing.T) {
	osWindows := []OSWindow{{
		ID: 1,
		Tabs: []Tab{{
			ID:    10,
			Title: "W:api",
			Windows: []Window{{
				ID:    100,
				Title: "shell",
			}},
		}},
	}}

	osw, tab, win, found := FindWindowLocationByID(osWindows, 100)
	if !found {
		t.Fatal("expected to find window 100")
	}
	if osw.ID != 1 || tab.ID != 10 || win.ID != 100 {
		t.Fatalf("location = osw:%d tab:%d win:%d; want 1/10/100", osw.ID, tab.ID, win.ID)
	}

	if _, _, _, found := FindWindowLocationByID(osWindows, 999); found {
		t.Fatal("unexpected match for missing window")
	}
}

func TestFindTabTitleOwner(t *testing.T) {
	tests := []struct {
		name      string
		tab       Tab
		wantID    int
		wantFound bool
	}{
		{
			name: "active managed owner",
			tab: Tab{Windows: []Window{
				{ID: 1, UserVars: UserVars{Profile: "work"}},
				{ID: 2, IsActive: true, UserVars: UserVars{Harness: harness.Codex}},
			}},
			wantID:    2,
			wantFound: true,
		},
		{
			name: "falls back to first managed window",
			tab: Tab{Windows: []Window{
				{ID: 1, UserVars: UserVars{Profile: "work"}},
				{ID: 2, UserVars: UserVars{Harness: harness.Copilot}},
			}},
			wantID:    1,
			wantFound: true,
		},
		{
			name: "unmanaged tab has no owner",
			tab: Tab{Windows: []Window{
				{ID: 1, IsActive: true},
				{ID: 2},
			}},
			wantFound: false,
		},
		{
			name: "active unmanaged split pane does not own managed tab",
			tab: Tab{Windows: []Window{
				{ID: 1, IsActive: true},
				{ID: 2, UserVars: UserVars{Account: "legacy-work"}},
				{ID: 3, UserVars: UserVars{Profile: "work"}},
			}},
			wantID:    2,
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := FindTabTitleOwner(&tt.tab)
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if !found {
				return
			}
			if got.ID != tt.wantID {
				t.Errorf("owner ID = %d, want %d", got.ID, tt.wantID)
			}
		})
	}

	if _, found := FindTabTitleOwner(nil); found {
		t.Fatal("nil tab should not have an owner")
	}
}

func TestIsDashboardCmdline(t *testing.T) {
	tests := []struct {
		name    string
		cmdline []string
		want    bool
	}{
		{"bare amx", []string{"amx"}, true},
		{"full path", []string{"/usr/local/bin/amx"}, true},
		{"with debug flag", []string{"amx", "--debug"}, true},
		{"with window ID flag", []string{"amx", "--window-id", "42"}, true},
		{"with window ID equals flag", []string{"amx", "--window-id=42"}, true},
		{"window ID flag without value", []string{"amx", "--window-id"}, false},
		{"flag terminator leaves root command", []string{"amx", "--", "keybindings"}, true},
		{"help flag is not dashboard", []string{"amx", "--help"}, false},
		{"version flag is not dashboard", []string{"amx", "--version"}, false},
		{"short help flag is not dashboard", []string{"amx", "-h"}, false},
		{"config subcommand", []string{"amx", "config"}, false},
		{"daemon start", []string{"amx", "daemon", "start"}, false},
		{"different binary", []string{"claude"}, false},
		{"empty", []string{}, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDashboardCmdline(tt.cmdline)
			if got != tt.want {
				t.Errorf("isDashboardCmdline(%v) = %v, want %v", tt.cmdline, got, tt.want)
			}
		})
	}
}

func TestIsKeybindingsCmdline(t *testing.T) {
	tests := []struct {
		name    string
		cmdline []string
		want    bool
	}{
		{"bare amx keybindings", []string{"amx", "keybindings"}, true},
		{"full path", []string{"/usr/local/bin/amx", "keybindings"}, true},
		{"with debug flag", []string{"amx", "--debug", "keybindings"}, true},
		{"with multiple flags", []string{"amx", "--debug", "-v", "keybindings"}, true},
		{"root help flag is not keybindings", []string{"amx", "--help", "keybindings"}, false},
		{"root version flag is not keybindings", []string{"amx", "--version", "keybindings"}, false},
		{"after flag terminator is positional arg", []string{"amx", "--", "keybindings"}, false},
		{"bare amx", []string{"amx"}, false},
		{"different subcommand", []string{"amx", "daemon"}, false},
		{"flag-only no subcommand", []string{"amx", "--debug"}, false},
		{"empty", []string{}, false},
		{"nil", nil, false},
		{"different binary", []string{"claude", "keybindings"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isKeybindingsCmdline(tt.cmdline)
			if got != tt.want {
				t.Errorf("isKeybindingsCmdline(%v) = %v, want %v", tt.cmdline, got, tt.want)
			}
		})
	}
}

func TestFindOverviewWindow(t *testing.T) {
	windows := []OSWindow{
		{ID: 1, Tabs: []Tab{{
			ID: 10,
			Windows: []Window{
				{ID: 100, ForegroundProcesses: []ForegroundProcess{{Cmdline: []string{"amx", "keybindings"}}}},
				{ID: 101, ForegroundProcesses: []ForegroundProcess{{Cmdline: []string{"claude", "overview"}}}},
			},
		}}},
		{ID: 2, Tabs: []Tab{{
			ID: 88,
			Windows: []Window{
				{ID: 879, ForegroundProcesses: []ForegroundProcess{{Cmdline: []string{"amx", "focus-next", "--window-id", "101"}}}},
				{ID: 880, ForegroundProcesses: []ForegroundProcess{{Cmdline: []string{"/usr/local/bin/amx", "overview", "--window-id", "101"}}}},
			},
		}}},
	}

	loc, found := FindOverviewWindow(windows, 0)
	if !found {
		t.Fatal("expected to find the overview in the second OS window")
	}
	if loc.TabID != 88 || loc.WindowID != 880 {
		t.Fatalf("location = %+v, want tab 88 window 880", loc)
	}
	if _, found := FindOverviewWindow(windows, 880); found {
		t.Fatal("expected self-exclusion to prevent an overview match")
	}
	if _, found := FindOverviewWindow(nil, 0); found {
		t.Fatal("expected no match in nil slice")
	}
	if _, found := FindOverviewWindow(windows[:1], 0); found {
		t.Fatal("expected no match among an unrelated overlay and an unrelated binary")
	}
}

func TestIsOverviewCmdline(t *testing.T) {
	tests := []struct {
		name    string
		cmdline []string
		want    bool
	}{
		{"bare amx overview", []string{"amx", "overview"}, true},
		{"full path", []string{"/usr/local/bin/amx", "overview"}, true},
		{"F2 binding argv", []string{"amx", "overview", "--window-id", "101"}, true},
		{"source window equals form", []string{"amx", "overview", "--window-id=101"}, true},
		{"with debug root flag", []string{"amx", "--debug", "overview"}, true},
		{"with multiple root flags", []string{"amx", "--debug=false", "-v", "overview", "--window-id", "7"}, true},
		// --debug is PERSISTENT, so Cobra accepts it after the subcommand too,
		// on either side of overview's own source-window flag. A rejection here
		// would hide a running overview and let the next F2 press open a second.
		{"trailing debug flag", []string{"amx", "overview", "--debug"}, true},
		{"trailing debug true", []string{"amx", "overview", "--debug=true"}, true},
		{"trailing debug false", []string{"amx", "overview", "--debug=false"}, true},
		{"trailing debug shorthand value", []string{"amx", "overview", "--debug=0"}, true},
		{"debug after source window flag", []string{"amx", "overview", "--window-id=101", "--debug=true"}, true},
		{"debug before source window flag", []string{"amx", "overview", "--debug", "--window-id", "101"}, true},
		{"debug on both sides of the subcommand", []string{"amx", "--debug", "overview", "--debug=false"}, true},
		// A boolean flag takes an attached value or none, and pflag refuses one
		// that does not parse: neither argv below survives parsing, so neither
		// names a window holding a surface to focus.
		{"trailing debug with unparsable value", []string{"amx", "overview", "--debug=maybe"}, false},
		{"trailing debug with a detached value", []string{"amx", "overview", "--debug", "false"}, false},
		{"debug-lookalike flag", []string{"amx", "overview", "--debugger"}, false},
		{"source window flag without value", []string{"amx", "overview", "--window-id"}, false},
		{"root help invocation", []string{"amx", "--help", "overview"}, false},
		{"subcommand help invocation", []string{"amx", "overview", "--help"}, false},
		{"root version invocation", []string{"amx", "--version", "overview"}, false},
		{"subcommand version invocation", []string{"amx", "overview", "--version"}, false},
		{"terminator before subcommand", []string{"amx", "--", "overview"}, false},
		{"terminator after subcommand", []string{"amx", "overview", "--"}, false},
		// The word is the subcommand or it is nothing: another command carrying
		// it as an argument must not be focused in the overview's place.
		{"overview as another command's argument", []string{"amx", "session", "continue", "overview"}, false},
		{"overview as another flag's value", []string{"amx", "hook-indicator", "--state", "overview"}, false},
		{"trailing positional argument", []string{"amx", "overview", "api"}, false},
		{"unknown flag after the subcommand", []string{"amx", "overview", "--json"}, false},
		{"another overlay", []string{"amx", "keybindings"}, false},
		{"bare amx", []string{"amx"}, false},
		{"flag-only no subcommand", []string{"amx", "--debug"}, false},
		{"different binary", []string{"claude", "overview"}, false},
		{"empty", []string{}, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOverviewCmdline(tt.cmdline); got != tt.want {
				t.Errorf("isOverviewCmdline(%v) = %v, want %v", tt.cmdline, got, tt.want)
			}
		})
	}
}

// The root dashboard and the overview are the two surfaces here whose argv
// carries --window-id, so they are the pair most easily confused: each detector
// has to claim its own argv and refuse the other's, or F2 and ctrl+shift+d would
// focus each other's windows.
func TestTheDashboardAndOverviewDetectorsClaimOnlyTheirOwnArgv(t *testing.T) {
	tests := []struct {
		name          string
		cmdline       []string
		wantDashboard bool
		wantOverview  bool
	}{
		{"dashboard binding argv", []string{"amx", "--window-id", "101"}, true, false},
		{"overview binding argv", []string{"amx", "overview", "--window-id", "101"}, false, true},
		{"bare amx", []string{"amx"}, true, false},
		{"bare overview", []string{"amx", "overview"}, false, true},
		{"another overlay", []string{"amx", "keybindings"}, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDashboardCmdline(tt.cmdline); got != tt.wantDashboard {
				t.Errorf("isDashboardCmdline(%v) = %v, want %v", tt.cmdline, got, tt.wantDashboard)
			}
			if got := isOverviewCmdline(tt.cmdline); got != tt.wantOverview {
				t.Errorf("isOverviewCmdline(%v) = %v, want %v", tt.cmdline, got, tt.wantOverview)
			}
		})
	}
}

func TestIsColorPickerCmdline(t *testing.T) {
	tests := []struct {
		name    string
		cmdline []string
		want    bool
	}{
		{"bare amx color-picker", []string{"amx", "color-picker"}, true},
		{"full path", []string{"/usr/local/bin/amx", "color-picker"}, true},
		{"with debug flag", []string{"amx", "--debug", "color-picker"}, true},
		{"root help flag is not color picker", []string{"amx", "--help", "color-picker"}, false},
		{"root version flag is not color picker", []string{"amx", "--version", "color-picker"}, false},
		{"after flag terminator is positional arg", []string{"amx", "--", "color-picker"}, false},
		{"wrong subcommand", []string{"amx", "keybindings"}, false},
		{"wrong binary", []string{"claude", "color-picker"}, false},
		{"empty", []string{}, false},
		{"nil", nil, false},
		{"bare amx", []string{"amx"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isColorPickerCmdline(tt.cmdline)
			if got != tt.want {
				t.Errorf("isColorPickerCmdline(%v) = %v, want %v", tt.cmdline, got, tt.want)
			}
		})
	}
}

func TestFindIconPickerWindow(t *testing.T) {
	windows := []OSWindow{{
		Tabs: []Tab{{
			ID: 77,
			Windows: []Window{
				{ID: 700, ForegroundProcesses: []ForegroundProcess{{Cmdline: []string{"amx", "keybindings"}}}},
				{ID: 701, ForegroundProcesses: []ForegroundProcess{{Cmdline: []string{"/usr/local/bin/amx", "icon-picker"}}}},
			},
		}},
	}}

	loc, found := FindIconPickerWindow(windows, 0)
	if !found {
		t.Fatal("expected to find icon picker")
	}
	if loc.TabID != 77 || loc.WindowID != 701 {
		t.Fatalf("location = %+v, want tab 77 window 701", loc)
	}
	if _, found := FindIconPickerWindow(windows, 701); found {
		t.Fatal("expected self-exclusion to prevent icon-picker match")
	}
	if _, found := FindIconPickerWindow(nil, 0); found {
		t.Fatal("expected no match in nil slice")
	}
}

func TestIsIconPickerCmdline(t *testing.T) {
	tests := []struct {
		name    string
		cmdline []string
		want    bool
	}{
		{"bare amx icon-picker", []string{"amx", "icon-picker"}, true},
		{"full path", []string{"/usr/local/bin/amx", "icon-picker"}, true},
		{"with debug root flag", []string{"amx", "--debug", "icon-picker"}, true},
		{"with multiple root flags", []string{"amx", "--debug=false", "-v", "icon-picker"}, true},
		{"root help invocation", []string{"amx", "--help", "icon-picker"}, false},
		{"subcommand help invocation", []string{"amx", "icon-picker", "--help"}, false},
		{"root version invocation", []string{"amx", "--version", "icon-picker"}, false},
		{"subcommand version invocation", []string{"amx", "icon-picker", "--version"}, false},
		{"terminator before subcommand", []string{"amx", "--", "icon-picker"}, false},
		{"terminator after subcommand", []string{"amx", "icon-picker", "--"}, false},
		{"other subcommand", []string{"amx", "color-picker"}, false},
		{"trailing positional argument", []string{"amx", "icon-picker", "other"}, false},
		{"bare amx", []string{"amx"}, false},
		{"different binary", []string{"claude", "icon-picker"}, false},
		{"empty", []string{}, false},
		{"nil", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isIconPickerCmdline(tt.cmdline); got != tt.want {
				t.Errorf("isIconPickerCmdline(%v) = %v, want %v", tt.cmdline, got, tt.want)
			}
		})
	}
}

func TestUserVars_EffectiveHarness(t *testing.T) {
	tests := []struct {
		name string
		u    UserVars
		want string
	}{
		{
			name: "explicit harness set",
			u:    UserVars{Harness: "copilot"},
			want: "copilot",
		},
		{
			name: "legacy cc_account with no harness",
			u:    UserVars{Account: "work"},
			want: harness.Claude,
		},
		{
			name: "both empty",
			u:    UserVars{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.u.EffectiveHarness()
			if got != tt.want {
				t.Errorf("EffectiveHarness() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUserVars_EffectiveProfile(t *testing.T) {
	tests := []struct {
		name string
		u    UserVars
		want string
	}{
		{
			name: "amx_profile set",
			u:    UserVars{Profile: "claude-work"},
			want: "claude-work",
		},
		{
			name: "legacy cc_account fallback",
			u:    UserVars{Account: "work"},
			want: "work",
		},
		{
			name: "amx_profile takes precedence over cc_account",
			u:    UserVars{Profile: "claude-work", Account: "work"},
			want: "claude-work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.u.EffectiveProfile()
			if got != tt.want {
				t.Errorf("EffectiveProfile() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUserVars_UnmarshalJSON_Locked(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{"true value", `{"amx_locked":"true"}`, true},
		{"false value", `{"amx_locked":"false"}`, false},
		{"empty value", `{"amx_locked":""}`, false},
		{"key absent", `{}`, false},
		{"other key only", `{"cc_account":"work"}`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var u UserVars
			if err := json.Unmarshal([]byte(tt.json), &u); err != nil {
				t.Fatalf("Unmarshal(%q) error: %v", tt.json, err)
			}
			if u.Locked != tt.want {
				t.Errorf("Locked = %v, want %v (input %q)", u.Locked, tt.want, tt.json)
			}
		})
	}
}

// TestUserVars_MarshalJSON_RoundTripPreservesPresenceFacts covers the path the
// daemon opened by publishing its cached window tree on the IPC wire: user vars
// decoded from `kitten @ ls` are re-encoded, sent, and decoded again by a
// client. Two facts are carried by key presence rather than by a value, so the
// default struct encoding loses both — silently, and in the direction that
// makes a locked session look unlocked.
func TestUserVars_MarshalJSON_RoundTripPreservesPresenceFacts(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"locked with explicit flags", `{"cc_account":"work","cc_explicit_flags":"--model opus","amx_locked":"true"}`},
		{"locked with no explicit flags", `{"cc_account":"work","amx_locked":"true"}`},
		{"unlocked with an empty explicit flags key", `{"cc_account":"work","cc_explicit_flags":""}`},
		{"unlocked with neither key", `{"cc_account":"work"}`},
		{"explicitly unlocked", `{"cc_account":"work","amx_locked":"false"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var first UserVars
			if err := json.Unmarshal([]byte(tt.json), &first); err != nil {
				t.Fatalf("Unmarshal(%q) error: %v", tt.json, err)
			}
			data, err := json.Marshal(first)
			if err != nil {
				t.Fatalf("Marshal(UserVars): %v", err)
			}
			var second UserVars
			if err := json.Unmarshal(data, &second); err != nil {
				t.Fatalf("Unmarshal(re-encoded %s): %v", data, err)
			}
			if second != first {
				t.Fatalf("round trip changed the user vars:\n got %+v\nwant %+v\nencoded as %s",
					second, first, data)
			}
		})
	}
}
