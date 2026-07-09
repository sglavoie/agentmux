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
