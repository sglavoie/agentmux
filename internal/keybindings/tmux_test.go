package keybindings_test

import (
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/keybindings"
	"github.com/sglavoie/agentmux/internal/tmux"
)

func TestTmuxBindingsAppearInServerConfig(t *testing.T) {
	seen := map[string]bool{}
	for _, action := range keybindings.Tmux {
		if seen[action.Key] {
			t.Errorf("duplicate tmux key %s", action.Key)
		}
		seen[action.Key] = true
		line := action.TmuxLine()
		if action.Key == "f12" {
			if line != "" || !strings.Contains(tmux.Config, "bind-key -n "+tmux.DetachKey+" detach-client\n") {
				t.Error("F12 must remain the hand-bound detach key")
			}
			continue
		}
		if action.AmxCommand != "" {
			if line == "" || !strings.Contains(tmux.Config, "\n"+line+"\n") {
				t.Errorf("%s binding missing from tmux config: %q", action.Key, line)
			}
		}
		moves := strings.HasPrefix(action.AmxCommand, "focus-")
		if named := strings.Contains(action.AmxCommand, `--client "#{client_name}"`); named != moves {
			t.Errorf("%s names its client = %t, moves it = %t", action.Key, named, moves)
		}
	}
}

func TestTmuxKeySpellsKeysAsTmuxDoes(t *testing.T) {
	for key, want := range map[string]string{"f1": "F1", "f11": "F11", "ctrl+shift+d": "C-S-d", "alt+x": "M-x"} {
		if got := keybindings.TmuxKey(key); got != want {
			t.Errorf("TmuxKey(%q) = %q, want %q", key, got, want)
		}
	}
}
