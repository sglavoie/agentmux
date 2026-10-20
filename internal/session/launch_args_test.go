package session_test

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestLaunch_CallsKittenWithCorrectArgs verifies that Launch calls kitten @ launch
// with the correct tab title and user vars.
func TestLaunch_CallsKittenWithCorrectArgs(t *testing.T) {
	t.Parallel()

	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		// All ls calls return empty — no existing sessions.
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	sess := config.Session{
		Title:       "newproject",
		Account:     "work",
		Directory:   "/tmp/newproject",
		ClaudeFlags: []string{"--dangerously-skip-permissions"},
		Env:         map[string]string{"DEBUG": "1"},
	}
	acc := cfg.Accounts[0]

	err := session.Launch(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	// Verify kitten @ launch was called.
	launchCalled := false
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		hasLaunch := false
		for _, a := range c.Args {
			if a == "launch" {
				hasLaunch = true
			}
		}
		if !hasLaunch {
			continue
		}
		launchCalled = true

		// Check expected args are present.
		argMap := make(map[string]bool)
		for _, a := range c.Args {
			argMap[a] = true
		}
		if !argMap["--tab-title=🦊newproject"] {
			t.Errorf("expected --tab-title=🦊newproject in args: %v", c.Args)
		}
		if !argMap["--var=cc_account=work"] {
			t.Errorf("expected --var=cc_account=work in args: %v", c.Args)
		}
		if !argMap["--var=cc_flags=--dangerously-skip-permissions"] {
			t.Errorf("expected cc_flags arg in args: %v", c.Args)
		}
		expectedHome := config.ExpandTilde("~/.config/claude-work")
		if !argMap["--env=CLAUDE_CONFIG_DIR="+expectedHome] {
			t.Errorf("expected CLAUDE_CONFIG_DIR launch env in args: %v", c.Args)
		}
		sawNativeCommand := false
		for _, arg := range c.Args {
			if strings.Contains(arg, "umask 077 && exec env CLAUDE_CONFIG_DIR="+expectedHome+" claude --dangerously-skip-permissions --debug-file ") {
				sawNativeCommand = true
			}
		}
		if !sawNativeCommand {
			t.Errorf("expected private native-log shell command in args: %v", c.Args)
		}
		// Empty Kitty: no --location flag, Kitty uses its default placement.
		for _, a := range c.Args {
			if strings.HasPrefix(a, "--location") {
				t.Errorf("expected no --location arg for empty kitty, got %q", a)
			}
		}
	}
	if !launchCalled {
		t.Error("expected kitten @ launch to be called")
	}
}

func TestLaunch_ProjectHarnessEnvOverridePinnedAtExec(t *testing.T) {
	t.Parallel()

	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	sess := config.Session{
		Title:     "override-env",
		Account:   "work",
		Directory: "/tmp/override-env",
		Env:       map[string]string{"CLAUDE_CONFIG_DIR": "/tmp/project-claude"},
	}
	acc := cfg.Accounts[0]

	if err := session.Launch(client, sess, acc, cfg); err != nil {
		t.Fatalf("Launch: %v", err)
	}

	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		argSet := make(map[string]bool)
		for _, arg := range c.Args {
			argSet[arg] = true
		}
		if !argSet["launch"] {
			continue
		}
		if !argSet["--env=CLAUDE_CONFIG_DIR=/tmp/project-claude"] {
			t.Errorf("expected project CLAUDE_CONFIG_DIR override in args: %v", c.Args)
		}
		sawNativeCommand := false
		for _, arg := range c.Args {
			if strings.Contains(arg, "umask 077 && exec env CLAUDE_CONFIG_DIR=/tmp/project-claude claude --debug-file ") {
				sawNativeCommand = true
			}
		}
		if !sawNativeCommand {
			t.Errorf("expected private native-log shell command with project CLAUDE_CONFIG_DIR override: %v", c.Args)
		}
		return
	}

	t.Error("expected kitten @ launch to be called")
}

func TestLaunchFreshIncludesHandoffInstruction(t *testing.T) {
	t.Parallel()

	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{lsResponses: [][]byte{empty, empty, empty, empty, empty}}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()
	sess := config.Session{Title: "api", Account: "work", Directory: "/tmp/source-worktree"}
	prompt := `Read the handoff pack at "/tmp/handoff.md" before taking action. Verify all assumptions.`

	if err := session.LaunchFresh(client, sess, cfg.Accounts[0], cfg, prompt); err != nil {
		t.Fatalf("LaunchFresh: %v", err)
	}
	for _, call := range runner.Calls {
		if call.Name != "kitten" || !hasArg(call.Args, "launch") {
			continue
		}
		if !hasArg(call.Args, "--cwd=/tmp/source-worktree") {
			t.Fatalf("launch args = %v, want source worktree CWD", call.Args)
		}
		for _, arg := range call.Args {
			if strings.Contains(arg, "Read the handoff pack") && strings.Contains(arg, "Verify all assumptions") {
				return
			}
		}
		t.Fatalf("launch args = %v, want fresh handoff instruction", call.Args)
	}
	t.Fatal("expected kitten @ launch")
}

