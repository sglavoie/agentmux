package tmux

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
)

// launchReader starts a pane whose program records every byte it reads, raw,
// after writing setup to its terminal and then a ready marker.
func (f *fixture) launchReader(t *testing.T, id, setup string) (Launched, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "read")
	l := f.launch(t, id, []string{
		"/bin/sh", "-c", `stty raw -echo; printf "$2"; printf 'reader-ready'; exec cat > "$1"`, "amx", out, setup,
	}, nil)
	f.observeUntil(t, l.Pane, contains("reader-ready"))
	return l, out
}

// readUntil waits for the reader's file to hold want.
func readUntil(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(path)
		if string(data) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane read %q, want %q", data, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSendPastesTextAsOnePasteThenSubmits(t *testing.T) {
	f := newFixture(t, Options{})
	// A program that asked for bracketed paste, as the harnesses do.
	l, out := f.launchReader(t, "paste", `\033[?2004h`)
	if err := f.backend.Send(l.Pane, "", backend.Input{Text: "first line\nsecond; line", Submit: true}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, out, "\x1b[200~first line\rsecond; line\x1b[201~\r")
	if buffers := f.tmux(t, "list-buffers"); buffers != "" {
		t.Errorf("the paste left a buffer on the server: %q", buffers)
	}
	if clients := f.tmux(t, "list-clients"); clients != "" {
		t.Errorf("sending attached a client: %q", clients)
	}
}

func TestSendWithoutSubmitLeavesTheTextUnentered(t *testing.T) {
	f := newFixture(t, Options{})
	l, out := f.launchReader(t, "draft", "")
	if err := f.backend.Send(l.Pane, "", backend.Input{Text: "draft"}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, out, "draft")
}

func TestSendPressesNamedKeysInOrder(t *testing.T) {
	f := newFixture(t, Options{})
	l, out := f.launchReader(t, "keys", "")
	keys := []backend.Key{"2", backend.KeyYes, backend.KeyDown, backend.KeyShiftTab, backend.KeyEscape, backend.KeyEnter}
	if err := f.backend.Send(l.Pane, "", backend.Input{Keys: keys}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, out, "2y\x1b[B\x1b[Z\x1b\r")
}

func TestSendRefusesWhatAPaneCouldMisread(t *testing.T) {
	f := newFixture(t, Options{})
	l, out := f.launchReader(t, "refuse", "")
	for name, input := range map[string]backend.Input{
		"nothing":          {},
		"blank":            {Text: " \n"},
		"both":             {Text: "a", Keys: []backend.Key{backend.KeyEnter}},
		"submit alone":     {Submit: true},
		"paste terminator": {Text: "a\x1b[201~b"},
		"carriage return":  {Text: "a\rb"},
		"unknown key":      {Keys: []backend.Key{"C-c"}},
		"too large":        {Text: strings.Repeat("a", backend.InputMaxBytes+1)},
	} {
		if err := f.backend.Send(l.Pane, "", input); !errors.Is(err, backend.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if err := f.backend.Send(l.Group, "", backend.Input{Text: "a"}); !errors.Is(err, backend.ErrInvalidRef) {
		t.Errorf("group ref: err = %v, want ErrInvalidRef", err)
	}
	// Proof that none of it was written: the next thing read is this.
	if err := f.backend.Send(l.Pane, "", backend.Input{Text: "only"}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, out, "only")
}

func TestSendToAMissingPaneKeepsTheTextOutOfTheErrorAndTheServer(t *testing.T) {
	f := newFixture(t, Options{})
	f.launchReader(t, "other", "")
	err := f.backend.Send(backend.PaneID("%987"), "", backend.Input{Text: "secret-prompt", Submit: true})
	if err == nil {
		t.Fatal("sending to a pane that does not exist succeeded")
	}
	if strings.Contains(err.Error(), "secret-prompt") {
		t.Errorf("the error carries the text: %v", err)
	}
	if buffers := f.tmux(t, "list-buffers"); buffers != "" {
		t.Errorf("a failed paste left its buffer on the server: %q", buffers)
	}
}

func TestSendNamingTheServerWritesAndNamingAnotherWritesNothing(t *testing.T) {
	f := newFixture(t, Options{})
	l, out := f.launchReader(t, "guarded", "")
	instance, err := f.backend.Instance()
	if err != nil {
		t.Fatal(err)
	}
	err = f.backend.Send(l.Pane, "1-1", backend.Input{Text: "not-for-you", Submit: true})
	if !errors.Is(err, backend.ErrStaleGeneration) {
		t.Fatalf("send naming another server = %v, want ErrStaleGeneration", err)
	}
	if buffers := f.tmux(t, "list-buffers"); buffers != "" {
		t.Errorf("a refused paste left its buffer on the server: %q", buffers)
	}
	if err := f.backend.Send(l.Pane, "1-1", backend.Input{Keys: []backend.Key{backend.KeyYes}}); !errors.Is(err, backend.ErrStaleGeneration) {
		t.Fatalf("keys naming another server = %v, want ErrStaleGeneration", err)
	}
	if err := f.backend.Send(l.Pane, instance.Generation, backend.Input{Text: "for-you", Submit: true}); err != nil {
		t.Fatal(err)
	}
	// Only what was sent to this server reached the pane.
	readUntil(t, out, "for-you\r")
}
