package kitty

import (
	"encoding/json"
	"os"
	"testing"
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
						{Cmdline: []string{"ccm", "daemon", "start"}},
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
		{"bare ccm", []string{"ccm"}, true},
		{"full path", []string{"/usr/local/bin/ccm"}, true},
		{"with debug flag", []string{"ccm", "--debug"}, true},
		{"config subcommand", []string{"ccm", "config"}, false},
		{"daemon start", []string{"ccm", "daemon", "start"}, false},
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
