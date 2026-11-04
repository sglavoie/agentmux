package dashboard

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
)

// `amx past-sessions` opens Past Sessions itself, on tmux without a desktop workspace, and never the dashboard.
func TestPastSessionsOpensPastSessionsWithoutTheDashboard(t *testing.T) {
	restoreWorkspaceViewSeams(t)
	run, load := runPastSessions, loadRunnableConfig
	dash := runDashboard
	t.Cleanup(func() { runPastSessions, loadRunnableConfig, runDashboard = run, load, dash })
	openTmux = func() (backend.TargetLauncher, error) { return stubTmux{}, nil }
	loadRunnableConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	runDashboard = func(*config.Config, int) error {
		t.Error("past-sessions ran the dashboard")
		return nil
	}
	ran := 0
	runPastSessions = func(cfg *config.Config) error {
		ran++
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
