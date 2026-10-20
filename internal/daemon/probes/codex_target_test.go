package probes

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
)

func TestCodexForegroundTargetPicksTheActiveInteractiveCodexPane(t *testing.T) {
	codex := func(id int, active bool, cmdline ...string) kitty.Window {
		return kitty.Window{ID: id, IsActive: active, Cmdline: cmdline}
	}
	foreground := kitty.Window{ID: 7, ForegroundProcesses: []kitty.ForegroundProcess{{Cmdline: []string{"/opt/homebrew/bin/codex", "--model", "o3"}}}}

	for _, tc := range []struct {
		name   string
		tab    *kitty.Tab
		wantID int
	}{
		{name: "nil tab", tab: nil},
		{name: "no Codex pane", tab: &kitty.Tab{Windows: []kitty.Window{codex(1, true, "zsh")}}},
		{name: "the active Codex pane wins", tab: &kitty.Tab{Windows: []kitty.Window{codex(1, false, "codex"), codex(2, true, "codex")}}, wantID: 2},
		{name: "else the first Codex pane", tab: &kitty.Tab{Windows: []kitty.Window{codex(1, true, "zsh"), codex(3, false, "CODEX"), codex(4, false, "codex")}}, wantID: 3},
		{name: "a foreground Codex counts", tab: &kitty.Tab{Windows: []kitty.Window{codex(1, true, "zsh"), foreground}}, wantID: 7},
		{name: "codex exec is not a session", tab: &kitty.Tab{Windows: []kitty.Window{codex(1, true, "codex", "exec", "fix it")}}},
		{name: "a managed tab is the managed path's", tab: &kitty.Tab{Windows: []kitty.Window{
			{ID: 1, UserVars: kitty.UserVars{Harness: "claude"}}, codex(2, true, "codex"),
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			win, ok := CodexForegroundTarget(tc.tab)
			if tc.wantID == 0 {
				if ok {
					t.Fatalf("CodexForegroundTarget = window %d, want none", win.ID)
				}
				return
			}
			if !ok || win.ID != tc.wantID {
				t.Fatalf("CodexForegroundTarget = %v, %t; want window %d", win, ok, tc.wantID)
			}
			// The window returned is the tab's own, not a copy.
			if win != &tc.tab.Windows[indexOf(tc.tab, tc.wantID)] {
				t.Fatal("CodexForegroundTarget returned a copy of the window")
			}
		})
	}
}

func indexOf(tab *kitty.Tab, id int) int {
	for i := range tab.Windows {
		if tab.Windows[i].ID == id {
			return i
		}
	}
	return -1
}
