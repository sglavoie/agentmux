package cmd

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestFocusManagedTab_FocusesRelativeManagedTab(t *testing.T) {
	client := &fakeFocusManagedClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{
				testManagedTab(10, 101, "W:api"),
				testUnmanagedTab(20, 201, "shell"),
				testManagedTab(30, 301, "W:web"),
			},
		}},
	}

	if err := focusManagedTab(client, 101, 1); err != nil {
		t.Fatalf("focusManagedTab: %v", err)
	}

	if len(client.focusCalls) != 1 {
		t.Fatalf("focus calls: got %d, want 1", len(client.focusCalls))
	}
	if client.focusCalls[0] != "id:30" {
		t.Fatalf("focus call: got %q, want %q", client.focusCalls[0], "id:30")
	}
}

func TestFocusManagedTab_NoManagedTargetIsNoOp(t *testing.T) {
	client := &fakeFocusManagedClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{
				testManagedTab(10, 101, "W:api"),
				testUnmanagedTab(20, 201, "shell"),
			},
		}},
	}

	if err := focusManagedTab(client, 101, 1); err != nil {
		t.Fatalf("focusManagedTab: %v", err)
	}

	if len(client.focusCalls) != 0 {
		t.Fatalf("focus calls: got %d, want 0", len(client.focusCalls))
	}
}

type fakeFocusManagedClient struct {
	windows    []kitty.OSWindow
	focusCalls []string
	userVars   []fakeUserVarsCall
}

type fakeUserVarsCall struct {
	match string
	vars  map[string]string
}

func (f *fakeFocusManagedClient) ListWindows() ([]kitty.OSWindow, error) {
	return f.windows, nil
}

func (f *fakeFocusManagedClient) LaunchTab(kitty.LaunchOpts) error {
	return nil
}

func (f *fakeFocusManagedClient) LaunchOverlay(kitty.LaunchOpts) error {
	return nil
}

func (f *fakeFocusManagedClient) CloseTab(string) error {
	return nil
}

func (f *fakeFocusManagedClient) MoveTab(string, int) error {
	return nil
}

func (f *fakeFocusManagedClient) SetTabColor(string, string, string, string, string) error {
	return nil
}

func (f *fakeFocusManagedClient) SetTabTitle(string, string) error {
	return nil
}

func (f *fakeFocusManagedClient) FocusTab(matchExpr string) error {
	f.focusCalls = append(f.focusCalls, matchExpr)
	return nil
}

func (f *fakeFocusManagedClient) FocusWindow(string) error {
	return nil
}

func (f *fakeFocusManagedClient) GetText(string, string) (string, error) {
	return "", nil
}

func (f *fakeFocusManagedClient) LaunchOverlayPager(string) error {
	return nil
}

func (f *fakeFocusManagedClient) SetUserVars(match string, vars map[string]string) error {
	f.userVars = append(f.userVars, fakeUserVarsCall{match: match, vars: vars})
	return nil
}

func TestFocusExistingDashboardRequestsSourceTabSelection(t *testing.T) {
	client := &fakeFocusManagedClient{windows: []kitty.OSWindow{{
		Tabs: []kitty.Tab{
			testManagedTab(10, 101, "api"),
			{
				ID: 90,
				Windows: []kitty.Window{{
					ID:                  901,
					ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"amx"}}},
				}},
			},
		},
	}}}

	if !focusExistingDashboard(client, 101) {
		t.Fatal("focusExistingDashboard returned false, want true")
	}
	if len(client.focusCalls) != 1 || client.focusCalls[0] != kitty.MatchTabByID(90) {
		t.Fatalf("focus calls = %v, want dashboard tab 90", client.focusCalls)
	}
	if len(client.userVars) != 1 {
		t.Fatalf("selection request calls = %d, want 1", len(client.userVars))
	}
	call := client.userVars[0]
	if call.match != kitty.MatchWindowByID(901) {
		t.Fatalf("selection request match = %q, want dashboard window 901", call.match)
	}
	request := call.vars[kitty.DashboardSelectionVar]
	if !strings.HasPrefix(request, "10:") || len(strings.TrimPrefix(request, "10:")) == 0 {
		t.Fatalf("selection request = %q, want tab 10 with a token", request)
	}
}

func testManagedTab(tabID, windowID int, title string) kitty.Tab {
	return kitty.Tab{
		ID:    tabID,
		Title: title,
		Windows: []kitty.Window{{
			ID:       windowID,
			UserVars: kitty.UserVars{Profile: "work"},
		}},
	}
}

func testUnmanagedTab(tabID, windowID int, title string) kitty.Tab {
	return kitty.Tab{
		ID:    tabID,
		Title: title,
		Windows: []kitty.Window{{
			ID: windowID,
		}},
	}
}
