package kittykeys

import "testing"

func findAction(t *testing.T, key string) Action {
	t.Helper()

	for _, action := range Actions {
		if action.Key == key {
			return action
		}
	}

	t.Fatalf("missing %s binding", key)
	return Action{}
}

func TestDashboardBindingLaunchesAMXRootOverlay(t *testing.T) {
	dashboard := findAction(t, "ctrl+shift+d")

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

func TestBackgroundBindingsPassActiveWindowID(t *testing.T) {
	tests := map[string]string{
		"f4":  "focus-next --window-id @active-kitty-window-id",
		"f7":  "focus-prev-managed --window-id @active-kitty-window-id",
		"f8":  "hook-indicator user_clear --window-id @active-kitty-window-id",
		"f9":  "focus-next-managed --window-id @active-kitty-window-id",
		"f10": "mute-toggle --window-id @active-kitty-window-id",
	}

	for key, want := range tests {
		action := findAction(t, key)
		if action.LaunchType != "background" {
			t.Fatalf("%s LaunchType = %q, want background", key, action.LaunchType)
		}
		if action.AmxCommand != want {
			t.Fatalf("%s AmxCommand = %q, want %q", key, action.AmxCommand, want)
		}
	}
}

func TestScrollbackMarkerBindingDescribesFailureMarkers(t *testing.T) {
	markers := findAction(t, "f3")

	if markers.Section != "Scrollback" {
		t.Fatalf("f3 Section = %q, want Scrollback", markers.Section)
	}
	if markers.Description != "toggle error/failure/warning markers" {
		t.Fatalf("f3 Description = %q", markers.Description)
	}
	if markers.AmxCommand != "" {
		t.Fatalf("f3 AmxCommand = %q, want native Kitty action", markers.AmxCommand)
	}
	if markers.LaunchType != "" {
		t.Fatalf("f3 LaunchType = %q, want native Kitty action", markers.LaunchType)
	}
}