// TestLaunch_StoresExplicitFlags verifies that Launch stores project-explicit
// args separately from the effective launch args.
func TestLaunch_StoresExplicitFlags(t *testing.T) {
	t.Parallel()

	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha"},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work", Args: []string{"--account-flag"}},
		},
	}

	sess := config.Session{
		Title:     "explicit",
		Account:   "work",
		Directory: "/tmp/explicit",
		Args:      []string{"--project-flag"},
	}
	acc := cfg.Accounts[0]

	err := session.Launch(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a != "launch" {
				continue
			}
			argSet := make(map[string]bool)
			for _, arg := range c.Args {
				argSet[arg] = true
			}
			if !argSet["--var=cc_flags=--project-flag"] {
				t.Errorf("expected cc_flags=--project-flag, args: %v", c.Args)
			}
			if !argSet["--var=cc_explicit_flags=--project-flag"] {
				t.Errorf("expected cc_explicit_flags=--project-flag, args: %v", c.Args)
			}
			return
		}
	}
	t.Error("expected kitten @ launch to be called")
}

func TestLaunch_MixedHarnessUsesProfileArgs(t *testing.T) {
	t.Parallel()

	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := &config.Config{
		Defaults: config.Defaults{
			Sort: "alpha",
			Args: []string{"--shared-default"},
		},
		Accounts: []config.Account{
			{
				Name:    "claude-work",
				Harness: "claude",
				HomeDir: "~/.config/claude-work",
				Color:   "blue",
				Args:    []string{"--dangerously-skip-permissions"},
			},
			{
				Name:    "copilot-work",
				Harness: "copilot",
				HomeDir: "~/.copilot-work",
				Color:   "green",
				Args:    []string{"--model", "gpt-5"},
			},
		},
	}

	sess := config.Session{
		Title:     "docs",
		Profile:   "copilot-work",
		Account:   "copilot-work",
		Directory: "/tmp/docs",
	}
	acc := cfg.Accounts[1]

	err := session.Launch(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	expectedHome := "--env=COPILOT_HOME=" + config.ExpandTilde("~/.copilot-work")
	launchFound := false
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		hasLaunch := false
		for _, a := range c.Args {
			if a == "launch" {
				hasLaunch = true
				break
			}
		}
		if !hasLaunch {
			continue
		}
		launchFound = true

		argSet := make(map[string]bool)
		for _, a := range c.Args {
			argSet[a] = true
		}
		if !argSet["--var=amx_harness=copilot"] {
			t.Errorf("expected amx_harness=copilot in args: %v", c.Args)
		}
		if !argSet["--var=cc_flags=--model gpt-5"] {
			t.Errorf("expected cc_flags=--model gpt-5 in args: %v", c.Args)
		}
		if !argSet[expectedHome] {
			t.Errorf("expected %q in args: %v", expectedHome, c.Args)
		}

		sawCopilot := false
		for _, arg := range c.Args {
			if strings.Contains(arg, "exec env COPILOT_HOME="+config.ExpandTilde("~/.copilot-work")+" copilot --model gpt-5") {
				sawCopilot = true
			}
			if strings.Contains(arg, "--dangerously-skip-permissions") {
				t.Errorf("unexpected Claude-only flag in Copilot launch args: %v", c.Args)
			}
			if strings.Contains(arg, "--shared-default") {
				t.Errorf("unexpected shared defaults override in Copilot launch args: %v", c.Args)
			}
		}
		if !sawCopilot {
			t.Errorf("expected Copilot executable in launch args: %v", c.Args)
		}
	}
	if !launchFound {
		t.Error("expected kitten @ launch to be called")
	}
}

func TestLaunch_QuotesArgsWithSpacesAndMetacharacters(t *testing.T) {
	t.Parallel()

	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	sess := config.Session{
		Title:     "quoted",
		Account:   "work",
		Directory: "/tmp/quoted",
		Args:      []string{"--prompt", "hello world", "--literal=$HOME", "semi;colon"},
	}
	acc := cfg.Accounts[0]

	err := session.Launch(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	wantArgs := "--prompt 'hello world' '--literal=$HOME' 'semi;colon'"
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		hasLaunch := false
		for _, a := range c.Args {
			if a == "launch" {
				hasLaunch = true
				break
			}
		}
		if !hasLaunch {
			continue
		}

		argSet := make(map[string]bool)
		for _, arg := range c.Args {
			argSet[arg] = true
		}
		if !argSet["--var=cc_flags="+wantArgs] {
			t.Errorf("expected quoted cc_flags in args: %v", c.Args)
		}
		if !argSet["--var=cc_explicit_flags="+wantArgs] {
			t.Errorf("expected quoted cc_explicit_flags in args: %v", c.Args)
		}

		sawQuotedShellCmd := false
		expectedShellCmd := "umask 077 && exec env CLAUDE_CONFIG_DIR=" + config.ExpandTilde("~/.config/claude-work") + " claude " + wantArgs + " --debug-file "
		for _, arg := range c.Args {
			if strings.Contains(arg, expectedShellCmd) {
				sawQuotedShellCmd = true
				break
			}
		}
		if !sawQuotedShellCmd {
			t.Errorf("expected quoted shell command in args: %v", c.Args)
		}
		return
	}

	t.Error("expected kitten @ launch to be called")
}
