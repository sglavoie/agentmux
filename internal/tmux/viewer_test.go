package tmux

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
)

// viewer is a real tmux client attached to one pane through a
// pseudo-terminal: what an embedded terminal is to the server. Bytes written
// to it arrive as that terminal's keyboard input.
type viewer struct {
	t     *testing.T
	f     *fixture
	pane  backend.Ref
	stdin io.WriteCloser
}

func attachViewer(t *testing.T, f *fixture, pane backend.Ref) *viewer {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("uses BSD script(1) for a pseudo-terminal")
	}
	client, err := f.backend.AttachCommand(pane)
	if err != nil {
		t.Fatal(err)
	}
	script := exec.Command("script", append([]string{"-q", "/dev/null", client.Path}, client.Args[1:]...)...)
	script.Env = append(client.Env, "TERM=xterm-256color")
	stdin, err := script.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := script.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = script.Process.Kill()
		_ = script.Wait()
	})
	v := &viewer{t: t, f: f, pane: pane, stdin: stdin}
	v.wait("a client to attach", func() bool {
		return f.tmux(t, "list-clients", "-t", pane.ID, "-F", "#{client_tty}") != ""
	})
	return v
}

func (v *viewer) send(bytes string) {
	v.t.Helper()
	if _, err := io.WriteString(v.stdin, bytes); err != nil {
		v.t.Fatalf("writing to the viewer: %v", err)
	}
}

// ask sends a history request the way the app does: the key's escape sequence.
func (v *viewer) ask(k ViewerKey) {
	v.t.Helper()
	v.send("\x1b[" + k.CSI())
}

func (v *viewer) format(format string) string {
	v.t.Helper()
	return v.f.tmux(v.t, "display-message", "-p", "-t", v.pane.ID, format)
}

