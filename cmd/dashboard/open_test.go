package dashboard

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/cli"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// recordingDashboardKitty is a Kitty that grows: a LaunchTab adds a tab whose
// window runs the launched command line, so a dashboard launched by one
// open-dashboard is what the next one finds. startDelay holds the new tab's
// process back for that many listings, the way a real process takes a moment
// to appear.
type recordingDashboardKitty struct {
	windows        []kitty.OSWindow
	listErr        error
	focusTabErr    error
	configErr      error // what the configuration loader reports
	startDelay     int
	noProcess      bool
	launched       []kitty.LaunchOpts
	focusedTabs    []string
	focusedWindows []string
	pendingListing int
	pendingProcess []string
}

func (c *recordingDashboardKitty) ListWindows() ([]kitty.OSWindow, error) {
	if c.listErr != nil {
		return nil, c.listErr
	}
	if c.pendingProcess != nil {
		if c.pendingListing <= 0 {
			tabs := c.windows[0].Tabs
			last := &tabs[len(tabs)-1].Windows[0]
			last.ForegroundProcesses = []kitty.ForegroundProcess{{Cmdline: c.pendingProcess}}
			c.pendingProcess = nil
		}
		c.pendingListing--
	}
	return c.windows, nil
}

func (c *recordingDashboardKitty) LaunchTab(opts kitty.LaunchOpts) error {
	c.launched = append(c.launched, opts)
	id := len(c.launched)
	tab := kitty.Tab{ID: 700 + id, Title: opts.Title, Windows: []kitty.Window{{ID: 7000 + id}}}
	if len(c.windows) == 0 {
		c.windows = []kitty.OSWindow{{ID: 1}}
	}
	c.windows[0].Tabs = append(c.windows[0].Tabs, tab)
	if !c.noProcess {
		c.pendingProcess = opts.Cmdline
		c.pendingListing = c.startDelay
	}
	return nil
}

func (c *recordingDashboardKitty) FocusTab(matchExpr string) error {
	c.focusedTabs = append(c.focusedTabs, matchExpr)
	return c.focusTabErr
}

func (c *recordingDashboardKitty) FocusWindow(matchExpr string) error {
	c.focusedWindows = append(c.focusedWindows, matchExpr)
	return nil
}

func (*recordingDashboardKitty) SetUserVars(string, map[string]string) error { return nil }
func (*recordingDashboardKitty) LaunchOverlay(kitty.LaunchOpts) error        { return nil }
func (*recordingDashboardKitty) CloseTab(string) error                       { return nil }
func (*recordingDashboardKitty) MoveTab(string, int) error                   { return nil }
func (*recordingDashboardKitty) SetTabColor(string, string, string, string, string) error {
	return nil
}
func (*recordingDashboardKitty) SetTabTitle(string, string) error       { return nil }
func (*recordingDashboardKitty) GetText(string, string) (string, error) { return "", nil }
func (*recordingDashboardKitty) LaunchOverlayPager(string) error        { return nil }

func (c *recordingDashboardKitty) dashboardCount() int {
	count := 0
	for _, osWindow := range c.windows {
		for _, tab := range osWindow.Tabs {
			for _, window := range tab.Windows {
				for _, process := range window.ForegroundProcesses {
					if len(process.Cmdline) > 0 && strings.HasSuffix(process.Cmdline[0], "/amx") {
						count++
					}
				}
			}
		}
	}
	return count
}

// runOpenDashboardWith runs the real RunE against client, outside Kitty (no
// KITTY_WINDOW_ID), with every seam restored afterwards.
func runOpenDashboardWith(t *testing.T, client *recordingDashboardKitty, jsonOut bool, sourceWindowID int) (string, error) {
	t.Helper()
	t.Setenv(cli.WindowIDEnv, "")
	oldClient, oldConfig, oldBinary, oldSleep := newOpenDashboardClient, loadOpenDashboardConfig, openDashboardBinary, openDashboardSleep
	oldJSON, oldSource := openDashboardJSON, openDashboardSourceWindowID
	t.Cleanup(func() {
		newOpenDashboardClient, loadOpenDashboardConfig, openDashboardBinary, openDashboardSleep = oldClient, oldConfig, oldBinary, oldSleep
		openDashboardJSON, openDashboardSourceWindowID = oldJSON, oldSource
		openDashboardCmd.SetOut(nil)
	})
	newOpenDashboardClient = func() kitty.KittyClient { return client }
	loadOpenDashboardConfig = func() (*config.Config, error) { return &config.Config{}, client.configErr }
	openDashboardBinary = func() (string, error) { return "/usr/local/bin/amx", nil }
	openDashboardSleep = func(time.Duration) {}
	openDashboardJSON, openDashboardSourceWindowID = jsonOut, sourceWindowID
	var out bytes.Buffer
	openDashboardCmd.SetOut(&out)
	err := openDashboardCmd.RunE(openDashboardCmd, nil)
	return out.String(), err
}

