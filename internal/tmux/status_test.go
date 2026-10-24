package tmux

import (
	"bytes"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
)

// terminal is what a real tmux client wrote to the pseudo-terminal it is
// attached through: everything the outer terminal was asked to show.
type terminal struct {
	mu  sync.Mutex
	out bytes.Buffer
}

func (o *terminal) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.out.Write(p)
}

func (o *terminal) received() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.out.String()
}

func (o *terminal) wait(t *testing.T, what string, ok func(received string) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok(o.received()) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; the terminal received %q", what, o.received())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// colors is what the terminal a client attaches through says it can draw.
// tmux reads it from TERM and COLORTERM, so a test names both rather than
// taking whichever the terminal that started it happened to export.
type colors struct {
	name      string
	colorterm string // COLORTERM, or empty to leave it unset
	// background is the SGR parameter the terminal is sent for a "#89b4fa"
	// background: the color itself, or the nearest of the 256 indexed ones.
	background string
	// noLocale leaves LANG and LC_* unset, as they are for an app launchd
	// started, though the terminal itself draws UTF-8.
	noLocale bool
}

var (
	indexedColors = colors{name: "256 colors", background: "48;5;111"}
	trueColors    = colors{name: "true color", colorterm: "truecolor", background: "48;2;137;180;250"}
	noLocale      = colors{name: "no locale", background: "48;5;111", noLocale: true}
)

// env is environ with the terminal's capabilities replaced by c's.
func (c colors) env(environ []string) []string {
	env := make([]string, 0, len(environ)+2)
	for _, kv := range environ {
		if strings.HasPrefix(kv, "TERM=") || strings.HasPrefix(kv, "COLORTERM=") {
			continue
		}
		if c.noLocale && (strings.HasPrefix(kv, "LANG=") || strings.HasPrefix(kv, "LC_")) {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "TERM=xterm-256color")
	if c.colorterm != "" {
		env = append(env, "COLORTERM="+c.colorterm)
	}
	return env
}

// attachTerminal attaches a real client to pane the way `amx attach` does,
// through a terminal that draws c, and returns what that terminal receives.
func attachTerminal(t *testing.T, f *fixture, pane backend.Ref, view AttachView, c colors) *terminal {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("uses BSD script(1) for a pseudo-terminal")
	}
	client, err := f.backend.AttachCommand(pane, view)
	if err != nil {
		t.Fatal(err)
	}
	sized := `stty cols 80 rows 12; exec "$@"`
	script := exec.Command("script", append([]string{"-q", "/dev/null", "/bin/sh", "-c", sized, "viewer", client.Path}, client.Args[1:]...)...)
	script.Env = c.env(client.Env)
	out := &terminal{}
	script.Stdout = out
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
	return out
}

// title is the sequence that sets an xterm's title.
func title(text string) string { return "\x1b]0;" + text + "\a" }

func (f *fixture) generation(t *testing.T) string {
	t.Helper()
	instance, err := f.server.Instance()
	if err != nil {
		t.Fatal(err)
	}
	return instance.Generation
}

func (f *fixture) status(t *testing.T, pane backend.Ref) (text, color string) {
	t.Helper()
	text, color, _ = strings.Cut(f.tmux(t, "display-message", "-p", "-t", pane.ID, "#{"+StatusOption+"}"+fieldSep+"#{"+StatusColorOption+"}"), fieldSep)
	return text, color
}

// A pass's statuses go out in one client, each applied by itself: a pane that
// is gone or a server that restarted costs the others nothing, and text is
// stored as written whatever it contains.
func TestWriteStatusesIsOneClientAndEachPaneStandsAlone(t *testing.T) {
	f := newFixture(t, Options{})
	first := f.launch(t, "first", []string{"/bin/sh", "-c", "exec cat"}, nil)
	second := f.launch(t, "second", []string{"/bin/sh", "-c", "exec cat"}, nil)
	third := f.launch(t, "third", []string{"/bin/sh", "-c", "exec cat"}, nil)
	generation := f.generation(t)

	awkward := "-⚡ it's #[fg=red]#{pid} a,b}; \"x\" \\ $HOME"
	before := Stats()
	err := f.backend.WriteStatuses([]Status{
		{Pane: first.Pane.ID, Generation: generation, Text: awkward, Color: "#89b4fa"},
		{Pane: "%999", Generation: generation, Text: "gone"},
		{Pane: second.Pane.ID, Generation: "1-1", Text: "another server's session", Color: "#89b4fa"},
		{Pane: third.Pane.ID, Generation: generation, Text: "two\nlines\tand\x1b[31m control", Color: "red; kill-server"},
		{Pane: "$0", Generation: generation, Text: "not a pane"},
		{Pane: third.Pane.ID, Generation: generation + "'", Text: "not a generation"},
	})
	if got := Stats().Commands - before.Commands; got != 1 {
		t.Fatalf("writing six statuses started %d tmux clients, want one", got)
	}
	if err == nil {
		t.Fatal("a status for a pane that is gone was not reported")
	}
	if text, color := f.status(t, first.Pane); text != awkward || color != "#89b4fa" {
		t.Fatalf("first = %q on %q, want %q on #89b4fa", text, color, awkward)
	}
	if text, color := f.status(t, second.Pane); text != "" || color != "" {
		t.Fatalf("a status naming another server generation was written: %q on %q", text, color)
	}
	if text, color := f.status(t, third.Pane); text != "two lines and [31m control" || color != "" {
		t.Fatalf("third = %q on %q, want one line and no color", text, color)
	}

	// Rendered again, a status replaces the one before it, color included.
	if err := f.backend.WriteStatuses([]Status{{Pane: first.Pane.ID, Generation: generation, Text: "api"}}); err != nil {
		t.Fatal(err)
	}
	if text, color := f.status(t, first.Pane); text != "api" || color != "" {
		t.Fatalf("first = %q on %q, want it replaced", text, color)
	}
	before = Stats()
	if err := f.backend.WriteStatuses(nil); err != nil || Stats().Commands != before.Commands {
		t.Fatalf("writing nothing ran a client (err=%v)", err)
	}
}

// The terminal attached to a session is titled with the rendered status and,
// when it asked for the line, shows it on the session's color; both follow
// the option as the daemon rewrites it, and nothing in the text is read as
// tmux styling. The color reaches a terminal in the form it can draw.
func TestAnAttachedTerminalShowsTheRenderedStatus(t *testing.T) {
	for _, c := range []colors{indexedColors, trueColors, noLocale} {
		t.Run(c.name, func(t *testing.T) { testAnAttachedTerminalShowsTheRenderedStatus(t, c) })
	}
}

func testAnAttachedTerminalShowsTheRenderedStatus(t *testing.T, c colors) {
	f := newFixture(t, Options{})
	l := f.launch(t, "status", []string{"/bin/sh", "-c", "exec cat"}, nil)
	generation := f.generation(t)
	write := func(text, color string) {
		t.Helper()
		if err := f.backend.WriteStatuses([]Status{{Pane: l.Pane.ID, Generation: generation, Text: text, Color: color}}); err != nil {
			t.Fatal(err)
		}
	}

	out := attachTerminal(t, f, l.Pane, AttachView{StatusLine: true}, c)
	out.wait(t, "the title of a session nothing was rendered for", func(got string) bool {
		return strings.Contains(got, title("Agent Mux"))
	})
	if got := f.tmux(t, "display-message", "-p", "-t", l.Pane.ID, "#{status}"); got != "on" {
		t.Fatalf("status of a session attached from a plain terminal = %q, want on", got)
	}

	write("⚡api #[fg=red]x", "#89b4fa")
	out.wait(t, "the rendered title", func(got string) bool { return strings.Contains(got, title("⚡api #[fg=red]x")) })
	out.wait(t, "the status line on the session's color", func(got string) bool {
		return strings.Contains(got, c.background) && strings.Contains(got, " ⚡api #[fg=red]x ")
	})

	write("✅api", "")
	out.wait(t, "the title to follow the status", func(got string) bool { return strings.Contains(got, title("✅api")) })
	out.wait(t, "the status line to follow the status", func(got string) bool { return strings.Contains(got, "\n ✅api") })

	// The workspace's terminal takes the session over and shows no line; the
	// title is still the session's.
	hosted := attachTerminal(t, f, l.Pane, AttachView{}, c)
	hosted.wait(t, "the hosted terminal's title", func(got string) bool { return strings.Contains(got, title("✅api")) })
	if got := f.tmux(t, "display-message", "-p", "-t", l.Pane.ID, "#{status}"); got != "off" {
		t.Fatalf("status of a session attached by the workspace = %q, want off", got)
	}
	if strings.Contains(hosted.received(), "\n ✅api") {
		t.Fatalf("the hosted terminal was shown a status line: %q", hosted.received())
	}
}
