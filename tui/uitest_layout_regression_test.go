package tui

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/tui/commands"
)

func TestUITestModalLayoutRegressions(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		fixture string
		size    UITestSize
		actions []UITestAction
		mode    string
	}{
		{
			name:    "actions overlay scrolls on short terminal",
			fixture: "actions",
			size:    UITestSize{Width: 120, Height: 16},
			actions: []UITestAction{{Type: UITestActionKey, Key: "a"}},
			mode:    "actions",
		},
		{
			name:    "help overlay fits on short terminal",
			fixture: "basic",
			size:    UITestSize{Width: 140, Height: 10},
			actions: []UITestAction{{Type: UITestActionKey, Key: "?"}},
			mode:    "help",
		},
		{
			name:    "top anchored config picker does not clip bottom border",
			fixture: "wide_profiles",
			size:    UITestSize{Width: 140, Height: 10},
			actions: []UITestAction{
				{Type: UITestActionKey, Key: keyA},
				{Type: UITestActionKey, Key: keyDashboardLaunch},
			},
			mode: "config_picker",
		},
		{
			name:    "detail overlay natural width fits at 100 columns",
			fixture: "basic",
			size:    UITestSize{Width: 100, Height: 24},
			actions: []UITestAction{
				{Type: UITestActionCommand, CommandID: string(commands.ShowTimeline)},
			},
			mode: "detail",
		},
		{
			name:    "actions overlay fits after resize",
			fixture: "actions",
			size:    UITestSize{Width: 140, Height: 40},
			actions: []UITestAction{
				{Type: UITestActionKey, Key: "a"},
				{Type: UITestActionResize, Width: 120, Height: 16},
			},
			mode: "actions",
		},
		{
			name:    "help overlay fits after resize",
			fixture: "basic",
			size:    UITestSize{Width: 140, Height: 40},
			actions: []UITestAction{
				{Type: UITestActionKey, Key: "?"},
				{Type: UITestActionResize, Width: 140, Height: 10},
			},
			mode: "help",
		},
		{
			name:    "config picker fits after resize",
			fixture: "wide_profiles",
			size:    UITestSize{Width: 140, Height: 40},
			actions: []UITestAction{
				{Type: UITestActionKey, Key: keyA},
				{Type: UITestActionKey, Key: keyDashboardLaunch},
				{Type: UITestActionResize, Width: 140, Height: 10},
			},
			mode: "config_picker",
		},
		{
			name:    "detail overlay fits after resize",
			fixture: "basic",
			size:    UITestSize{Width: 140, Height: 40},
			actions: []UITestAction{
				{Type: UITestActionCommand, CommandID: string(commands.ShowTimeline)},
				{Type: UITestActionResize, Width: 100, Height: 24},
			},
			mode: "detail",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs, err := RunUITestFixture(tc.fixture, UITestOptions{Size: tc.size}, tc.actions)
			if err != nil {
				t.Fatalf("RunUITestFixture returned error: %v", err)
			}
			final := obs[len(obs)-1]
			if final.Mode != tc.mode {
				t.Fatalf("mode = %q, want %q", final.Mode, tc.mode)
			}
			if len(final.Screen.Issues) > 0 {
				t.Fatalf("expected no layout issues, got %s\nscreen:\n%s", formatUITestIssueTypes(final.Screen.Issues), strings.Join(final.Screen.Lines, "\n"))
			}
		})
	}
}

func TestUITestNarrowConfigPickerFits(t *testing.T) {
	t.Parallel()

	obs, err := RunUITestFixture("wide_profiles", UITestOptions{Size: UITestSize{Width: 80, Height: 24}}, []UITestAction{
		{Type: UITestActionKey, Key: keyA},
		{Type: UITestActionKey, Key: keyDashboardLaunch},
	})
	if err != nil {
		t.Fatalf("RunUITestFixture returned error: %v", err)
	}
	final := obs[len(obs)-1]
	if len(final.Screen.Issues) != 0 {
		t.Fatalf("expected a fitting profile picker at 80x24, got %s", formatUITestIssueTypes(final.Screen.Issues))
	}
}

func formatUITestIssueTypes(issues []UITestLayoutIssue) string {
	if len(issues) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		parts = append(parts, string(issue.Type))
	}
	return strings.Join(parts, ", ")
}