func shellOnly() []kitty.OSWindow {
	return []kitty.OSWindow{{ID: 1, Tabs: []kitty.Tab{{ID: 10, Title: "shell", Windows: []kitty.Window{{ID: 100}}}}}}
}

func TestOpenDashboardTwiceLeavesExactlyOneDashboard(t *testing.T) {
	client := &recordingDashboardKitty{windows: shellOnly(), startDelay: 2}

	first, err := runOpenDashboardWith(t, client, true, 0)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if got := strings.TrimSpace(first); got != `{"schema_version":1,"data":{"window_id":7001,"tab_id":701,"opened":true}}` {
		t.Fatalf("first json = %s", got)
	}
	if len(client.launched) != 1 {
		t.Fatalf("launched %d tabs, want 1", len(client.launched))
	}
	if opts := client.launched[0]; opts.Title != dashboardTabTitle || len(opts.Cmdline) != 1 || opts.Cmdline[0] != "/usr/local/bin/amx" {
		t.Fatalf("launch opts = %+v, want a bare amx dashboard tab", opts)
	}

	second, err := runOpenDashboardWith(t, client, true, 0)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if got := strings.TrimSpace(second); got != `{"schema_version":1,"data":{"window_id":7001,"tab_id":701,"opened":false}}` {
		t.Fatalf("second json = %s", got)
	}
	if len(client.launched) != 1 || client.dashboardCount() != 1 {
		t.Fatalf("after two runs: %d launches, %d dashboards; want exactly one", len(client.launched), client.dashboardCount())
	}
	if len(client.focusedTabs) != 1 || client.focusedTabs[0] != kitty.MatchTabByID(701) {
		t.Fatalf("focused tabs = %v, want the dashboard tab once", client.focusedTabs)
	}
}

func TestOpenDashboardPassesTheSourceWindow(t *testing.T) {
	client := &recordingDashboardKitty{windows: shellOnly()}
	if _, err := runOpenDashboardWith(t, client, false, 100); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(client.launched[0].Cmdline, " "); got != "/usr/local/bin/amx --window-id 100" {
		t.Fatalf("cmdline = %q, want the source window handed over", got)
	}
}

// A tab whose process never shows within the wait is still the one the launch
// opened, so it is reported rather than refused.
func TestOpenDashboardFallsBackToTheNewTitledTab(t *testing.T) {
	client := &recordingDashboardKitty{windows: shellOnly(), noProcess: true}
	out, err := runOpenDashboardWith(t, client, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Opened the Agent Mux dashboard (tab 701, window 7001)") {
		t.Fatalf("text = %q", out)
	}
}

func TestOpenDashboardReportsAListingFailureInsteadOfLaunching(t *testing.T) {
	client := &recordingDashboardKitty{listErr: errors.New("no socket")}
	out, err := runOpenDashboardWith(t, client, true, 0)
	if err == nil {
		t.Fatal("want an error")
	}
	if len(client.launched) != 0 {
		t.Fatal("a failed listing launched a dashboard")
	}
	if !strings.Contains(out, `"code":"kitty_unavailable"`) {
		t.Fatalf("json = %s", out)
	}
}

func TestOpenDashboardReportsAFocusFailureInsteadOfLaunchingASecond(t *testing.T) {
	windows := shellOnly()
	windows[0].Tabs = append(windows[0].Tabs, kitty.Tab{ID: 90, Windows: []kitty.Window{{ID: 901, ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"amx"}}}}}})
	client := &recordingDashboardKitty{windows: windows, focusTabErr: errors.New("gone")}
	out, err := runOpenDashboardWith(t, client, true, 0)
	if err == nil {
		t.Fatal("want an error")
	}
	if len(client.launched) != 0 || !strings.Contains(out, `"code":"focus_failed"`) {
		t.Fatalf("launched %d, json = %s; want focus_failed and no launch", len(client.launched), out)
	}
}

func TestOpenDashboardRefusesAnInvalidConfigBeforeLaunching(t *testing.T) {
	client := &recordingDashboardKitty{windows: shellOnly(), configErr: errors.New("bad config")}
	out, err := runOpenDashboardWith(t, client, true, 0)
	if err == nil {
		t.Fatal("want an error")
	}
	if len(client.launched) != 0 || !strings.Contains(out, `"code":"config_invalid"`) {
		t.Fatalf("launched %d, json = %s", len(client.launched), out)
	}
}

func TestOpenDashboardDeclaresJSON(t *testing.T) {
	if openDashboardCmd.Flags().Lookup("json") == nil {
		t.Fatal("open-dashboard does not declare --json")
	}
}
