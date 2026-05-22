package session_test

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestRestartWithAccount_SwitchesAccount verifies that RestartWithAccount
// relaunches with the same project title and the target account user var.
func TestRestartWithAccount_SwitchesAccount(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	running := session.RunningSession{
		TabID:     42,
		WindowID:  43,
		Title:     "W:api",
		Base:      "W:api",
		Account:   "work",
		Directory: "/Users/user/projects/api",
	}
	targetAcc := cfg.Accounts[1] // personal

	err := session.RestartWithAccount(client, running, targetAcc, cfg)
	if err != nil {
		t.Fatalf("RestartWithAccount: %v", err)
	}

	closeTabFound := false
	var launchArgs []string
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a == "close-tab" {
				closeTabFound = true
			}
			if a == "launch" {
				launchArgs = c.Args
			}
		}
	}

	if !closeTabFound {
		t.Error("expected kitten @ close-tab to be called")
	}
	if launchArgs == nil {
		t.Fatal("expected kitten @ launch to be called")
	}

	argSet := make(map[string]bool)
	for _, a := range launchArgs {
		argSet[a] = true
	}
	if !argSet["--tab-title=api"] {
		t.Errorf("expected --tab-title=api in launch args: %v", launchArgs)
	}
	if !argSet["--var=cc_account=personal"] {
		t.Errorf("expected --var=cc_account=personal in launch args: %v", launchArgs)
	}
}

// TestRestartWithAccount_NewSessionPicksUpTargetAccountArgs verifies that
// switching profiles recomputes inherited profile args when the running session
// has no project-explicit args.
func TestRestartWithAccount_NewSessionPicksUpTargetAccountArgs(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha"},
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work", Color: "blue", Args: []string{"--work-flag"}},
			{Name: "personal", ConfigDir: "~/.config/claude", Color: "green", Args: []string{"--personal-flag"}},
		},
	}

	running := session.RunningSession{
		TabID:            42,
		Base:             "W:api",
		Account:          "work",
		Directory:        "/tmp/api",
		Flags:            "--work-flag",
		ExplicitFlags:    "",
		ExplicitFlagsSet: true,
	}
	targetAcc := cfg.Accounts[1] // personal

	err := session.RestartWithAccount(client, running, targetAcc, cfg)
	if err != nil {
		t.Fatalf("RestartWithAccount: %v", err)
	}

	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a != "launch" {
				continue
			}
			for _, arg := range c.Args {
				if arg == "--var=cc_flags=--personal-flag" {
					return
				}
			}
			t.Errorf("expected --var=cc_flags=--personal-flag in launch args: %v", c.Args)
			return
		}
	}
	t.Error("expected kitten @ launch to be called")
}

func TestRestartWithAccount_MixedHarnessRecomputesTargetProfileArgs(t *testing.T) {
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

	running := session.RunningSession{
		TabID:            42,
		Base:             "W:docs",
		Account:          "claude-work",
		Profile:          "claude-work",
		Harness:          "claude",
		Directory:        "/tmp/docs",
		Flags:            "--dangerously-skip-permissions",
		ExplicitFlags:    "",
		ExplicitFlagsSet: true,
	}
	targetAcc := cfg.Accounts[1]

	err := session.RestartWithAccount(client, running, targetAcc, cfg)
	if err != nil {
		t.Fatalf("RestartWithAccount: %v", err)
	}

	expectedHome := "--env=COPILOT_HOME=" + config.ExpandTilde("~/.copilot-work")
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
				if strings.Contains(arg, "exec copilot --model gpt-5") {
					sawCopilot = true
				}
				if strings.Contains(arg, "--dangerously-skip-permissions") {
					t.Errorf("unexpected Claude-only flag in Copilot restart args: %v", c.Args)
				}
				if strings.Contains(arg, "--shared-default") {
					t.Errorf("unexpected shared defaults override in Copilot restart args: %v", c.Args)
				}
			}
			if !sawCopilot {
				t.Errorf("expected Copilot executable in launch args: %v", c.Args)
			}
			return
		}
	}
	t.Error("expected kitten @ launch to be called")
}

