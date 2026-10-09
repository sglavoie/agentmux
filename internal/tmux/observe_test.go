package tmux

import (
	"errors"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
)

// observeUntil polls Observe until the screen satisfies ok, so a test never
// races the pane program's first write.
func (f *fixture) observeUntil(t *testing.T, ref backend.Ref, ok func(backend.Screen) bool) backend.Screen {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		screen, err := f.backend.Observe(ref)
		if err == nil && ok(screen) {
			return screen
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane %s never showed the expected screen; err=%v screen=%+v", ref.ID, err, screen)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func contains(marker string) func(backend.Screen) bool {
	return func(s backend.Screen) bool { return strings.Contains(s.Text, marker) }
}

func TestObserveReportsScreenWithItsProvenance(t *testing.T) {
	f := newFixture(t, Options{})
	l := f.launch(t, "plain", echoArgv("plain-marker"), nil)
	before := time.Now()
	screen := f.observeUntil(t, l.Pane, contains("plain-marker"))

	instance, err := f.server.Instance()
	if err != nil {
		t.Fatal(err)
	}
	if screen.Instance != instance || screen.Pane != l.Pane {
		t.Errorf("observed %+v pane %+v, want instance %+v pane %+v", screen.Instance, screen.Pane, instance, l.Pane)
	}
	if screen.AlternateScreen || screen.Exited {
		t.Errorf("a running primary-screen pane observed as alternate=%v exited=%v", screen.AlternateScreen, screen.Exited)
	}
	if screen.CapturedAt.Before(before) || screen.CapturedAt.After(time.Now()) {
		t.Errorf("CapturedAt %v outside the read window", screen.CapturedAt)
	}
	cols, _ := strconv.Atoi(f.tmux(t, "display-message", "-p", "-t", l.Pane.ID, "#{pane_width}"))
	rows, _ := strconv.Atoi(f.tmux(t, "display-message", "-p", "-t", l.Pane.ID, "#{pane_height}"))
	if screen.Cols != cols || screen.Rows != rows || cols == 0 || rows == 0 {
		t.Errorf("observed %dx%d, tmux reports %dx%d", screen.Cols, screen.Rows, cols, rows)
	}
	if got := strings.Count(screen.Text, "\n"); got != rows {
		t.Errorf("capture has %d rows, pane has %d", got, rows)
	}
	plain, err := f.backend.Capture(l.Pane, backend.ExtentScreen)
	if err != nil || plain != screen.Text {
		t.Errorf("Observe text differs from Capture(screen): %q vs %q (%v)", screen.Text, plain, err)
	}
	t.Logf("observed: %dx%d pane %s of generation %s captured %d rows identical to Capture(screen)", cols, rows, l.Pane.ID, instance.Generation, rows)
}

func TestObserveHandlesWrapAnsiAndAlternateScreen(t *testing.T) {
	f := newFixture(t, Options{})
	long := strings.Repeat("w", 100)
	// Colour on the primary screen, then a line longer than the pane.
	primary := f.launch(t, "primary", []string{
		"/bin/sh", "-c",
		`printf '\033[1;31mred-marker\033[0m\n%s\nend-marker\n' "$1"; exec sleep 600`, "amx", long,
	}, nil)
	screen := f.observeUntil(t, primary.Pane, contains("end-marker"))
	if strings.ContainsRune(screen.Text, '\x1b') {
		t.Errorf("observed text carries escape sequences: %q", screen.Text)
	}
	if !strings.Contains(screen.Text, "red-marker\n") {
		t.Errorf("coloured text lost: %q", screen.Text)
	}
	if screen.Cols >= len(long) {
		t.Fatalf("pane is %d columns; the wrap case needs fewer than %d", screen.Cols, len(long))
	}
	var wrapped []string
	for _, line := range strings.Split(screen.Text, "\n") {
		if len(line) > screen.Cols {
			t.Errorf("row %q is wider than the %d-column pane", line, screen.Cols)
		}
		if strings.HasPrefix(line, "w") {
			wrapped = append(wrapped, line)
		}
	}
	if len(wrapped) != 2 || len(wrapped[0]) != screen.Cols || wrapped[0]+wrapped[1] != long {
		t.Errorf("a %d-char line on a %d-column pane was captured as %q, want two rows", len(long), screen.Cols, wrapped)
	}

	// The alternate screen hides the primary one; the capture follows it.
	alt := f.launch(t, "alt", []string{
		"/bin/sh", "-c",
		`echo primary-marker; printf '\033[?1049h\033[H'; echo alt-marker; exec sleep 600`,
	}, nil)
	altScreen := f.observeUntil(t, alt.Pane, contains("alt-marker"))
	if !altScreen.AlternateScreen {
		t.Error("alternate screen not reported")
	}
	if strings.Contains(altScreen.Text, "primary-marker") {
		t.Errorf("alternate-screen capture shows the primary screen: %q", altScreen.Text)
	}
	t.Logf("observed: %d columns; a 100-char line captured as rows of %d and %d chars; the plain capture held no SGR bytes; alternate_on=1 captured only the alternate screen", screen.Cols, len(wrapped[0]), len(wrapped[1]))
}

func TestObserveNeverAnswersForAnotherPaneOrGeneration(t *testing.T) {
	f := newFixture(t, Options{})
	done := f.launch(t, "done", []string{"/bin/sh", "-c", `echo done-marker; exit 3`}, nil)
	live := f.launch(t, "live", echoArgv("live-marker"), nil)
	f.waitFor(t, "done", func(p backend.Pane) bool { return p.Exit != nil && p.Exit.Status == 3 })

	exited, err := f.backend.Observe(done.Pane)
	// tmux redraws a dead pane around its "Pane is dead" line, so the visible
	// screen need not hold the program's last output; Exited is the fact.
	if err != nil || !exited.Exited || exited.Rows == 0 {
		t.Errorf("Observe(exited pane) = %+v, %v; want Exited with a screen", exited, err)
	}

	if _, err := f.backend.Observe(live.Group); !errors.Is(err, backend.ErrInvalidRef) {
		t.Errorf("Observe(group) err = %v, want ErrInvalidRef", err)
	}
	if screen, err := f.backend.Observe(backend.PaneID("%9999")); err == nil {
		t.Errorf("Observe(missing pane) = %+v, want an error", screen)
	}
	if err := f.backend.Close(done.Group); err != nil {
		t.Fatal(err)
	}
	if screen, err := f.backend.Observe(done.Pane); err == nil {
		t.Errorf("Observe(closed pane) = %+v, want an error", screen)
	}

	before := f.observeUntil(t, live.Pane, contains("live-marker"))
	pid, err := strconv.Atoi(f.tmux(t, "display-message", "-p", "#{pid}"))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("server %d survived SIGKILL", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := f.backend.Observe(live.Pane); !errors.Is(err, ErrServerNotRunning) {
		t.Errorf("Observe after server death err = %v, want ErrServerNotRunning", err)
	}

	// A restarted server reuses pane IDs; only the generation tells them apart.
	next := f.launch(t, "next", echoArgv("next-marker"), nil)
	after := f.observeUntil(t, next.Pane, contains("next-marker"))
	if next.Pane != live.Pane {
		t.Logf("restarted server did not reuse %s (got %s)", live.Pane.ID, next.Pane.ID)
	}
	if after.Instance.Generation == before.Instance.Generation {
		t.Errorf("restarted server kept generation %s", after.Instance.Generation)
	}
	t.Logf("observed: exited pane Exited=true; group ref ErrInvalidRef; missing and closed panes error; dead server ErrServerNotRunning; pane %s of generation %s became pane %s of generation %s", live.Pane.ID, before.Instance.Generation, next.Pane.ID, after.Instance.Generation)
}
