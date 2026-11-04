package dashboard

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/tui/commands"
)

func TestOverviewUsesTmux(t *testing.T) {
	restoreWorkspaceViewSeams(t)
	oldLoad, oldRun, oldBackend := loadOverviewConfig, runOverview, overviewBackend
	t.Cleanup(func() {
		loadOverviewConfig, runOverview, overviewBackend = oldLoad, oldRun, oldBackend
	})
	openTmux = func() (backend.TargetLauncher, error) { return stubTmux{}, nil }
	loadOverviewConfig = func() (*config.Config, error) { return &config.Config{}, nil }
	overviewBackend = ""
	ran := false
	runOverview = func(_ *config.Config, _ int) error {
		ran = true
		if commands.Portable.Launch != runtimesession.BackendTmux {
			t.Fatalf("overview backend = %q", commands.Portable.Launch)
		}
		return nil
	}
	if err := runOverviewCommand(0); err != nil || !ran {
		t.Fatalf("runOverviewCommand = %v, ran = %t", err, ran)
	}
}

func TestOverviewRejectsUnsupportedBackend(t *testing.T) {
	old := overviewBackend
	t.Cleanup(func() { overviewBackend = old })
	overviewBackend = "unsupported"
	if err := runOverviewCommand(0); err == nil {
		t.Fatal("overview accepted an unsupported backend")
	}
}