func TestRestartWithAccount_LegacySessionPreservesEffectiveArgs(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha"},
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work", Color: "blue", Args: []string{"--work-flag"}},
			{Name: "personal", ConfigDir: "~/.config/claude-personal", Color: "green", Args: []string{"--personal-flag"}},
		},
	}

	running := session.RunningSession{
		TabID:     42,
		Base:      "W:api",
		Account:   "work",
		Directory: "/tmp/api",
		Flags:     "--work-flag",
	}
	targetAcc := cfg.Accounts[1]

	err := session.RestartWithAccount(client, running, targetAcc, cfg)
	if err != nil {
		t.Fatalf("RestartWithAccount: %v", err)
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
			if !argSet["--var=cc_flags=--work-flag"] {
				t.Errorf("expected legacy effective args to be preserved: %v", c.Args)
			}
			if argSet["--var=cc_flags=--personal-flag"] {
				t.Errorf("expected target profile args to be ignored for legacy sessions: %v", c.Args)
			}
			return
		}
	}
	t.Error("expected kitten @ launch to be called")
}

// TestRestartWithAccount_SameAccountNoOp verifies that selecting the same account
// returns nil without sending any kitten commands.
func TestRestartWithAccount_SameAccountNoOp(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	running := session.RunningSession{
		TabID:   42,
		Base:    "W:api",
		Account: "work",
	}
	sameAcc := cfg.Accounts[0] // work

	err := session.RestartWithAccount(client, running, sameAcc, cfg)
	if err != nil {
		t.Fatalf("RestartWithAccount same account: %v", err)
	}
	if len(runner.Calls) != 0 {
		t.Errorf("expected no kitten calls for same-account no-op, got: %v", runner.Calls)
	}
}

// TestRestartWithAccount_CaseInsensitiveSameAccountNoOp verifies that the
// no-op short-circuit when restarting under the same profile is case-insensitive.
// Without this, a running tab whose user var case drifted from the config
// would trigger a needless relaunch+close even though the profile is the same.
func TestRestartWithAccount_CaseInsensitiveSameAccountNoOp(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig() // profile "work"

	running := session.RunningSession{
		TabID:   42,
		Base:    "W:api",
		Account: "WORK", // user var case differs from config
	}
	sameAcc := cfg.Accounts[0] // Name: "work"

	if err := session.RestartWithAccount(client, running, sameAcc, cfg); err != nil {
		t.Fatalf("RestartWithAccount case-insensitive no-op: %v", err)
	}
	if len(runner.Calls) != 0 {
		t.Errorf("expected no kitten calls for case-insensitive no-op, got: %v", runner.Calls)
	}
}

// TestRestartWithAccount_PreservesMetadata verifies that directory, flags, env,
// and groups survive an account switch.
func TestRestartWithAccount_PreservesMetadata(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	ccEnv := "DEBUG=1\x1fNODE_ENV=production"
	running := session.RunningSession{
		TabID:     55,
		Base:      "W:backend",
		Account:   "work",
		Directory: "/tmp/backend",
		Flags:     "--dangerously-skip-permissions",
		Env:       ccEnv,
		Groups:    []string{"backend"},
	}
	targetAcc := cfg.Accounts[1] // personal

	err := session.RestartWithAccount(client, running, targetAcc, cfg)
	if err != nil {
		t.Fatalf("RestartWithAccount: %v", err)
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
			if !argSet["--var=cc_flags=--dangerously-skip-permissions"] {
				t.Errorf("expected flags preserved: %v", c.Args)
			}
			if !argSet["--var=cc_groups=backend"] {
				t.Errorf("expected groups preserved: %v", c.Args)
			}
			return
		}
	}
	t.Error("expected kitten @ launch to be called")
}

// TestRestartWithAccount_DedupRaceSkipped verifies that RestartWithAccount
// launches even when a session with the target title already exists in ls.
func TestRestartWithAccount_DedupRaceSkipped(t *testing.T) {
	stale := loadFixture(t, "kitty_ls.json") // contains P:dotfiles
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, stale, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	running := session.RunningSession{
		TabID:     42,
		WindowID:  43,
		Title:     "W:dotfiles",
		Base:      "W:dotfiles",
		Account:   "work",
		Directory: "/Users/user/dotfiles",
	}
	targetAcc := cfg.Accounts[1] // personal → P:dotfiles already in fixture

	err := session.RestartWithAccount(client, running, targetAcc, cfg)
	if err != nil {
		t.Fatalf("RestartWithAccount: %v", err)
	}

	launchFound := false
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a == "launch" {
				launchFound = true
			}
		}
	}
	if !launchFound {
		t.Error("expected kitten @ launch despite stale P:dotfiles in ls")
	}
}
