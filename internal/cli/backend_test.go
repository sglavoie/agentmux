package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sglavoie/agentmux/internal/actions"
	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
)

func launchCommand(t *testing.T, value string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "launch"}
	AddBackendFlag(cmd)
	if value != "" {
		if err := cmd.Flags().Set(BackendFlag, value); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
}

func configWithBackend(value string) *config.Config {
	return &config.Config{Defaults: config.Defaults{LaunchBackend: value}}
}

func TestLaunchBackendComesFromTheFlagThenTheConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		flag       string
		configured *config.Config
		want       runtimesession.BackendKind
	}{
		{name: "nothing set keeps Kitty", want: runtimesession.BackendKitty},
		{name: "an empty configuration keeps Kitty", configured: configWithBackend(""), want: runtimesession.BackendKitty},
		{name: "configured tmux", configured: configWithBackend("tmux"), want: runtimesession.BackendTmux},
		{name: "the flag overrides configured tmux", flag: "kitty", configured: configWithBackend("tmux"), want: runtimesession.BackendKitty},
		{name: "the flag overrides configured Kitty", flag: "TMUX", configured: configWithBackend("kitty"), want: runtimesession.BackendTmux},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SelectedBackend(launchCommand(t, tc.flag), tc.configured)
			if err != nil {
				t.Fatalf("SelectedBackend: %v", err)
			}
			if got != tc.want {
				t.Fatalf("backend = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestACommandWithoutTheFlagFollowsTheConfiguration(t *testing.T) {
	got, err := SelectedBackend(&cobra.Command{Use: "reopen"}, configWithBackend("tmux"))
	if err != nil || got != runtimesession.BackendTmux {
		t.Fatalf("SelectedBackend = %q, %v", got, err)
	}
}

func TestAnUnknownBackendIsRefused(t *testing.T) {
	_, err := SelectedBackend(launchCommand(t, "ghostty"), nil)
	var refused *actions.Error
	if !errors.As(err, &refused) || refused.Code != "invalid_backend" {
		t.Fatalf("SelectedBackend = %v, want invalid_backend", err)
	}
}

func replaceTmuxBackend(t *testing.T, open func() (backend.TargetLauncher, error)) {
	t.Helper()
	previous := NewTmuxBackend
	NewTmuxBackend = open
	t.Cleanup(func() { NewTmuxBackend = previous })
}

func TestSelectingKittyNeverOpensTmux(t *testing.T) {
	replaceTmuxBackend(t, func() (backend.TargetLauncher, error) {
		t.Fatal("a Kitty launch opened the tmux backend")
		return nil, nil
	})
	deps := actions.Dependencies{Config: configWithBackend("")}
	if err := SelectBackend(launchCommand(t, ""), &deps); err != nil {
		t.Fatalf("SelectBackend: %v", err)
	}
	if deps.Backend != runtimesession.BackendKitty || deps.Tmux != nil {
		t.Fatalf("deps = %+v", deps)
	}
}

func TestAMissingTmuxFailsTheLaunchWithWhatToDo(t *testing.T) {
	replaceTmuxBackend(t, func() (backend.TargetLauncher, error) {
		return nil, &tmux.NoRuntimeError{Candidates: []tmux.Candidate{
			{Source: tmux.SourceBundled, Problem: "not present (this amx was not started by Agent Mux.app)"},
			{Source: tmux.SourcePath, Problem: "tmux not found"},
		}}
	})
	deps := actions.Dependencies{Config: configWithBackend("tmux")}
	err := SelectBackend(launchCommand(t, ""), &deps)
	var refused *actions.Error
	if !errors.As(err, &refused) || refused.Code != "backend_unavailable" {
		t.Fatalf("SelectBackend = %v, want backend_unavailable", err)
	}
	for _, want := range []string{"tmux backend unavailable", "PATH: tmux not found", "brew install tmux", "[defaults.tmux] path", "Agent Mux.app"} {
		if !strings.Contains(refused.Message, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, refused.Message)
		}
	}
	if !tmux.IsNoRuntime(err) {
		t.Fatalf("the cause was dropped from %v", err)
	}
}

// probe is a machine with a usable tmux and no server.
func probe() BackendProbe {
	return BackendProbe{
		Resolve: func() (tmux.Runtime, []tmux.Candidate, error) {
			rt := tmux.Runtime{Binary: "/runtime/tmux-3.7c/bin/tmux", Version: "3.7c", Source: tmux.SourceBundled, Verified: true}
			return rt, []tmux.Candidate{{Source: tmux.SourceBundled, Path: rt.Binary, Version: rt.Version}}, nil
		},
		ServerDir: func() string { return "/tmp/agent-mux-test" },
		Listening: func(string) bool { return false },
		Pinned:    func(string) string { return "" },
		Survey: func() ([]backend.Pane, string, error) {
			return nil, "", errors.New("no server was running, so nothing may ask it for panes")
		},
	}
}

func problemCodes(status BackendStatus) []string {
	codes := make([]string, 0, len(status.Problems))
	for _, problem := range status.Problems {
		codes = append(codes, problem.Severity+":"+problem.Code)
	}
	return codes
}

func TestBackendStatusOfAWorkingSetupHasNoProblems(t *testing.T) {
	status := CollectBackendStatus(configWithBackend("tmux"), nil, probe())
	if !status.Usable() || len(status.Problems) != 0 {
		t.Fatalf("status = %+v", status)
	}
	if status.LaunchBackend != "tmux" || status.Tmux.Source != "bundled" || !status.Tmux.Verified || status.Tmux.Version != "3.7c" {
		t.Fatalf("status = %+v", status)
	}
	if status.Server.Running || status.Server.Socket != "/tmp/agent-mux-test/tmux.sock" {
		t.Fatalf("server = %+v", status.Server)
	}
}

func TestBackendStatusWithoutTmuxSaysWhatToInstall(t *testing.T) {
	missing := probe()
	missing.Resolve = func() (tmux.Runtime, []tmux.Candidate, error) {
		candidates := []tmux.Candidate{{Source: tmux.SourcePath, Problem: "tmux not found"}}
		return tmux.Runtime{}, candidates, &tmux.NoRuntimeError{Candidates: candidates}
	}

	// Launching on tmux cannot work: an error.
	status := CollectBackendStatus(configWithBackend("tmux"), nil, missing)
	if status.Usable() || status.Tmux.Available {
		t.Fatalf("status = %+v", status)
	}
	if got := problemCodes(status); len(got) != 1 || got[0] != "error:tmux_unavailable" {
		t.Fatalf("problems = %v", got)
	}
	problem := status.Problems[0]
	if !strings.Contains(problem.Message, "PATH: tmux not found") || strings.Contains(problem.Message, "brew install") {
		t.Fatalf("message = %q: it names what was tried and leaves the fix to the hint", problem.Message)
	}
	if problem.Hint != tmux.RuntimeHint {
		t.Fatalf("hint = %q", problem.Hint)
	}
	if len(status.Tmux.Candidates) != 1 {
		t.Fatalf("candidates = %+v", status.Tmux.Candidates)
	}

	// An operator who launches into Kitty is only warned.
	kitty := CollectBackendStatus(nil, nil, missing)
	if got := problemCodes(kitty); len(got) != 1 || got[0] != "warning:tmux_unavailable" {
		t.Fatalf("problems for a Kitty setup = %v", got)
	}
	if kitty.Usable() {
		t.Fatal("a setup without tmux reports the tmux backend as usable")
	}
}

func TestBackendStatusReportsAnUnreadableConfiguration(t *testing.T) {
	status := CollectBackendStatus(nil, errors.New("defaults.launch_backend \"ghostty\": must be \"kitty\" or \"tmux\""), probe())
	if got := problemCodes(status); len(got) != 1 || got[0] != "error:config_invalid" {
		t.Fatalf("problems = %v", got)
	}
	if status.Usable() {
		t.Fatal("an unreadable configuration reports a usable setup")
	}
	if !strings.Contains(status.Problems[0].Hint, "amx config validate") {
		t.Fatalf("hint = %q", status.Problems[0].Hint)
	}
	if status.LaunchBackend != "kitty" {
		t.Fatalf("launch backend = %q", status.LaunchBackend)
	}
}

func runningServer(pinned string, panes []backend.Pane, err error) BackendProbe {
	p := probe()
	p.Listening = func(string) bool { return true }
	p.Pinned = func(string) string { return pinned }
	p.Survey = func() ([]backend.Pane, string, error) { return panes, "4242-1760000000", err }
	return p
}

func TestBackendStatusCountsSessionsAndRunningAgents(t *testing.T) {
	panes := []backend.Pane{
		{Ref: backend.PaneID("%0"), Group: backend.GroupID("$0")},
		{Ref: backend.PaneID("%1"), Group: backend.GroupID("$1"), Exit: &backend.Exit{Status: 0}},
		{Ref: backend.PaneID("%2"), Group: backend.GroupID("$2")},
	}
	status := CollectBackendStatus(nil, nil, runningServer("/runtime/tmux-3.7c/bin/tmux", panes, nil))
	if !status.Server.Running || status.Server.Sessions != 3 || status.Server.Agents != 2 || status.Server.Generation != "4242-1760000000" {
		t.Fatalf("server = %+v", status.Server)
	}
	if len(status.Problems) != 0 {
		t.Fatalf("problems = %v", problemCodes(status))
	}
}

func TestBackendStatusSaysALiveServerKeepsItsTmux(t *testing.T) {
	status := CollectBackendStatus(nil, nil, runningServer("/runtime/tmux-3.6-old/bin/tmux", nil, nil))
	if got := problemCodes(status); len(got) != 1 || got[0] != "info:server_keeps_previous_tmux" {
		t.Fatalf("problems = %v", got)
	}
	if !status.Usable() {
		t.Fatal("a server that keeps its tmux is reported as unusable")
	}
	if status.Server.Binary != "/runtime/tmux-3.6-old/bin/tmux" || status.Tmux.Path != "/runtime/tmux-3.7c/bin/tmux" {
		t.Fatalf("status = %+v", status)
	}
}

func TestBackendStatusReportsAServerWithNoRecordedTmux(t *testing.T) {
	status := CollectBackendStatus(nil, nil, runningServer("", nil, nil))
	if got := problemCodes(status); len(got) != 1 || got[0] != "warning:server_binary_unknown" {
		t.Fatalf("problems = %v", got)
	}
}

func TestBackendStatusReportsAServerThatDoesNotAnswer(t *testing.T) {
	status := CollectBackendStatus(configWithBackend("tmux"), nil, runningServer("/runtime/tmux-3.7c/bin/tmux", nil, errors.New("timed out after 5s")))
	if got := problemCodes(status); len(got) != 1 || got[0] != "error:server_unreadable" {
		t.Fatalf("problems = %v", got)
	}
	if status.Usable() {
		t.Fatal("an unanswering server reports a usable setup")
	}
}

func TestBackendStatusWithALiveServerAndNoTmuxForTheNextOne(t *testing.T) {
	live := runningServer("/runtime/tmux-3.7c/bin/tmux", []backend.Pane{{Ref: backend.PaneID("%0"), Group: backend.GroupID("$0")}}, nil)
	live.Resolve = func() (tmux.Runtime, []tmux.Candidate, error) {
		candidates := []tmux.Candidate{{Source: tmux.SourcePath, Problem: "tmux not found"}}
		return tmux.Runtime{}, candidates, &tmux.NoRuntimeError{Candidates: candidates}
	}
	status := CollectBackendStatus(configWithBackend("tmux"), nil, live)
	if got := problemCodes(status); len(got) != 1 || got[0] != "warning:tmux_unavailable" {
		t.Fatalf("problems = %v: launches reach the running server, so this is a warning", got)
	}
	if !strings.Contains(status.Problems[0].Message, "while the running server lasts") {
		t.Fatalf("message = %q", status.Problems[0].Message)
	}
	if !status.Usable() || status.Tmux.Available || status.Server.Agents != 1 {
		t.Fatalf("status = %+v", status)
	}
}
