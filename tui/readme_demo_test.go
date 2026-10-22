//go:build tuitest

package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/tui/commands"
)

// TestReadmeDemo exports real dashboard renders after keyboard input. The
// existing offline driver supplies synthetic sessions, a frozen clock, and
// isolated command seams; no agent, daemon, or terminal is started.
func TestReadmeDemo(t *testing.T) {
	fx := portableTmuxFixture()
	fx.Size = UITestSize{Width: 100, Height: 24}
	fx.Config.Accounts = []config.Account{
		{Name: "claude", Harness: "claude", Color: "blue"},
		{Name: "codex", Harness: "codex", Color: "green"},
		{Name: "copilot", Harness: "copilot", Color: "magenta"},
	}
	for i, name := range []string{"claude", "codex", "copilot"} {
		fx.Portable[i].Profile = name
		fx.Portable[i].Account = name
		fx.Portable[i].Harness = name
		fx.Config.Sessions[i].Profile = name
	}
	fx.States[commands.PortableIDBase] = protocol.WindowStateSnapshot{IconState: "key", Reason: "permission"}
	fx.States[commands.PortableIDBase+2] = protocol.WindowStateSnapshot{IconState: "stop"}
	for id, state := range fx.States {
		state.Branch = "main"
		fx.States[id] = state
	}
	fx.Health = &commands.HealthMsg{
		DaemonAlive: true,
		HookSummary: commands.HookHealth{InSync: []string{"claude", "codex", "copilot"}},
	}
	output := map[int]string{
		commands.PortableIDBase:     "Claude Code\n\nAdd request validation to the API.\n\nReady to check the new handlers:\n\n  go test ./internal/api/...\n\nAllow this command?\n  1. Yes\n  2. No\n",
		commands.PortableIDBase + 1: "OpenAI Codex\n\nBuild the settings page.\n\n+ Added account preferences\n+ Connected the theme selector\n\nRunning component tests...\n",
		commands.PortableIDBase + 2: "GitHub Copilot CLI\n\nUpdate the getting-started guide.\n\n+ Documented tmux setup\n+ Added profile examples\n\nDocumentation checks passed.\nReady for your next prompt.\n",
	}
	fx.Text = output[commands.PortableIDBase]
	d, _ := newUITestFixtureDriver(fx, fx.Size)
	cleanupUITestDriver(t, d)
	d.model.backgroundCommands.scrollback = func(id int) (string, error) { return output[id], nil }
	d.Tick(1)

	type frame struct {
		ANSI    string `json:"ansi"`
		HoldMS  int    `json:"hold_ms"`
		Caption string `json:"caption"`
	}
	var frames []frame
	capture := func(caption string, hold int, want string) {
		t.Helper()
		obs := d.Observe()
		plain := strings.Join(obs.Screen.Lines, "\n")
		if err := d.Err(); err != nil {
			t.Fatal(err)
		}
		if len(obs.Screen.Issues) != 0 || !strings.Contains(plain, want) {
			t.Fatalf("%s: want %q, layout issues: %+v\n%s", caption, want, obs.Screen.Issues, plain)
		}
		if os.Getenv("AMX_README_DEMO_PRINT") != "" {
			t.Logf("%s\n%s", caption, plain)
		}
		frames = append(frames, frame{d.model.render(), hold, caption})
	}
	key := func(key string) {
		t.Helper()
		if err := d.Key(key); err != nil {
			t.Fatal(err)
		}
	}
	typeText := func(text string) {
		t.Helper()
		if err := d.Text(text); err != nil {
			t.Fatal(err)
		}
	}

	capture("Three agents. One dashboard.", 3000, "Allow this command?")
	key("j")
	capture("j  inspect the next agent", 2600, "Running component tests")
	key("j")
	capture("j  check completed work", 2600, "Documentation checks passed")
	key("n")
	capture("n  jump to the agent that needs you", 3000, "Allow this command?")
	key("/")
	typeText("%codex")
	key("enter")
	key("enter")
	capture("/ %codex  filter by harness", 2800, "Running component tests")
	if len(d.model.filtered) != 1 || d.model.filtered[0].Harness != "codex" {
		t.Fatal("harness filter did not isolate Codex")
	}
	key("esc")
	key("p")
	capture("p  switch projects", 2400, "All projects")
	key("esc")
	key(":")
	typeText("worktree")
	capture(": worktree  find a command", 3000, "Manage Worktrees")
	key("esc")
	key("n")
	capture("Back to the queue", 1800, "Allow this command?")

	if dir := os.Getenv("AMX_README_DEMO_DIR"); dir != "" {
		data, err := json.MarshalIndent(frames, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "demo.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
