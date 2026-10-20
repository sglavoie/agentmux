package dashboard

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// `amx past-sessions` opens Past Sessions itself, outside Kitty on tmux with
// no Kitty client, and never the dashboard.
func TestPastSessionsOpensPastSessionsWithoutTheDashboard(t *testing.T) {
	restoreWorkspaceViewSeams(t)
	run, load := runPastSessions, loadRunnableConfig
	dash := runDashboard
	t.Cleanup(func() { runPastSessions, loadRunnableConfig, runDashboard = run, load, dash })
	insideKitty = func() bool { return false }
	tmuxServerPresent = func() bool { return false }
	openTmux = func() (backend.TargetLauncher, error) { return stubTmux{}, nil }
	loadRunnableConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	runDashboard = func(*config.Config, kitty.KittyClient, int) error {
		t.Error("past-sessions ran the dashboard")
		return nil
	}
	ran := 0
	runPastSessions = func(cfg *config.Config, client kitty.KittyClient) error {
		ran++
		if client != nil {
			t.Errorf("client = %v; want no Kitty client outside Kitty", client)
		}
		return nil
	}
	pastSessionsCmd.SetArgs(nil)
	if err := pastSessionsCmd.RunE(pastSessionsCmd, nil); err != nil {
		t.Fatalf("past-sessions: %v", err)
	}
	if ran != 1 {
		t.Fatalf("Past Sessions ran %d time(s), want once", ran)
	}
}
