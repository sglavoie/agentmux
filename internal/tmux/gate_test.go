package tmux

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/backend"
)

// The gate's promise is a ceiling: however many callers ask at once, no more
// than its limit run, the rest wait in turn, and every one of them is
// answered or told that it timed out.
func TestClientGateNeverAdmitsMoreThanItsLimit(t *testing.T) {
	t.Parallel()

	const limit, callers = 3, 40
	gate := newClientGate(limit)
	release := make(chan struct{})
	var admitted sync.WaitGroup
	var asking sync.WaitGroup
	var done sync.WaitGroup
	admitted.Add(limit)
	asking.Add(callers - limit)
	for i := 0; i < callers; i++ {
		done.Add(1)
		go func(first bool) {
			defer done.Done()
			if !first {
				asking.Done()
			}
			finish, err := gate.acquire(context.Background())
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			if first {
				admitted.Done()
			}
			<-release
			finish(false, false)
		}(i < limit)
		if i == limit-1 {
			// The first callers hold every slot before the rest arrive, so
			// the rest are certain to queue.
			admitted.Wait()
		}
	}
	// Everyone past the limit is queued, not running.
	deadline := time.Now().Add(5 * time.Second)
	for gate.snapshot().InFlight != limit {
		if time.Now().After(deadline) {
			t.Fatalf("in flight = %d; want %d", gate.snapshot().InFlight, limit)
		}
		time.Sleep(time.Millisecond)
	}
	// A goroutine that was started has not necessarily run: released at once,
	// the slots could come free before anyone else asked, and nobody would
	// queue. Every other caller has reached its acquire, and the pause gives
	// each the processor to block in it, however few processors there are.
	asking.Wait()
	time.Sleep(20 * time.Millisecond)
	close(release)
	done.Wait()

	stats := gate.snapshot()
	if stats.MaxInFlight != limit || stats.InFlight != 0 {
		t.Errorf("max in flight = %d, in flight = %d; want %d and 0", stats.MaxInFlight, stats.InFlight, limit)
	}
	// How many queued depends on how many had asked by the time the slots
	// came free; that some did, and none past the limit ran, is the point.
	if stats.Commands != callers || stats.Queued == 0 || stats.Queued > callers-limit || stats.Failures != 0 || stats.Timeouts != 0 {
		t.Errorf("stats = %+v; want %d commands, some queued, no failure", stats, callers)
	}
}

// A caller that cannot get a slot within its own timeout is told so, and
// starts nothing.
func TestClientGateTimesOutAQueuedCaller(t *testing.T) {
	t.Parallel()

	gate := newClientGate(1)
	finish, err := gate.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := gate.acquire(ctx); !errors.Is(err, ErrClientQueueTimeout) {
		t.Fatalf("acquire with every slot taken = %v; want ErrClientQueueTimeout", err)
	}
	finish(true, false)

	stats := gate.snapshot()
	if stats.Commands != 1 || stats.Timeouts != 1 || stats.Failures != 2 || stats.Queued != 1 || stats.InFlight != 0 {
		t.Errorf("stats = %+v; want one command run, one queued caller timed out, two failures", stats)
	}
	if stats.MaxQueue < 30*time.Millisecond {
		t.Errorf("max queue = %s; want the timed-out wait counted", stats.MaxQueue)
	}
	// The slot it was waiting for is free again.
	if finish, err = gate.acquire(context.Background()); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	finish(false, false)
}

// The same ceiling against a real server: thirty panes read at once start at
// most MaxClientCommands tmux clients at a time, and every read is answered.
func TestConcurrentCapturesStayWithinTheClientLimit(t *testing.T) {
	f := newFixture(t, Options{})
	const panes = 30
	refs := make([]backend.Ref, panes)
	for i := range refs {
		marker := fmt.Sprintf("gate-%02d", i)
		refs[i] = f.launch(t, marker, echoArgv(marker), nil).Pane
	}

	before := Stats()
	const rounds = 3
	errs := make(chan error, panes*rounds)
	var wg sync.WaitGroup
	for round := 0; round < rounds; round++ {
		for i, ref := range refs {
			wg.Add(1)
			go func(i int, ref backend.Ref) {
				defer wg.Done()
				text, err := f.backend.Capture(ref, backend.ExtentScreen)
				if err == nil && !strings.Contains(text, fmt.Sprintf("gate-%02d", i)) {
					err = fmt.Errorf("pane %d read another pane's screen: %q", i, text)
				}
				errs <- err
			}(i, ref)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}

	after := Stats()
	if after.MaxInFlight > MaxClientCommands {
		t.Errorf("max tmux clients at once = %d; the limit is %d", after.MaxInFlight, MaxClientCommands)
	}
	if got := after.Commands - before.Commands; got < panes*rounds {
		t.Errorf("commands = %d; want at least %d", got, panes*rounds)
	}
	if after.Queued == before.Queued {
		t.Errorf("%d concurrent reads never queued behind a limit of %d", panes*rounds, MaxClientCommands)
	}
	if after.Timeouts != before.Timeouts {
		t.Errorf("timeouts went from %d to %d", before.Timeouts, after.Timeouts)
	}
	if after.InFlight != 0 {
		t.Errorf("in flight = %d after every read returned", after.InFlight)
	}
}

func TestFailureClassIsAFixedVocabulary(t *testing.T) {
	t.Parallel()

	secret := "/Users/someone/project --prompt hunter2"
	cases := []struct {
		err  error
		want string
	}{
		{nil, ""},
		{&CommandError{Args: []string{"list-panes"}, Stderr: "no server running on " + secret}, "server_not_running"},
		{&CommandError{Args: []string{"capture-pane", secret}, Err: fmt.Errorf("timed out after 5s: %w", context.DeadlineExceeded)}, "timeout"},
		{&CommandError{Args: []string{"x"}, Err: fmt.Errorf("timed out after 5s: %w", ErrClientQueueTimeout)}, "timeout"},
		{&NoRuntimeError{}, "tmux_unavailable"},
		{&CommandError{Args: []string{"capture-pane", secret}, Stderr: "can't find pane: " + secret}, "command_failed"},
		{fmt.Errorf("reading %s", filepath.Join(secret, "runtime-sessions.json")), "unreadable"},
	}
	for _, tc := range cases {
		got := FailureClass(tc.err)
		if got != tc.want {
			t.Errorf("FailureClass(%v) = %q; want %q", tc.err, got, tc.want)
		}
		if strings.ContainsAny(got, " /") {
			t.Errorf("FailureClass(%v) = %q carries more than a class", tc.err, got)
		}
	}
}
