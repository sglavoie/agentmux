package cmd

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestMuteToggle_UnmutedToMuted(t *testing.T) {
	client := &fakeMuteClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{
				testManagedTabWithBase(10, 101, "api"),
			},
		}},
	}

	if err := muteToggle(client, 101); err != nil {
		t.Fatalf("muteToggle: %v", err)
	}

	if len(client.setUserVarsCalls) != 1 {
		t.Fatalf("set-user-vars calls: got %d, want 1", len(client.setUserVarsCalls))
	}
	got := client.setUserVarsCalls[0]
	if got.match != "id:101" {
		t.Errorf("set-user-vars match: got %q, want %q", got.match, "id:101")
	}
	if v, ok := got.vars["cc_mute"]; !ok || v != "1" {
		t.Errorf("set-user-vars vars: got %v, want cc_mute=1", got.vars)
	}

	if len(client.setTabTitleCalls) != 1 {
		t.Fatalf("set-tab-title calls: got %d, want 1", len(client.setTabTitleCalls))
	}
	titleCall := client.setTabTitleCalls[0]
	if titleCall.match != "window_id:101" {
		t.Errorf("set-tab-title match: got %q, want %q", titleCall.match, "window_id:101")
	}
	if !strings.Contains(titleCall.title, palette.MuteIcon()) {
		t.Errorf("set-tab-title %q should contain mute icon %q", titleCall.title, palette.MuteIcon())
	}
}

func TestMuteToggle_MutedToUnmuted(t *testing.T) {
	client := &fakeMuteClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{
				testMutedManagedTab(10, 101, palette.BuildTitleFull("api", "", false, true)),
			},
		}},
	}

	if err := muteToggle(client, 101); err != nil {
		t.Fatalf("muteToggle: %v", err)
	}

	if len(client.setUserVarsCalls) != 1 {
		t.Fatalf("set-user-vars calls: got %d, want 1", len(client.setUserVarsCalls))
	}
	got := client.setUserVarsCalls[0]
	if v, ok := got.vars["cc_mute"]; !ok || v != "" {
		t.Errorf("set-user-vars vars: got %v, want cc_mute=\"\"", got.vars)
	}

	if len(client.setTabTitleCalls) != 1 {
		t.Fatalf("set-tab-title calls: got %d, want 1", len(client.setTabTitleCalls))
	}
	if strings.Contains(client.setTabTitleCalls[0].title, palette.MuteIcon()) {
		t.Errorf("set-tab-title %q should not contain mute icon", client.setTabTitleCalls[0].title)
	}
}

func TestMuteToggle_UnmanagedIsNoOp(t *testing.T) {
	client := &fakeMuteClient{
		windows: []kitty.OSWindow{{
			ID: 1,
			Tabs: []kitty.Tab{
				testUnmanagedTab(20, 201, "shell"),
			},
		}},
	}

	if err := muteToggle(client, 201); err != nil {
		t.Fatalf("muteToggle: %v", err)
	}

	if len(client.setUserVarsCalls) != 0 {
		t.Errorf("expected zero set-user-vars calls, got %d", len(client.setUserVarsCalls))
	}
	if len(client.setTabTitleCalls) != 0 {
		t.Errorf("expected zero set-tab-title calls, got %d", len(client.setTabTitleCalls))
	}
}

func TestMuteToggle_WindowIDZeroIsNoOp(t *testing.T) {
	client := &fakeMuteClient{}

	if err := muteToggle(client, 0); err != nil {
		t.Fatalf("muteToggle: %v", err)
	}

	if client.listWindowsCalls != 0 {
		t.Errorf("expected zero list-windows calls, got %d", client.listWindowsCalls)
	}
	if len(client.setUserVarsCalls) != 0 {
		t.Errorf("expected zero set-user-vars calls, got %d", len(client.setUserVarsCalls))
	}
	if len(client.setTabTitleCalls) != 0 {
		t.Errorf("expected zero set-tab-title calls, got %d", len(client.setTabTitleCalls))
	}
}

type setUserVarsCall struct {
	match string
	vars  map[string]string
}

type setTabTitleCall struct {
	match string
	title string
}

type fakeMuteClient struct {
	windows          []kitty.OSWindow
	listWindowsCalls int
	setUserVarsCalls []setUserVarsCall
	setTabTitleCalls []setTabTitleCall
}

func (f *fakeMuteClient) ListWindows() ([]kitty.OSWindow, error) {
	f.listWindowsCalls++
	return f.windows, nil
}

func (f *fakeMuteClient) LaunchTab(kitty.LaunchOpts) error { return nil }
func (f *fakeMuteClient) CloseTab(string) error            { return nil }
func (f *fakeMuteClient) MoveTab(string, int) error        { return nil }
func (f *fakeMuteClient) SetTabColor(string, string, string, string, string) error {
	return nil
}

func (f *fakeMuteClient) SetTabTitle(matchExpr, title string) error {
	f.setTabTitleCalls = append(f.setTabTitleCalls, setTabTitleCall{match: matchExpr, title: title})
	return nil
}

func (f *fakeMuteClient) SetUserVars(matchExpr string, vars map[string]string) error {
	copyVars := make(map[string]string, len(vars))
	for k, v := range vars {
		copyVars[k] = v
	}
	f.setUserVarsCalls = append(f.setUserVarsCalls, setUserVarsCall{match: matchExpr, vars: copyVars})
	return nil
}

func (f *fakeMuteClient) FocusTab(string) error               { return nil }
func (f *fakeMuteClient) FocusWindow(string) error            { return nil }
func (f *fakeMuteClient) GetText(string, string) (string, error) {
	return "", nil
}
func (f *fakeMuteClient) LaunchOverlayPager(string) error { return nil }

func testManagedTabWithBase(tabID, windowID int, base string) kitty.Tab {
	return kitty.Tab{
		ID:    tabID,
		Title: base,
		Windows: []kitty.Window{{
			ID:       windowID,
			UserVars: kitty.UserVars{Profile: "work"},
		}},
	}
}

func testMutedManagedTab(tabID, windowID int, title string) kitty.Tab {
	return kitty.Tab{
		ID:    tabID,
		Title: title,
		Windows: []kitty.Window{{
			ID:       windowID,
			UserVars: kitty.UserVars{Profile: "work", Mute: "1"},
		}},
	}
}
