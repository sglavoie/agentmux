package kittykeys_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/kittykeys"
	"github.com/sglavoie/agentmux/internal/tmux"
)

var functionKey = regexp.MustCompile(`^f\d+$`)

// amxSubcommand is the amx command an action runs, without the arguments that
// only say where it was pressed: Kitty passes its window, tmux its client.
func amxSubcommand(command string) string {
	var kept []string
	fields := strings.Fields(command)
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "--window-id", "--client":
			i++
		case "--popup":
		default:
			kept = append(kept, fields[i])
		}
	}
	return strings.Join(kept, " ")
}

// TestTmuxFunctionKeysMatchKitty holds the keys a plain terminal gets from
// the private tmux configuration to the ones agent-mux.conf gives a Kitty
// tab. The two tables are read by different programs, so a key added to one
// only would work in one kind of terminal and be missing, or mean something
// else, in the other.
func TestTmuxFunctionKeysMatchKitty(t *testing.T) {
	kitty := map[string]kittykeys.Action{}
	for _, action := range kittykeys.Actions {
		if functionKey.MatchString(action.Key) || strings.HasPrefix(action.Key, "ctrl+shift+") {
			kitty[action.Key] = action
		}
	}
	seen := map[string]bool{}
	for _, action := range kittykeys.Tmux {
		if seen[action.Key] {
			t.Errorf("tmux binds %s twice", action.Key)
		}
		seen[action.Key] = true
		want, ok := kitty[action.Key]
		if action.Key == "f12" {
			// The one key tmux had before these: it detaches.
			if ok || action.TmuxLine() != "" || !strings.Contains(tmux.Config, "bind-key -n "+tmux.DetachKey+" detach-client\n") {
				t.Errorf("f12 must stay the hand-bound detach key")
			}
			continue
		}
		if !ok {
			t.Errorf("tmux binds %s (%s), which Kitty does not", action.Key, action.Description)
			continue
		}
		if got, want := amxSubcommand(action.AmxCommand), amxSubcommand(want.AmxCommand); got != want {
			t.Errorf("%s runs amx %q in tmux and amx %q in Kitty", action.Key, got, want)
		}
		line := action.TmuxLine()
		switch want.LaunchType {
		case "overlay":
			if !strings.HasPrefix(line, "bind-key -n "+kittykeys.TmuxKey(action.Key)+" display-popup -E ") {
				t.Errorf("%s is an overlay in Kitty, so it is a popup in tmux; got %q", action.Key, line)
			}
			if strings.Contains(line, "#{") {
				t.Errorf("%s: tmux does not expand a popup's command, yet it carries a format: %q", action.Key, line)
			}
		case "background":
			if !strings.HasPrefix(line, "bind-key -n "+kittykeys.TmuxKey(action.Key)+" run-shell -b ") {
				t.Errorf("%s runs in the background in Kitty, so it is a run-shell in tmux; got %q", action.Key, line)
			}
		default:
			// Kitty's own action (F3's markers): tmux's is written by hand.
			if line != "" {
				t.Errorf("%s is Kitty's own action, yet tmux generates %q for it", action.Key, line)
			}
			if !strings.Contains(tmux.Config, "\nbind-key -n "+kittykeys.TmuxKey(action.Key)+" ") {
				t.Errorf("%s has no hand-written binding in the tmux configuration", action.Key)
			}
		}
		if line != "" && !strings.Contains(tmux.Config, "\n"+line+"\n") {
			t.Errorf("the tmux configuration does not hold %q", line)
		}
	}
	for key, action := range kitty {
		if !seen[key] {
			t.Errorf("Kitty binds %s (%s) and tmux does not", key, action.Description)
		}
	}

	// Going to another session moves the terminal that pressed the key, so
	// those three name it; nothing else may.
	for _, action := range kittykeys.Tmux {
		moves := strings.HasPrefix(action.AmxCommand, "focus-")
		if named := strings.Contains(action.AmxCommand, `--client "#{client_name}"`); named != moves {
			t.Errorf("%s: names its client = %v, moves it = %v", action.Key, named, moves)
		}
	}
}

func TestTmuxKeySpellsKeysAsTmuxDoes(t *testing.T) {
	for key, want := range map[string]string{"f1": "F1", "f11": "F11", "ctrl+shift+d": "C-S-d", "alt+x": "M-x"} {
		if got := kittykeys.TmuxKey(key); got != want {
			t.Errorf("TmuxKey(%q) = %q, want %q", key, got, want)
		}
	}
}