func (v *viewer) wait(what string, ok func() bool) {
	v.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			v.t.Fatalf("timed out waiting for %s (mode=%s position=%s line=%q)", what,
				v.format("#{pane_in_mode}"), v.format("#{scroll_position}"), v.format("#{copy_cursor_line}"))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (v *viewer) inHistory() bool { return v.format("#{pane_in_mode}") == "1" }

const historyLines = `i=1; while [ $i -le 600 ]; do printf 'LINE %04d needle-%04d\n' $i $i; i=$((i+1)); done; exec cat`

// TestViewerKeysReachHistoryHeldByTheServer drives the history requests of a
// viewer against a pane whose output scrolled into tmux's history: paging,
// finding, stepping through matches, and returning to the live screen, with
// no prefix and no tmux key.
func TestViewerKeysReachHistoryHeldByTheServer(t *testing.T) {
	f := newFixture(t, Options{})
	l := f.launch(t, "history", []string{"/bin/sh", "-c", historyLines}, nil)
	v := attachViewer(t, f, l.Pane)
	v.wait("the output to scroll into history", func() bool { return v.format("#{history_size}") != "0" })

	v.ask(ViewerPageUp)
	v.wait("page up to enter history", v.inHistory)
	first := v.format("#{scroll_position}")
	v.ask(ViewerPageUp)
	v.wait("a second page up to go further", func() bool { return v.format("#{scroll_position}") != first })
	v.ask(ViewerPageDown)
	v.ask(ViewerPageDown)
	v.wait("paging down to the bottom to leave history", func() bool { return !v.inHistory() })

	v.ask(ViewerFind)
	v.wait("find to open history", v.inHistory)
	v.send("needle-012")
	v.send("\r")
	v.wait("find to land on the last match", func() bool {
		return strings.Contains(v.format("#{copy_cursor_line}"), "needle-0129")
	})
	v.ask(ViewerFindNext)
	v.wait("find next to step to the match before", func() bool {
		return strings.Contains(v.format("#{copy_cursor_line}"), "needle-0128")
	})
	v.ask(ViewerFindPrevious)
	v.wait("find previous to step back", func() bool {
		return strings.Contains(v.format("#{copy_cursor_line}"), "needle-0129")
	})

	v.ask(ViewerTop)
	v.wait("top to reach the first line", func() bool {
		return v.format("#{scroll_position}") == v.format("#{history_size}")
	})
	v.ask(ViewerBottom)
	v.wait("bottom to leave history", func() bool { return !v.inHistory() })

	// History belongs to the server: a replaced viewer finds all of it.
	size := v.format("#{history_size}")
	v2 := attachViewer(t, f, l.Pane)
	if got := v2.format("#{history_size}"); got != size {
		t.Fatalf("history after the viewer was replaced = %s lines, want %s", got, size)
	}
	v2.ask(ViewerFind)
	v2.wait("find to open history for the replacement viewer", v2.inHistory)
	v2.send("needle-0001\r")
	v2.wait("the replacement viewer to find the first line", func() bool {
		return strings.Contains(v2.format("#{copy_cursor_line}"), "needle-0001")
	})
	v2.send("\x1b")
	v2.wait("Escape to leave history", func() bool { return !v2.inHistory() })
	t.Logf("observed: page up/down, find, find next/previous, top and bottom moved through %s lines of tmux history; a replacement viewer found line 1", size)
}

// TestViewerPagingReachesAFullScreenProgram: a program on the alternate
// screen keeps its own history, so paging is its Page Up and Page Down rather
// than a copy mode with nothing above the screen.
func TestViewerPagingReachesAFullScreenProgram(t *testing.T) {
	f := newFixture(t, Options{})
	out := filepath.Join(f.dir, "keys.out")
	l := f.launch(t, "fullscreen", []string{"/bin/sh", "-c",
		`printf '\033[?1049h'; stty raw -echo; exec cat > "$1"`, "sh", out}, nil)
	v := attachViewer(t, f, l.Pane)
	v.wait("the program to take the alternate screen", func() bool { return v.format("#{alternate_on}") == "1" })

	v.ask(ViewerPageUp)
	v.ask(ViewerPageDown)
	v.ask(ViewerTop)
	v.wait("the program to receive Page Up and Page Down", func() bool {
		data, _ := os.ReadFile(out)
		return string(data) == "\x1b[5~\x1b[6~"
	})
	if v.inHistory() {
		t.Fatal("paging a full-screen program entered copy mode")
	}
}

// TestMouseBindingsOfferNothingDestructive: no mouse gesture on the private
// server opens a tmux menu or rearranges, kills, or respawns a pane.
func TestMouseBindingsOfferNothingDestructive(t *testing.T) {
	f := newFixture(t, Options{})
	f.launch(t, "mouse", echoArgv("mouse"), nil)
	keys := f.tmux(t, "list-keys", "-T", "root")
	for _, forbidden := range []string{"display-menu", "kill-pane", "kill-window", "respawn", "split-window", "swap-pane", "swap-window", "new-window", "new-session"} {
		if strings.Contains(keys, forbidden) {
			t.Errorf("a root binding still runs %s:\n%s", forbidden, keys)
		}
	}
	for _, kept := range []string{"WheelUpPane", "MouseDrag1Pane", "DoubleClick1Pane", "MouseDown3Pane"} {
		if !strings.Contains(keys, kept) {
			t.Errorf("root binding %s is gone", kept)
		}
	}
}

// TestAttachBringsAnOlderServerUpToDate: a server started by an earlier build
// lacks this build's bindings until a viewer attaches, and applying the
// configuration again changes nothing else.
func TestAttachBringsAnOlderServerUpToDate(t *testing.T) {
	f := newFixture(t, Options{})
	l := f.launch(t, "older", echoArgv("older"), nil)
	features := f.tmux(t, "show-options", "-s", "terminal-features")
	for _, k := range ViewerKeys() {
		f.tmux(t, "unbind-key", "-n", "User"+string(rune('0'+int(k))))
	}
	f.tmux(t, "set-option", "-su", "user-keys")

	v := attachViewer(t, f, l.Pane)
	v.wait("the bindings to be restored", func() bool {
		return strings.Count(f.tmux(t, "list-keys", "-T", "root"), " User") == len(ViewerKeys())
	})
	for _, k := range ViewerKeys() {
		want := `\033[` + k.CSI()
		if got := f.tmux(t, "show-options", "-s", "user-keys"); !strings.Contains(got, want) {
			t.Errorf("user-keys = %q, missing %s", got, want)
		}
	}
	attachViewer(t, f, l.Pane)
	if got := f.tmux(t, "show-options", "-s", "terminal-features"); got != features {
		t.Errorf("terminal-features changed when the configuration was applied again:\n%s\nwas:\n%s", got, features)
	}
}
