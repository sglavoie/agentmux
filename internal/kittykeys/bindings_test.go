package kittykeys

import "testing"

func TestDashboardBindingLaunchesAMXRootOverlay(t *testing.T) {
	var dashboard Action
	found := false
	for _, action := range Actions {
		if action.Key == "ctrl+shift+d" {
			dashboard = action
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing ctrl+shift+d dashboard binding")
	}

	if dashboard.LaunchType != "overlay" {
		t.Fatalf("dashboard LaunchType = %q, want overlay", dashboard.LaunchType)
	}
	if dashboard.AmxCommand != "" {
		t.Fatalf("dashboard AmxCommand = %q, want empty amx root command", dashboard.AmxCommand)
	}
	if dashboard.Description != "open or focus Agent Mux dashboard" {
		t.Fatalf("dashboard Description = %q", dashboard.Description)
	}
}
