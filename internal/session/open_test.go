package session_test

import (
	"testing"

	"github.com/sglavoie/cc-manager-kitty/internal/config"
	"github.com/sglavoie/cc-manager-kitty/internal/kitty"
	"github.com/sglavoie/cc-manager-kitty/internal/session"
)

// TestOpenDirectory_LaunchesWithCorrectArgs verifies that OpenDirectory calls
// kitten @ launch with the correct tab title and cwd but no exec claude command.
func TestOpenDirectory_LaunchesWithCorrectArgs(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	sess := config.Session{
		Title:     "myproject",
		Account:   "work",
		Directory: "/tmp/myproject",
	}
	acc := cfg.Accounts[0]

	err := session.OpenDirectory(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("OpenDirectory: %v", err)
	}

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

		argMap := make(map[string]bool)
		for _, a := range c.Args {
			argMap[a] = true
		}
		if !argMap["--tab-title=W:myproject"] {
			t.Errorf("expected --tab-title=W:myproject in args: %v", c.Args)
		}
		if !argMap["--cwd=/tmp/myproject"] {
			t.Errorf("expected --cwd=/tmp/myproject in args: %v", c.Args)
		}
		// Verify no cc_account user var (unmanaged tab).
		for _, a := range c.Args {
			if a == "--var=cc_account=work" {
				t.Error("expected no --var=cc_account for unmanaged tab")
			}
		}
		// Verify no exec claude in the command line.
		for _, a := range c.Args {
			if a == "exec claude" || a == "claude" {
				t.Error("expected no claude command for plain shell tab")
			}
		}
	}
	if !launchCalled {
		t.Error("expected kitten @ launch to be called")
	}
}

// TestOpenDirectory_ColorApplied verifies that set-tab-color is called
// when the new tab appears in a post-launch ls response.
func TestOpenDirectory_ColorApplied(t *testing.T) {
	empty := loadFixture(t, "kitty_ls_empty.json")

	postLaunch := []byte(`[{"id":1,"is_active":true,"is_focused":true,"last_focused":true,"tabs":[` +
		`{"id":200,"is_active":true,"title":"W:myproject","windows":[` +
		`{"id":201,"is_active":true,"title":"W:myproject","cwd":"/tmp/myproject","cmdline":["zsh"],` +
		`"user_vars":{}}` +
		`]}]}]`)

	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, postLaunch, postLaunch, postLaunch},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()

	sess := config.Session{
		Title:     "myproject",
		Account:   "work",
		Directory: "/tmp/myproject",
	}
	acc := cfg.Accounts[0]

	err := session.OpenDirectory(client, sess, acc, cfg)
	if err != nil {
		t.Fatalf("OpenDirectory: %v", err)
	}

	colorCalled := false
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a == "set-tab-color" {
				colorCalled = true
				break
			}
		}
	}
	if !colorCalled {
		t.Error("expected kitten @ set-tab-color to be called after color retry found new tab")
	}
}
