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

func TestIsDashboardCmdline(t *testing.T) {
	tests := []struct {
		name    string
		cmdline []string
		want    bool
	}{
		{"bare amx", []string{"amx"}, true},
		{"full path", []string{"/usr/local/bin/amx"}, true},
		{"with debug flag", []string{"amx", "--debug"}, true},
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

func TestUserVars_EffectiveHarness(t *testing.T) {
	tests := []struct {
		name    string
		u       UserVars
		want    string
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
