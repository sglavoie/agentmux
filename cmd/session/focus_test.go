package session

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestFocusManagedTab_FocusesRelativeManagedTab(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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

// TestNextAttentionTab_UsesSharedAttentionSet pins focus-next to the shared
// protocol.AttentionStates set: every state in it is a stop for the cycle and
// every other palette state is passed over. The companion snapshot's attention
// bit reads the same set, which is what keeps the menu bar's "next attention"
// and focus-next landing on the same tab.
func TestNextAttentionTab_UsesSharedAttentionSet(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"bolt", "stop", "key", "warning", "error", "subagent", "compact", "question", ""} {
		t.Run("state="+state, func(t *testing.T) {
			windows := []kitty.OSWindow{{
				ID: 1,
				Tabs: []kitty.Tab{
					{ID: 10, Title: "W:here", Windows: []kitty.Window{{ID: 101}}},
					{ID: 20, Title: palette.BuildTitleFull("W:there", state, false, false), Windows: []kitty.Window{{ID: 201}}},
				},
			}}
			tab, ok := nextAttentionTab(windows, 101)
			want := protocol.AttentionStates[state]
			if ok != want {
				t.Fatalf("nextAttentionTab ok = %v, want %v", ok, want)
			}
			if ok && tab.ID != 20 {
				t.Fatalf("tab = %d, want 20", tab.ID)
			}
		})
	}
}

func TestNextAttentionTab_KittyFlagAndCurrentTab(t *testing.T) {
	t.Parallel()

	windows := []kitty.OSWindow{{
		ID: 1,
		Tabs: []kitty.Tab{
			{ID: 10, Title: palette.BuildTitleFull("W:here", "stop", false, false), Windows: []kitty.Window{{ID: 101}}},
			{ID: 20, Title: "W:quiet", Windows: []kitty.Window{{ID: 201}}},
			{ID: 30, Title: "W:flagged", NeedsAttention: true, Windows: []kitty.Window{{ID: 301}}},
		},
	}}
	tab, ok := nextAttentionTab(windows, 101)
	if !ok || tab.ID != 30 {
		t.Fatalf("nextAttentionTab = (%d, %v), want (30, true): the current tab is skipped and Kitty's flag counts", tab.ID, ok)
	}
	if tab, ok := nextAttentionTab(windows, 999); ok {
		t.Fatalf("nextAttentionTab from an unknown window = %d, want no target", tab.ID)
	}
}
