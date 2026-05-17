package indicator

import "testing"

// fakeTree builds walkTree-compatible cmdline and ppid functions from a map.
func fakeTree(cmdlines map[int]string, ppids map[int]int) (func(int) string, func(int) int) {
	return func(pid int) string { return cmdlines[pid] },
		func(pid int) int {
			if p, ok := ppids[pid]; ok {
				return p
			}
			return 1
		}
}

func TestWalkTree_NormalProcess(t *testing.T) {
	cmd, ppid := fakeTree(
		map[int]string{1000: "/bin/zsh", 999: "launchd"},
		map[int]int{1000: 999},
	)
	if walkTree(1000, cmd, ppid) {
		t.Error("expected pipe_mode=false for normal process tree")
	}
}

func TestWalkTree_ClaudeWithDashP(t *testing.T) {
	// Tree: 1002 (hook) → 1001 (shell) → 1000 (claude -p)
	cmd, ppid := fakeTree(
		map[int]string{
			1002: "amx hook-indicator active",
			1001: "/bin/zsh",
			1000: "node /usr/local/bin/claude -p 'summarize'",
		},
		map[int]int{1002: 1001, 1001: 1000, 1000: 1},
	)
	if !walkTree(1002, cmd, ppid) {
		t.Error("expected pipe_mode=true when ancestor has claude -p")
	}
}

func TestWalkTree_ClaudeWithPrint(t *testing.T) {
	cmd, ppid := fakeTree(
		map[int]string{
			1001: "amx hook-indicator stop",
			1000: "node /usr/local/bin/claude --print",
		},
		map[int]int{1001: 1000, 1000: 1},
	)
	if !walkTree(1001, cmd, ppid) {
		t.Error("expected pipe_mode=true when ancestor has claude --print")
	}
}

func TestWalkTree_StopsAtRoot(t *testing.T) {
	cmd, ppid := fakeTree(
		map[int]string{1: "launchd"},
		map[int]int{},
	)
	if walkTree(1, cmd, ppid) {
		t.Error("expected pipe_mode=false when starting at root")
	}
}

// TestWalkTree_FlagPositionMatters verifies that a -p flag belonging to an
// unrelated ancestor command (e.g. `claude-lint -p`) does not trigger pipe
// mode for an interactive child claude session. Under the old substring-based
// check, the parent's "claude" substring + "-p" was enough to flag the whole
// tree as pipe mode.
func TestWalkTree_FlagPositionMatters(t *testing.T) {
	cmd, ppid := fakeTree(
		map[int]string{
			1001: "claude",         // interactive child, no pipe flag
			1000: "claude-lint -p", // unrelated ancestor with its own -p flag
		},
		map[int]int{1001: 1000, 1000: 1},
	)
	if walkTree(1001, cmd, ppid) {
		t.Error("expected pipe_mode=false: ancestor's -p must not propagate to a child interactive claude")
	}
}

func TestWalkTree_CycleProtection(t *testing.T) {
	count := 0
	cmd := func(_ int) string {
		count++
		if count > 100 {
			panic("infinite loop in walkTree")
		}
		return "something"
	}
	ppid := func(pid int) int { return pid } // self-referential cycle
	if walkTree(100, cmd, ppid) {
		t.Error("expected pipe_mode=false for non-claude cycle")
	}
}

func TestLooksLikeClaudePipe(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"node /usr/local/bin/claude -p 'do thing'", true},
		{"node /usr/local/bin/claude --print 'do thing'", true},
		{"node /usr/local/bin/claude --help", false},
		{"/bin/zsh", false},
		{"", false},
		{"claude", false},
		{"claude -p", true},
		{"claude --print", true},
		{"claude --print-output", false}, // --print-output is not --print
		// Exact-command match: basename must equal "claude" or "copilot".
		{"claude-lint", false},
		{"claude-lint -p", false},          // basename is claude-lint, not claude
		{"/opt/claude-tools/foo", false},   // basename is foo, not claude
		{"/opt/claude-tools/foo -p", false},
		{"/usr/bin/claude -p", true},                // path-prefixed claude is fine
		{"/usr/local/bin/copilot --prompt", true},   // path-prefixed copilot is fine
		{"copilot", false},
		{"copilot -p", true},
		{"copilot --prompt", true},
		{"copilot --print", false}, // --print is claude-only; copilot uses --prompt
		// Flag must follow the command in the same cmdline.
		{"-p claude", false},        // -p before claude
		{"some-tool -p claude", false},
	}
	for _, tc := range cases {
		got := looksLikeClaudePipe(tc.cmd)
		if got != tc.want {
			t.Errorf("looksLikeClaudePipe(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}
