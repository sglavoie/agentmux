package dashboard

import (
	"fmt"
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/tui/commands"
)

// Exercise the actual command entry points: standalone history and overview
// must honor configuration and flags just as the main dashboard does.
func TestStandaloneSurfacesShareLaunchPrecedence(t *testing.T) {
	for _, surface := range []string{"dashboard", "overview", "history"} {
		for _, tc := range []struct{ name, configured, flag, want, source string }{
			{name: "default", want: "tmux", source: "default"},
			{name: "configured tmux", configured: "tmux", want: "tmux", source: "config.toml"},
			{name: "flag tmux", configured: "tmux", flag: "tmux", want: "tmux", source: "--backend"},
		} {
			t.Run(fmt.Sprintf("%s/%s", surface, tc.name), func(t *testing.T) {
				restoreWorkspaceViewSeams(t)
				oldLoad, oldOverviewLoad := loadRunnableConfig, loadOverviewConfig
				oldDashboard, oldOverview, oldHistory := runDashboard, runOverview, runPastSessions
				oldHistoryBackend, oldHistoryView, oldOverviewView := pastSessionsBackend, pastSessionsView, overviewView
				t.Cleanup(func() {
					loadRunnableConfig, loadOverviewConfig = oldLoad, oldOverviewLoad
					runDashboard, runOverview, runPastSessions = oldDashboard, oldOverview, oldHistory
					pastSessionsBackend, pastSessionsView, overviewView = oldHistoryBackend, oldHistoryView, oldOverviewView
				})
				desktopViews = nil
				opened := 0
				openTmux = func() (backend.TargetLauncher, error) { opened++; return stubTmux{}, nil }
				cfg := &config.Config{Defaults: config.Defaults{LaunchBackend: tc.configured}}
				loadRunnableConfig = func() (*config.Config, error) { return cfg, nil }
				loadOverviewConfig = loadRunnableConfig
				ran := 0
				run := func(_ *config.Config, _ int) error {
					ran++
					if commands.Portable.Launch != runtimesession.BackendKind(tc.want) || commands.Portable.LaunchSource != tc.source {
						t.Fatalf("launch = %s (%s), want %s (%s)", commands.Portable.Launch, commands.Portable.LaunchSource, tc.want, tc.source)
					}
					return nil
				}
				runDashboard = run
				runOverview = run
				runPastSessions = func(c *config.Config) error { return run(c, 0) }
				overviewBackend, pastSessionsBackend = tc.flag, tc.flag
				overviewView, pastSessionsView = View{}, View{}
				var err error
				switch surface {
				case "dashboard":
					err = Run(tc.flag, View{})
				case "overview":
					err = runOverviewCommand(0)
				case "history":
					err = pastSessionsCmd.RunE(pastSessionsCmd, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				if ran != 1 || opened != 1 {
					t.Fatalf("surface starts=%d, tmux opens=%d", ran, opened)
				}
			})
		}
	}
}
