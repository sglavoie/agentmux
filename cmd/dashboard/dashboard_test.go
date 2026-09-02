package dashboard

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
)

// fakeDashboardClient records the focus and user-var writes the dashboard
// singleton check performs. It mirrors the per-file fake each command test in
// this tree keeps; only ListWindows, FocusTab, FocusWindow, and SetUserVars
// carry behavior.
type fakeDashboardClient struct {
	windows    []kitty.OSWindow
	focusCalls []string
	userVars   []fakeUserVarsCall
}

type fakeUserVarsCall struct {
	match string
	vars  map[string]string
}

func (f *fakeDashboardClient) ListWindows() ([]kitty.OSWindow, error) { return f.windows, nil }
func (*fakeDashboardClient) LaunchTab(kitty.LaunchOpts) error         { return nil }
func (*fakeDashboardClient) LaunchOverlay(kitty.LaunchOpts) error     { return nil }
func (*fakeDashboardClient) CloseTab(string) error                    { return nil }
func (*fakeDashboardClient) MoveTab(string, int) error                { return nil }
func (*fakeDashboardClient) SetTabColor(string, string, string, string, string) error {
	return nil
}
func (*fakeDashboardClient) SetTabTitle(string, string) error { return nil }

func (f *fakeDashboardClient) FocusTab(matchExpr string) error {
	f.focusCalls = append(f.focusCalls, matchExpr)
	return nil
}

func (*fakeDashboardClient) FocusWindow(string) error               { return nil }
func (*fakeDashboardClient) GetText(string, string) (string, error) { return "", nil }
func (*fakeDashboardClient) LaunchOverlayPager(string) error        { return nil }

func (f *fakeDashboardClient) SetUserVars(match string, vars map[string]string) error {
	f.userVars = append(f.userVars, fakeUserVarsCall{match: match, vars: vars})
	return nil
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

func TestFocusExistingDashboardRequestsSourceTabSelection(t *testing.T) {
	client := &fakeDashboardClient{windows: []kitty.OSWindow{{
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
