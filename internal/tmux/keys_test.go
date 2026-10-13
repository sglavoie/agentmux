package tmux_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
	"github.com/sglavoie/agentmux/internal/runtimesession"
	"github.com/sglavoie/agentmux/internal/tmux"
	"github.com/sglavoie/agentmux/internal/tmux/tmuxtest"
)

// The function keys are bound in the configuration and run `amx`. This runs
// them for real: a terminal attached to a session types each key, and a
// stand-in amx, named by the terminal as it attached, records what it was
// run with. What it
// must see is the command of the key, the runtime session of the session the
// terminal is on (from that session's environment, never from an argument),
// and for the keys that move the terminal, which terminal pressed the key.
func TestFunctionKeysRunAmxForTheSessionAndTerminalThatPressedThem(t *testing.T) {
	f := tmuxtest.New(t)
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "amx.log")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s|%%s\\n' \"$*\" \"$%s\" >> %q\n", runtimesession.EnvVar, log)
	if err := os.WriteFile(filepath.Join(bin, "amx"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// The stand-in is on no PATH: the terminal that attaches says where it is.
	launched, err := f.Backend.LaunchTarget(backend.LaunchSpec{
		Title: "agent", Directory: t.TempDir(), Argv: []string{"/bin/sh", "-c", "exec sleep 600"},
		Env: map[string]string{runtimesession.EnvVar: "rs-keys"},
	})
	if err != nil {
		t.Fatal(err)
	}
	attaching, err := f.Backend.AttachCommand(launched.Pane, tmux.AttachView{Amx: filepath.Join(bin, "amx")})
	if err != nil {
		t.Fatal(err)
	}
	viewer := tmuxtest.StartViewer(t, attaching, 90, 24)
	client := on(t, eventually(t, f, "the owner", owned(launched.Pane)), launched.Pane).Name
	if _, err := f.Backend.AttachCommand(launched.Pane, tmux.AttachView{Amx: "amx; kill-server"}); err == nil {
		t.Fatal("an amx that is not an absolute path was accepted")
	}

	keys := []struct {
		name, bytes, want string
	}{
		{"F9", "\x1b[20~", `focus-next-managed --client ` + client},
		{"F7", "\x1b[18~", `focus-prev-managed --client ` + client},
		{"F4", "\x1bOS", `focus-next --client ` + client},
		{"F8", "\x1b[19~", "hook-indicator user_clear"},
		{"F10", "\x1b[21~", "mute-toggle"},
		{"F11", "\x1b[23~", "hook-indicator user_active"},
		// The popups: the stand-in ends at once, which closes each.
		{"F1", "\x1bOP", "keybindings"},
		{"F2", "\x1bOQ", "overview --popup"},
		{"F5", "\x1b[15~", "icon-picker"},
		{"F6", "\x1b[17~", "color-picker"},
		{"C-S-d", "\x1b[100;6u", "--popup"},
	}
	for i, key := range keys {
		if err := viewer.Type(key.bytes); err != nil {
			t.Fatal(err)
		}
		want := key.want + "|rs-keys"
		var lines []string
		typed := time.Now()
		for deadline := typed.Add(20 * time.Second); ; time.Sleep(25 * time.Millisecond) {
			data, _ := os.ReadFile(log)
			lines = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
			if len(lines) == i+1 && lines[i] != "" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s ran nothing; amx was run with %q", key.name, lines)
			}
			// A popup that is still closing takes the key meant for the
			// terminal under it, so a popup key that opened nothing is typed
			// again. The others ran before any popup opened.
			if strings.HasPrefix(tmuxLine(key.name), "display-popup") && time.Since(typed) > 2*time.Second {
				if err := viewer.Type(key.bytes); err != nil {
					t.Fatal(err)
				}
				typed = time.Now()
			}
		}
		if lines[i] != want {
			t.Fatalf("%s ran amx with %q, want %q", key.name, lines[i], want)
		}
		if strings.HasPrefix(tmuxLine(key.name), "display-popup") {
			// The stand-in has written its line; give its popup the moment
			// it takes to close before the next key is typed.
			time.Sleep(300 * time.Millisecond)
		}
	}

	// None of it attached, detached, or added anything.
	if s := survey(t, f); len(s.Panes) != 1 || len(s.Clients) != 1 || s.Clients[0].Name != client {
		t.Fatalf("after the keys: panes=%+v clients=%+v", s.Panes, s.Clients)
	}
}

// tmuxLine is what the configuration binds a tmux key name to.
func tmuxLine(key string) string {
	for line := range strings.SplitSeq(tmux.Config, "\n") {
		if rest, ok := strings.CutPrefix(line, "bind-key -n "+key+" "); ok {
			return rest
		}
	}
	return ""
}
