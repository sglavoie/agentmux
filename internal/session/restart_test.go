package session_test

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
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
	if !argSet["--tab-title=🦊api"] {
		t.Errorf("expected --tab-title=🦊api in launch args: %v", launchArgs)
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

// TestRestart_NewSessionUsesAccountArgs verifies that when a session stores an
// explicit-args marker with no project args, restart recomputes inherited
// profile args instead of pinning the previous effective args as project args.
func TestRestart_NewSessionUsesAccountArgs(t *testing.T) {
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

	running := session.RunningSession{
		TabID:            42,
		Base:             "W:api",
		Account:          "work",
		Directory:        "/tmp/api",
		Flags:            "--account-flag",
		ExplicitFlags:    "",
		ExplicitFlagsSet: true,
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
			if a != "launch" {
				continue
			}
			for _, arg := range c.Args {
				if arg == "--var=cc_flags=--account-flag" {
					return
				}
			}
			t.Errorf("expected --var=cc_flags=--account-flag in launch args: %v", c.Args)
			return
		}
	}
	t.Error("expected kitten @ launch to be called")
}

// TestRestart_CaseInsensitiveProfileLookup verifies that Restart resolves a
// profile name that differs only in case from the config-declared name.
// SessionKey lowercases profile names and config validation rejects case-only
// duplicates, so a running tab whose user var case has drifted from the config
// must still restart cleanly rather than failing with "profile not found".
func TestRestart_CaseInsensitiveProfileLookup(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig() // declares profile "work" (lowercase)

	// Running tab carries the profile name with different case ("WORK").
	running := session.RunningSession{
		TabID:     42,
		Base:      "W:api",
		Account:   "WORK",
		Directory: "/tmp/api",
	}

	if err := session.Restart(client, running, cfg); err != nil {
		t.Fatalf("Restart with case-mismatched profile: %v", err)
	}
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

// TestRestart_OverrideResolution verifies that nil/empty RunningSession fields
// result in empty user vars (no defaults injected from outside the session).
func TestRestart_OverrideResolution(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := &config.Config{
		Defaults:      config.Defaults{Sort: "alpha"},
		HarnessEmojis: map[string]string{"claude": "🦊"},
		Accounts: []config.Account{
			{Name: "work", ConfigDir: "~/.config/claude-work"},
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

func TestRestart_QuotedExplicitArgsRoundTrip(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	quoted := "--prompt 'hello world' '--literal=$HOME' 'semi;colon'"
	running := session.RunningSession{
		TabID:            61,
		Base:             "W:quoted",
		Account:          "work",
		Directory:        "/tmp/quoted",
		ExplicitFlags:    quoted,
		ExplicitFlagsSet: true,
	}

	err := session.Restart(client, running, cfg)
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}

	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		hasLaunch := false
		for _, arg := range c.Args {
			if arg == "launch" {
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
		if !argSet["--var=cc_flags="+quoted] {
			t.Errorf("expected quoted cc_flags to survive restart: %v", c.Args)
		}
		if !argSet["--var=cc_explicit_flags="+quoted] {
			t.Errorf("expected quoted cc_explicit_flags to survive restart: %v", c.Args)
		}

		sawQuotedShellCmd := false
		expectedShellCmd := "umask 077 && exec env CLAUDE_CONFIG_DIR=" + config.ExpandTilde("~/.config/claude-work") + " claude " + quoted + " --debug-file "
		for _, arg := range c.Args {
			if strings.Contains(arg, expectedShellCmd) {
				sawQuotedShellCmd = true
				break
			}
		}
		if !sawQuotedShellCmd {
			t.Errorf("expected quoted shell command in restart args: %v", c.Args)
		}
		return
	}

	t.Error("expected kitten @ launch to be called")
}
