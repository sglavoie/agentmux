package dashboard

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/tui/customization"
)

type iconPickerCommandClient struct {
	windows        []kitty.OSWindow
	listErr        error
	focusTabCalls  []string
	focusWinCalls  []string
	userVarsWrites int
}

func (c *iconPickerCommandClient) ListWindows() ([]kitty.OSWindow, error) {
	return c.windows, c.listErr
}
func (*iconPickerCommandClient) LaunchTab(kitty.LaunchOpts) error     { return nil }
func (*iconPickerCommandClient) LaunchOverlay(kitty.LaunchOpts) error { return nil }
func (*iconPickerCommandClient) CloseTab(string) error                { return nil }
func (*iconPickerCommandClient) MoveTab(string, int) error            { return nil }
func (*iconPickerCommandClient) SetTabColor(string, string, string, string, string) error {
	return nil
}
func (*iconPickerCommandClient) SetTabTitle(string, string) error { return nil }
func (c *iconPickerCommandClient) SetUserVars(string, map[string]string) error {
	c.userVarsWrites++
	return nil
}
func (c *iconPickerCommandClient) FocusTab(match string) error {
	c.focusTabCalls = append(c.focusTabCalls, match)
	return nil
}
func (c *iconPickerCommandClient) FocusWindow(match string) error {
	c.focusWinCalls = append(c.focusWinCalls, match)
	return nil
}
func (*iconPickerCommandClient) GetText(string, string) (string, error) { return "", nil }
func (*iconPickerCommandClient) LaunchOverlayPager(string) error        { return nil }

func resetIconPickerCommandTestState(t *testing.T) {
	t.Helper()
	oldNewClient := newIconPickerClient
	oldLoadConfig := loadIconPickerConfig
	oldRunOverlay := runIconPickerOverlay
	t.Cleanup(func() {
		newIconPickerClient = oldNewClient
		loadIconPickerConfig = oldLoadConfig
		runIconPickerOverlay = oldRunOverlay
	})
}

func TestIconPickerCommandTargetsUnmanagedTabWithoutUserVarWrites(t *testing.T) {
	resetIconPickerCommandTestState(t)
	t.Setenv("KITTY_WINDOW_ID", "202")
	client := &iconPickerCommandClient{windows: []kitty.OSWindow{{
		Tabs: []kitty.Tab{{
			ID:    20,
			Title: "shell",
			Windows: []kitty.Window{{
				ID: 202,
			}},
		}},
	}}}
	emojis := map[string]string{"claude": "⭕", "copilot": "🚁", "codex": "📟"}
	newIconPickerClient = func() kitty.KittyClient { return client }
	loadIconPickerConfig = func() (*config.Config, error) {
		return &config.Config{HarnessEmojis: emojis}, nil
	}

	var got customization.IconContext
	runIconPickerOverlay = func(ctx customization.IconContext) error {
		got = ctx
		return nil
	}

	if err := iconPickerCmd.RunE(iconPickerCmd, nil); err != nil {
		t.Fatalf("icon-picker: %v", err)
	}
	if got.Client != client || got.TabID != 20 || got.CurrentTitle != "shell" {
		t.Fatalf("picker context = %+v, want client/tab 20/title shell", got)
	}
	if got.HarnessEmojis["claude"] != "⭕" || got.HarnessEmojis["copilot"] != "🚁" || got.HarnessEmojis["codex"] != "📟" {
		t.Fatalf("picker harness emojis = %#v, want active config", got.HarnessEmojis)
	}
	if client.userVarsWrites != 0 {
		t.Fatalf("SetUserVars writes = %d, want 0", client.userVarsWrites)
	}
}

func TestIconPickerCommandFocusesExistingOverlay(t *testing.T) {
	resetIconPickerCommandTestState(t)
	t.Setenv("KITTY_WINDOW_ID", "202")
	client := &iconPickerCommandClient{windows: []kitty.OSWindow{{
		Tabs: []kitty.Tab{
			{ID: 20, Windows: []kitty.Window{{ID: 202}}},
			{ID: 30, Windows: []kitty.Window{{
				ID:                  303,
				ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"amx", "icon-picker"}}},
			}}},
		},
	}}}
	newIconPickerClient = func() kitty.KittyClient { return client }
	loadIconPickerConfig = func() (*config.Config, error) {
		t.Fatal("config should not load when an icon picker is already open")
		return nil, nil
	}
	runIconPickerOverlay = func(customization.IconContext) error {
		t.Fatal("overlay should not launch when an icon picker is already open")
		return nil
	}

	if err := iconPickerCmd.RunE(iconPickerCmd, nil); err != nil {
		t.Fatalf("icon-picker: %v", err)
	}
	if len(client.focusTabCalls) != 1 || client.focusTabCalls[0] != kitty.MatchTabByID(30) {
		t.Fatalf("FocusTab calls = %#v, want tab 30", client.focusTabCalls)
	}
	if len(client.focusWinCalls) != 1 || client.focusWinCalls[0] != kitty.MatchWindowByID(303) {
		t.Fatalf("FocusWindow calls = %#v, want window 303", client.focusWinCalls)
	}
}

func TestIconPickerCommandReturnsDescriptiveErrors(t *testing.T) {
	t.Run("missing source tab", func(t *testing.T) {
		resetIconPickerCommandTestState(t)
		t.Setenv("KITTY_WINDOW_ID", "404")
		client := &iconPickerCommandClient{}
		newIconPickerClient = func() kitty.KittyClient { return client }

		err := iconPickerCmd.RunE(iconPickerCmd, nil)
		if err == nil || !strings.Contains(err.Error(), "could not find tab for window 404") {
			t.Fatalf("error = %v, want missing source tab detail", err)
		}
	})

	t.Run("config load failure", func(t *testing.T) {
		resetIconPickerCommandTestState(t)
		t.Setenv("KITTY_WINDOW_ID", "202")
		client := &iconPickerCommandClient{windows: []kitty.OSWindow{{Tabs: []kitty.Tab{{
			ID: 20, Title: "shell", Windows: []kitty.Window{{ID: 202}},
		}}}}}
		newIconPickerClient = func() kitty.KittyClient { return client }
		loadIconPickerConfig = func() (*config.Config, error) {
			return nil, errors.New("config unavailable")
		}

		err := iconPickerCmd.RunE(iconPickerCmd, nil)
		if err == nil || !strings.Contains(err.Error(), "load Agent Mux config: config unavailable") {
			t.Fatalf("error = %v, want config-load detail", err)
		}
	})

	t.Run("no configured harness icons", func(t *testing.T) {
		resetIconPickerCommandTestState(t)
		t.Setenv("KITTY_WINDOW_ID", "202")
		client := &iconPickerCommandClient{windows: []kitty.OSWindow{{Tabs: []kitty.Tab{{
			ID: 20, Title: "shell", Windows: []kitty.Window{{ID: 202}},
		}}}}}
		newIconPickerClient = func() kitty.KittyClient { return client }
		loadIconPickerConfig = func() (*config.Config, error) { return &config.Config{}, nil }
		runIconPickerOverlay = customization.RunIconOverlay

		err := iconPickerCmd.RunE(iconPickerCmd, nil)
		if err == nil || !strings.Contains(err.Error(), "no harness icons configured in [harness_emojis]") {
			t.Fatalf("error = %v, want empty harness-emojis detail", err)
		}
	})
}
