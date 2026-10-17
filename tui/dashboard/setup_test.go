package dashboard

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestSetupGuidanceTracksTheNextUnfinishedStep(t *testing.T) {
	cfg := &config.Config{}
	if got := SetupGuidance(cfg, nil); !got.Blocked || !strings.Contains(got.Detail, "agent profile") {
		t.Fatalf("profile: %+v", got)
	}
	cfg.Profiles = []config.Account{{Name: "work"}}
	if got := SetupGuidance(cfg, nil); !got.Blocked || !strings.Contains(got.Detail, "project directory") {
		t.Fatalf("project: %+v", got)
	}
	cfg.Projects = []config.Session{{Title: "api"}}
	unknown := []HealthIssue{{ID: HealthIssueDaemon, State: HealthUnknown}}
	if got := SetupGuidance(cfg, unknown); got.Blocked || !strings.Contains(got.Detail, "launcher") {
		t.Fatalf("ready: %+v", got)
	}
	failed := []HealthIssue{{ID: HealthIssueTopology, State: HealthFailed, Summary: "The session server could not be reached."}}
	guidance := SetupGuidance(cfg, failed)
	if !guidance.Blocked || !strings.Contains(guidance.Detail, "Show Health Details") {
		t.Fatalf("backend: %+v", guidance)
	}
	for _, width := range []int{40, 80, 120} {
		out := renderEmptyHero(ListInput{Setup: guidance, Width: width})
		if strings.Contains(ansi.Strip(out), "No running sessions") {
			t.Fatal("failed read was presented as empty")
		}
		for _, line := range strings.Split(out, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatalf("width %d: %q", width, line)
			}
		}
	}
}
