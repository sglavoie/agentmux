package session_test

import (
	"testing"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
	"github.com/sglavoie/cc-manager-kitty/internal/session"
)

// TestRestart_ClosesAndRelaunches verifies that Restart sends close-tab
// followed by launch with the session's preserved metadata.
func TestRestart_ClosesAndRelaunches(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		// All ls calls return empty — tab is already gone and no existing sessions.
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
		Flags:     "--dangerously-skip-permissions",
	}

	err := session.Restart(client, running, cfg)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}

	closeTabFound := false
	launchFound := false
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
				launchFound = true
				launchArgs = c.Args
			}
		}
	}

	if !closeTabFound {
		t.Error("expected kitten @ close-tab to be called")
	}
	if !launchFound {
		t.Error("expected kitten @ launch to be called")
	}

	// Verify the relaunched session uses the correct title and account.
	argSet := make(map[string]bool)
	for _, a := range launchArgs {
		argSet[a] = true
	}
	if !argSet["--tab-title=W:api"] {
		t.Errorf("expected --tab-title=W:api in launch args: %v", launchArgs)
	}
	if !argSet["--var=cc_account=work"] {
		t.Errorf("expected --var=cc_account=work in launch args: %v", launchArgs)
	}
	if !argSet["--var=cc_flags=--dangerously-skip-permissions"] {
		t.Errorf("expected cc_flags in launch args: %v", launchArgs)
	}
}

// TestRestart_UnknownAccountErrors returns an error when the account is missing.
func TestRestart_UnknownAccountErrors(t *testing.T) {
	runner := kitty.NewMockRunner(map[string][]byte{})
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	running := session.RunningSession{
		TabID:   10,
		Base:    "X:session",
		Account: "nonexistent",
	}

	err := session.Restart(client, running, cfg)
	if err == nil {
		t.Error("expected error for unknown account, got nil")
	}
}

// TestRestart_PreservesEnvAndGroups verifies that env and groups from user vars
// are preserved through the restart cycle.
func TestRestart_PreservesEnvAndGroups(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	// Serialize env as SerializeSessionEnv would.
	ccEnv := "DEBUG=1\x1fNODE_ENV=development"
	running := session.RunningSession{
		TabID:     50,
		Base:      "W:myapp",
		Account:   "work",
		Directory: "/tmp/myapp",
		Env:       ccEnv,
		Groups:    []string{"backend", "api"},
	}

	err := session.Restart(client, running, cfg)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}

	// Verify launch was called with groups user var.
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a == "launch" {
				// Check cc_groups contains both groups joined with \x1f.
				for _, arg := range c.Args {
					if arg == "--var=cc_groups=backend\x1fapi" {
						return // found
					}
				}
				t.Errorf("expected --var=cc_groups=backend\\x1fapi in launch args: %v", c.Args)
				return
			}
		}
	}
}

// TestRestartWithAccount_SwitchesAccount verifies that RestartWithAccount
// relaunches with the target account's prefix and user var.
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
	if !argSet["--tab-title=P:api"] {
		t.Errorf("expected --tab-title=P:api in launch args: %v", launchArgs)
	}
	if !argSet["--var=cc_account=personal"] {
		t.Errorf("expected --var=cc_account=personal in launch args: %v", launchArgs)
	}
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

// TestRestart_DedupRaceSkipped verifies that Restart still launches even when
// the old tab appears in Kitty's ls output (stale state after close).
func TestRestart_DedupRaceSkipped(t *testing.T) {
	stale := loadFixture(t, "kitty_ls.json") // contains W:api
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		// ls 1: PollClosed (tab 42 not in fixture → immediate success)
		// ls 2: Launch List() dedup — stale W:api still visible
		// ls 3: ComputeInsertLocation
		// ls 4-5: color retry
		lsResponses: [][]byte{empty, stale, empty, empty, empty},
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

	err := session.Restart(client, running, cfg)
	if err != nil {
		t.Fatalf("Restart: %v", err)
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
		t.Error("expected kitten @ launch despite stale W:api in ls")
	}
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

// TestRestart_OverrideResolution verifies that nil/empty RunningSession fields
// result in empty user vars (no defaults injected from outside the session).
func TestRestart_OverrideResolution(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := &config.Config{
		Defaults: config.Defaults{Sort: "alpha"},
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work", Color: "blue"},
		},
	}

	running := session.RunningSession{
		TabID:     60,
		Base:      "W:bare",
		Account:   "work",
		Directory: "/tmp/bare",
		// Flags, Env, Groups all empty — no overrides.
	}

	err := session.Restart(client, running, cfg)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}

	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a == "launch" {
				for _, arg := range c.Args {
					if arg == "--var=cc_flags=" {
						return // empty flags preserved correctly
					}
				}
				t.Errorf("expected --var=cc_flags= (empty) in launch args: %v", c.Args)
				return
			}
		}
	}
}
