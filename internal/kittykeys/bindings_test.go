package kittykeys

import (
	"strings"
	"testing"
)

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
	if dashboard.AmxCommand != "--window-id @active-kitty-window-id" {
		t.Fatalf("dashboard AmxCommand = %q, want active Kitty window ID", dashboard.AmxCommand)
	}
	if dashboard.Description != "open or focus Agent Mux dashboard" {
		t.Fatalf("dashboard Description = %q", dashboard.Description)
	}
}

// F2 is the overview's only keyboard entry point, and the whole singleton
// contract depends on the argv it launches: `overview` has to be the subcommand
// (the detector recognizes nothing else) and the source window has to be passed
// (otherwise the overview cannot preselect the pane the operator came from).
func TestOverviewBindingLaunchesF2OverlayWithTheSourceWindow(t *testing.T) {
	overview := findAction(t, "f2")

	if overview.Section != "Overlays" {
		t.Fatalf("f2 Section = %q, want Overlays", overview.Section)
	}
	if overview.LaunchType != "overlay" {
		t.Fatalf("f2 LaunchType = %q, want overlay", overview.LaunchType)
	}
	if overview.AmxCommand != "overview --window-id @active-kitty-window-id" {
		t.Fatalf("f2 AmxCommand = %q, want the overview command with the active Kitty window ID", overview.AmxCommand)
	}
	if overview.Description != "open or focus the live session overview" {
		t.Fatalf("f2 Description = %q", overview.Description)
	}
	const want = "map f2 launch --type=overlay --allow-remote-control amx overview --window-id @active-kitty-window-id"
	if got := overview.ConfigLine(); got != want {
		t.Fatalf("f2 ConfigLine() = %q, want %q", got, want)
	}
	if first := strings.Fields(overview.AmxCommand)[0]; first != "overview" {
		t.Fatalf("f2 launches %q, want overview as the subcommand", first)
	}
}

func TestIconPickerBindingLaunchesF5Overlay(t *testing.T) {
	picker := findAction(t, "f5")

	if picker.Section != "Sessions" {
		t.Fatalf("f5 Section = %q, want Sessions", picker.Section)
	}
	if picker.LaunchType != "overlay" {
		t.Fatalf("f5 LaunchType = %q, want overlay", picker.LaunchType)
	}
	if picker.AmxCommand != "icon-picker" {
		t.Fatalf("f5 AmxCommand = %q, want icon-picker", picker.AmxCommand)
	}
	if picker.Description != "set tab harness icon (overlay picker)" {
		t.Fatalf("f5 Description = %q", picker.Description)
	}
	const want = "map f5 launch --type=overlay --allow-remote-control amx icon-picker"
	if got := picker.ConfigLine(); got != want {
		t.Fatalf("f5 ConfigLine() = %q, want %q", got, want)
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

func TestScrollbackMarkerBindingDescribesConfiguredMarkers(t *testing.T) {
	markers := findAction(t, "f3")

	if markers.Section != "Scrollback" {
		t.Fatalf("f3 Section = %q, want Scrollback", markers.Section)
	}
	if markers.Description != "toggle error/failure/warning/deprecated markers" {
		t.Fatalf("f3 Description = %q", markers.Description)
	}
	if markers.AmxCommand != "" {
		t.Fatalf("f3 AmxCommand = %q, want native Kitty action", markers.AmxCommand)
	}
	if markers.LaunchType != "" {
		t.Fatalf("f3 LaunchType = %q, want native Kitty action", markers.LaunchType)
	}
	if markers.KittyCommand == "" {
		t.Fatal("f3 KittyCommand is empty, want native Kitty command")
	}
}

func TestConfigLineGeneratesAMXRootDashboardLaunch(t *testing.T) {
	dashboard := findAction(t, "ctrl+shift+d")

	want := "map ctrl+shift+d launch --type=overlay --allow-remote-control amx --window-id @active-kitty-window-id"
	if got := dashboard.ConfigLine(); got != want {
		t.Fatalf("dashboard ConfigLine() = %q, want %q", got, want)
	}
}

func TestConfigLineGeneratesNativeKittyCommands(t *testing.T) {
	tests := map[string]string{
		"f3":    `map f3 toggle_marker iregex 1 \\bError\\b 2 \\bFailed\\b 3 \\bWarning\\b 3 \\bDEPRECATED\\b`,
		"f12":   "map f12 launch --allow-remote-control kitty +kitten broadcast",
		"cmd+0": "map cmd+0 goto_tab 10",
		"cmd+l": "map cmd+l goto_tab -1",
	}

	for key, want := range tests {
		action := findAction(t, key)
		if got := action.ConfigLine(); got != want {
			t.Fatalf("%s ConfigLine() = %q, want %q", key, got, want)
		}
	}
}

func TestActionsGenerateConcreteConfigLines(t *testing.T) {
	for _, action := range Actions {
		line := action.ConfigLine()
		if line == "" {
			t.Fatalf("%s ConfigLine() is empty", action.Key)
		}
		if !strings.HasPrefix(line, "map "+action.Key+" ") {
			t.Fatalf("%s ConfigLine() = %q, want map prefix", action.Key, line)
		}
	}
}

func TestActionsUseConcreteKittyKeyExpressions(t *testing.T) {
	seen := make(map[string]bool)

	for _, action := range Actions {
		if action.Key == "" {
			t.Fatal("action has empty Key")
		}
		if strings.ContainsAny(action.Key, " \t\n\r") {
			t.Fatalf("%q is not a concrete Kitty key expression", action.Key)
		}
		if strings.Contains(action.Key, "–") {
			t.Fatalf("%q is a display range, want concrete Kitty key expression", action.Key)
		}
		if seen[action.Key] {
			t.Fatalf("duplicate binding for %q", action.Key)
		}
		seen[action.Key] = true
	}
}
