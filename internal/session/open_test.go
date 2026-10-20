package session_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

// TestOpenDirectory_LaunchesWithCorrectArgs verifies that OpenDirectory calls
// kitten @ launch with the correct tab title and cwd but no exec claude command.
func TestOpenDirectory_LaunchesWithCorrectArgs(t *testing.T) {
	t.Parallel()

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
		if !argMap["--tab-title=myproject"] {
			t.Errorf("expected project title without a harness icon in args: %v", c.Args)
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

// TestOpenPathInEditor_LaunchesWithCorrectArgs verifies that OpenPathInEditor
// calls kitten @ launch with the expected title, cwd, and editor cmdline, and
// does not apply any account-derived color.
func TestOpenPathInEditor_LaunchesWithCorrectArgs(t *testing.T) {
	t.Parallel()

	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{
		lsResponses: [][]byte{empty, empty, empty, empty, empty},
	}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()
	cfg.Defaults.Editor = "myeditor --wait"

	err := session.OpenPathInEditor(client, "/tmp/plans/myplan", "myplan", cfg)
	if err != nil {
		t.Fatalf("OpenPathInEditor: %v", err)
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
		if !argMap["--tab-title=myplan"] {
			t.Errorf("expected --tab-title=myplan in args: %v", c.Args)
		}
		if !argMap["--cwd=/tmp/plans/myplan"] {
			t.Errorf("expected --cwd=/tmp/plans/myplan in args: %v", c.Args)
		}

		// Verify the trailing cmdline contains: myeditor --wait /tmp/plans/myplan
		// (split on whitespace per resolveEditor's handling).
		wantTrailing := []string{"myeditor", "--wait", "/tmp/plans/myplan"}
		if !containsInOrder(c.Args, wantTrailing) {
			t.Errorf("expected cmdline tail %v to appear in order in args: %v", wantTrailing, c.Args)
		}
	}
	if !launchCalled {
		t.Error("expected kitten @ launch to be called")
	}

	// No set-tab-color call: OpenPathInEditor has no account context.
	for _, c := range runner.Calls {
		if c.Name != "kitten" {
			continue
		}
		for _, a := range c.Args {
			if a == "set-tab-color" {
				t.Errorf("did not expect set-tab-color for OpenPathInEditor: %v", c.Args)
			}
		}
	}
}

func TestOpenPathInEditorUsesParentDirectoryForFile(t *testing.T) {
	t.Parallel()

	empty := loadFixture(t, "kitty_ls_empty.json")
	runner := &sequentialLSRunner{lsResponses: [][]byte{empty, empty, empty}}
	client := kitty.NewClientWithSocket(runner, "")
	cfg := buildTestConfig()
	cfg.Defaults.Editor = "myeditor"
	directory := t.TempDir()
	path := filepath.Join(directory, "handoff.md")
	if err := os.WriteFile(path, []byte("handoff"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := session.OpenPathInEditor(client, path, "handoff", cfg); err != nil {
		t.Fatalf("OpenPathInEditor: %v", err)
	}
	for _, call := range runner.Calls {
		if call.Name == "kitten" && containsInOrder(call.Args, []string{"myeditor", path}) {
			if !containsInOrder(call.Args, []string{"--cwd=" + directory}) {
				t.Fatalf("launch args = %v, want parent cwd %q", call.Args, directory)
			}
			return
		}
	}
	t.Fatal("editor launch call not found")
}

// containsInOrder reports whether sub appears as a contiguous slice in s.
func containsInOrder(s, sub []string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := range sub {
			if s[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// TestOpenDirectory_ColorApplied verifies that set-tab-color is called
// when the new tab appears in a post-launch ls response.
func TestOpenDirectory_ColorApplied(t *testing.T) {
	t.Parallel()

	empty := loadFixture(t, "kitty_ls_empty.json")

	postLaunch := []byte(`[{"id":1,"is_active":true,"is_focused":true,"last_focused":true,"tabs":[` +
		`{"id":200,"is_active":true,"title":"myproject","windows":[` +
		`{"id":201,"is_active":true,"title":"myproject","cwd":"/tmp/myproject","cmdline":["zsh"],` +
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
