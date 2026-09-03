package kitty

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"
)

// listPayload is a topology with every nested slice the deep copy must own:
// tabs, windows, a window cmdline, and a foreground process cmdline.
const listPayload = `[{"id":1,"tabs":[{"id":10,"title":"tab","windows":[` +
	`{"id":100,"title":"win","cmdline":["amx","--debug"],` +
	`"foreground_processes":[{"pid":7,"cmdline":["claude","--resume"],"cwd":"/tmp"}],` +
	`"user_vars":{"amx_profile":"work"}}]}]}]`

// scriptedRunner is a CommandRunner whose behavior a test controls per socket.
// run is invoked without the runner's lock held so a test may block inside it
// to hold an underlying execution open.
type scriptedRunner struct {
	mu    sync.Mutex
	calls map[string]int
	run   func(socket string) ([]byte, []byte, error)
}

func (r *scriptedRunner) Run(_ string, args ...string) ([]byte, []byte, error) {
	socket := socketFromArgs(args)
	r.mu.Lock()
	if r.calls == nil {
		r.calls = make(map[string]int)
	}
	r.calls[socket]++
	r.mu.Unlock()
	return r.run(socket)
}

func (r *scriptedRunner) count(socket string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[socket]
}

func socketFromArgs(args []string) string {
	for i, arg := range args {
		if arg == "--to" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func okRunner(payload string) *scriptedRunner {
	return &scriptedRunner{run: func(string) ([]byte, []byte, error) {
		return []byte(payload), nil, nil
	}}
}

// scriptClock returns scripted instants so latency and failure timestamps are
// exact rather than merely plausible. The coordinator reads it exactly twice
// per underlying attempt and never for a waiter, which is what lets these
// tests pin "underlying attempt latency, not waiter latency".
type scriptClock struct {
	mu      sync.Mutex
	base    time.Time
	offsets []time.Duration
	next    int
}

func (c *scriptClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	off := c.offsets[len(c.offsets)-1]
	if c.next < len(c.offsets) {
		off = c.offsets[c.next]
	}
	c.next++
	return c.base.Add(off)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestListCoordinator_SameSocketRunsOnceAndFansOut(t *testing.T) {
	const waiters = 8

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	runner := &scriptedRunner{run: func(string) ([]byte, []byte, error) {
		once.Do(func() { close(entered) })
		<-release
		return []byte(listPayload), nil, nil
	}}
	client := NewClientWithSocket(runner, "sock-a")
	client.lists.now = (&scriptClock{offsets: []time.Duration{0, 40 * time.Millisecond}}).now

	results := make([][]OSWindow, waiters+1)
	errs := make([]error, waiters+1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		results[0], errs[0] = client.ListWindows()
	}()
	<-entered

	for i := 1; i <= waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = client.ListWindows()
		}(i)
	}
	waitFor(t, "waiters to join the in-flight read", func() bool {
		st, _ := client.ListStatus()
		return st.CoalescedWaiters == waiters
	})

	status, ok := client.ListStatus()
	if !ok || !status.InFlight {
		t.Fatalf("expected the socket to be in flight, got %+v (ok=%v)", status, ok)
	}

	close(release)
	wg.Wait()

	if got := runner.count("sock-a"); got != 1 {
		t.Fatalf("underlying executions: got %d, want 1", got)
	}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if len(results[i]) != 1 || len(results[i][0].Tabs) != 1 {
			t.Fatalf("caller %d received %+v, want the decoded topology", i, results[i])
		}
	}

	status, _ = client.ListStatus()
	if status.Attempts != 1 || status.CoalescedWaiters != waiters {
		t.Fatalf("accounting: attempts=%d waiters=%d, want 1 and %d", status.Attempts, status.CoalescedWaiters, waiters)
	}
	if status.InFlight || status.Failures != 0 || status.ConsecutiveFailures != 0 {
		t.Fatalf("unexpected failure accounting after a success: %+v", status)
	}
	// The waiters all joined after the execution started and returned when it
	// ended, so a waiter-derived latency would be shorter than the attempt's.
	if status.LastLatency != 40*time.Millisecond || status.MaxLatency != 40*time.Millisecond {
		t.Fatalf("latency: last=%v max=%v, want 40ms for the underlying attempt", status.LastLatency, status.MaxLatency)
	}
}

func TestListCoordinator_DifferentSocketsAreIndependent(t *testing.T) {
	enteredA := make(chan struct{})
	enteredB := make(chan struct{})
	runner := &scriptedRunner{run: func(socket string) ([]byte, []byte, error) {
		// Each socket blocks until the other has started, so the test cannot
		// pass unless both run concurrently.
		switch socket {
		case "sock-a":
			close(enteredA)
			<-enteredB
		case "sock-b":
			close(enteredB)
			<-enteredA
		}
		return []byte(listPayload), nil, nil
	}}
	base := NewClientWithSocket(runner, "")

	var wg sync.WaitGroup
	for _, socket := range []string{"sock-a", "sock-b"} {
		wg.Add(1)
		go func(socket string) {
			defer wg.Done()
			if _, err := base.WithSocket(socket).ListWindows(); err != nil {
				t.Errorf("%s: %v", socket, err)
			}
		}(socket)
	}
	wg.Wait()

	for _, socket := range []string{"sock-a", "sock-b"} {
		if got := runner.count(socket); got != 1 {
			t.Fatalf("%s executions: got %d, want 1", socket, got)
		}
		status, ok := base.lists.status(socket)
		if !ok {
			t.Fatalf("%s: no retained status", socket)
		}
		if status.Attempts != 1 || status.CoalescedWaiters != 0 {
			t.Fatalf("%s accounting: %+v, want one uncoalesced attempt", socket, status)
		}
	}
}

func TestListCoordinator_SharedByWithSocketClones(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	runner := &scriptedRunner{run: func(string) ([]byte, []byte, error) {
		once.Do(func() { close(entered) })
		<-release
		return []byte(listPayload), nil, nil
	}}

	base := NewClientWithSocket(runner, "")
	first := base.WithSocket("sock-shared")
	second := base.WithSocket("sock-shared")
	if base.lists != first.lists || first.lists != second.lists {
		t.Fatal("WithSocket clones must retain the base client's list coordinator")
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := first.ListWindows(); err != nil {
			t.Errorf("first clone: %v", err)
		}
	}()
	<-entered

	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := second.ListWindows(); err != nil {
			t.Errorf("second clone: %v", err)
		}
	}()
	waitFor(t, "the second clone to join the first clone's read", func() bool {
		st, _ := second.ListStatus()
		return st.CoalescedWaiters == 1
	})

	close(release)
	wg.Wait()

	if got := runner.count("sock-shared"); got != 1 {
		t.Fatalf("underlying executions across clones: got %d, want 1", got)
	}
}

func TestListCoordinator_CallersOwnIndependentCopies(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	runner := &scriptedRunner{run: func(string) ([]byte, []byte, error) {
		once.Do(func() { close(entered) })
		<-release
		return []byte(listPayload), nil, nil
	}}
	client := NewClientWithSocket(runner, "sock-copy")

	var executor, waiter []OSWindow
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		executor, err = client.ListWindows()
		if err != nil {
			t.Errorf("executor: %v", err)
		}
	}()
	<-entered
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		waiter, err = client.ListWindows()
		if err != nil {
			t.Errorf("waiter: %v", err)
		}
	}()
	waitFor(t, "the waiter to join", func() bool {
		st, _ := client.ListStatus()
		return st.CoalescedWaiters == 1
	})
	close(release)
	wg.Wait()

	if &executor[0] == &waiter[0] {
		t.Fatal("callers received the same backing array")
	}
	if &executor[0].Tabs[0] == &waiter[0].Tabs[0] {
		t.Fatal("callers share tab storage")
	}
	if &executor[0].Tabs[0].Windows[0] == &waiter[0].Tabs[0].Windows[0] {
		t.Fatal("callers share window storage")
	}

	// Mutating one caller's tree at every depth must leave the other's intact.
	executor[0].Tabs[0].Title = "mutated"
	executor[0].Tabs[0].Windows[0].Title = "mutated"
	executor[0].Tabs[0].Windows[0].Cmdline[0] = "mutated"
	executor[0].Tabs[0].Windows[0].ForegroundProcesses[0].Cmdline[0] = "mutated"
	executor[0].Tabs = nil

	win := waiter[0].Tabs[0].Windows[0]
	if waiter[0].Tabs[0].Title != "tab" || win.Title != "win" {
		t.Fatalf("waiter observed the executor's mutation: %+v", waiter[0].Tabs[0])
	}
	if win.Cmdline[0] != "amx" {
		t.Fatalf("waiter shares the window cmdline array: %v", win.Cmdline)
	}
	if win.ForegroundProcesses[0].Cmdline[0] != "claude" {
		t.Fatalf("waiter shares the foreground process cmdline array: %v", win.ForegroundProcesses[0].Cmdline)
	}
}

func TestListCoordinator_ClassifiesFailures(t *testing.T) {
	timeoutErr := fmt.Errorf("%w: signal: killed", context.DeadlineExceeded)

	cases := []struct {
		name      string
		stdout    string
		runErr    error
		wantClass ListErrorClass
		wantTimes uint64
	}{
		{name: "timeout", runErr: timeoutErr, wantClass: ListErrorTimeout, wantTimes: 1},
		{name: "command", runErr: errors.New("kitten: connection refused"), wantClass: ListErrorCommand},
		{name: "decode", stdout: "not-json", wantClass: ListErrorDecode},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &scriptedRunner{run: func(string) ([]byte, []byte, error) {
				return []byte(tc.stdout), nil, tc.runErr
			}}
			client := NewClientWithSocket(runner, "sock-fail")
			clock := &scriptClock{offsets: []time.Duration{0, 7 * time.Millisecond}}
			client.lists.now = clock.now

			windows, err := client.ListWindows()
			if err == nil {
				t.Fatal("expected an error")
			}
			if windows != nil {
				t.Fatalf("a failed read must return no topology, got %+v", windows)
			}

			status, ok := client.ListStatus()
			if !ok {
				t.Fatal("no retained status")
			}
			if status.Attempts != 1 || status.Failures != 1 || status.ConsecutiveFailures != 1 {
				t.Fatalf("accounting: %+v, want one failed attempt", status)
			}
			if status.Timeouts != tc.wantTimes {
				t.Fatalf("timeouts: got %d, want %d", status.Timeouts, tc.wantTimes)
			}
			if status.LastErrorClass != tc.wantClass {
				t.Fatalf("error class: got %q, want %q", status.LastErrorClass, tc.wantClass)
			}
			if status.LastLatency != 7*time.Millisecond {
				t.Fatalf("failed attempts must still be timed, got %v", status.LastLatency)
			}
			if !status.LastFailure.Equal(clock.base.Add(7 * time.Millisecond)) {
				t.Fatalf("last failure: got %v, want the attempt's completion instant", status.LastFailure)
			}
		})
	}
}

func TestListCoordinator_ReportsOnlyEdgeTransitions(t *testing.T) {
	var mode struct {
		mu   sync.Mutex
		fail bool
	}
	setFail := func(v bool) {
		mode.mu.Lock()
		defer mode.mu.Unlock()
		mode.fail = v
	}
	runner := &scriptedRunner{run: func(string) ([]byte, []byte, error) {
		mode.mu.Lock()
		defer mode.mu.Unlock()
		if mode.fail {
			return nil, nil, errors.New("kitten unavailable")
		}
		return []byte(listPayload), nil, nil
	}}

	client := NewClientWithSocket(runner, "sock-edge")
	var seen []ListTransition
	client.SetListObserver(func(tr ListTransition) {
		// Reading status from inside the observer proves the transition is
		// emitted with no coordinator lock held.
		if _, ok := client.ListStatus(); !ok {
			t.Error("observer could not read status")
		}
		seen = append(seen, tr)
	})

	setFail(true)
	for i := 0; i < 3; i++ {
		if _, err := client.ListWindows(); err == nil {
			t.Fatalf("failure %d: expected an error", i)
		}
	}
	status, _ := client.ListStatus()
	if status.ConsecutiveFailures != 3 || status.Failures != 3 {
		t.Fatalf("failure streak: %+v", status)
	}

	setFail(false)
	for i := 0; i < 2; i++ {
		if _, err := client.ListWindows(); err != nil {
			t.Fatalf("recovery %d: %v", i, err)
		}
	}

	if len(seen) != 2 {
		t.Fatalf("transitions: got %d (%+v), want exactly one failure and one recovery", len(seen), seen)
	}
	if seen[0].Kind != ListTransitionFailed || seen[0].Err == nil {
		t.Fatalf("first transition: %+v, want a failure carrying its error", seen[0])
	}
	if seen[0].Status.ConsecutiveFailures != 1 {
		t.Fatalf("failure transition must report the first failure of the streak: %+v", seen[0].Status)
	}
	if seen[1].Kind != ListTransitionRecovered || seen[1].Err != nil {
		t.Fatalf("second transition: %+v, want a recovery with no error", seen[1])
	}

	status, _ = client.ListStatus()
	if status.ConsecutiveFailures != 0 {
		t.Fatalf("recovery must reset the streak counter, got %d", status.ConsecutiveFailures)
	}
	if status.Failures != 3 || status.Attempts != 5 {
		t.Fatalf("cumulative accounting must survive recovery: %+v", status)
	}
	// The retained failure evidence describes the last failure and is not
	// erased by a recovery; ConsecutiveFailures is what says "healthy now".
	if status.LastErrorClass != ListErrorCommand || status.LastFailure.IsZero() {
		t.Fatalf("recovery must retain the last failure evidence: %+v", status)
	}
}

func TestListCoordinator_EvictsOldestIdleSockets(t *testing.T) {
	runner := okRunner(listPayload)
	base := NewClientWithSocket(runner, "")

	const total = maxRetainedListSockets + 4
	for i := 0; i < total; i++ {
		if _, err := base.WithSocket("sock-" + strconv.Itoa(i)).ListWindows(); err != nil {
			t.Fatalf("socket %d: %v", i, err)
		}
	}

	retained := base.ListStatuses()
	if len(retained) != maxRetainedListSockets {
		t.Fatalf("retained keys: got %d, want %d", len(retained), maxRetainedListSockets)
	}
	for i := 0; i < total; i++ {
		socket := "sock-" + strconv.Itoa(i)
		_, ok := base.lists.status(socket)
		wantOK := i >= total-maxRetainedListSockets
		if ok != wantOK {
			t.Fatalf("%s retained=%v, want %v (oldest idle keys are evicted first)", socket, ok, wantOK)
		}
	}
}

func TestListCoordinator_NeverEvictsInFlightSocket(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	runner := &scriptedRunner{run: func(socket string) ([]byte, []byte, error) {
		if socket == "sock-held" {
			once.Do(func() { close(entered) })
			<-release
		}
		return []byte(listPayload), nil, nil
	}}
	base := NewClientWithSocket(runner, "")

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := base.WithSocket("sock-held").ListWindows(); err != nil {
			t.Errorf("held socket: %v", err)
		}
	}()
	<-entered

	// Churn far more idle keys than the bound while the held socket is still
	// in flight. Its entry owns a running execution, so it must survive.
	for i := 0; i < maxRetainedListSockets*2; i++ {
		if _, err := base.WithSocket("sock-churn-" + strconv.Itoa(i)).ListWindows(); err != nil {
			t.Fatalf("churn %d: %v", i, err)
		}
	}

	held, ok := base.lists.status("sock-held")
	if !ok {
		t.Fatal("an in-flight socket key was evicted")
	}
	if !held.InFlight {
		t.Fatalf("held socket should still be in flight: %+v", held)
	}
	if got := len(base.ListStatuses()); got != maxRetainedListSockets+1 {
		t.Fatalf("retained keys: got %d, want %d idle plus the in-flight one", got, maxRetainedListSockets)
	}

	close(release)
	wg.Wait()

	if got := len(base.ListStatuses()); got != maxRetainedListSockets {
		t.Fatalf("after the held read completes, retained keys: got %d, want %d", got, maxRetainedListSockets)
	}
}

func TestListCoordinator_RaceSafety(t *testing.T) {
	var counter struct {
		mu sync.Mutex
		n  int
	}
	runner := &scriptedRunner{run: func(string) ([]byte, []byte, error) {
		counter.mu.Lock()
		counter.n++
		n := counter.n
		counter.mu.Unlock()
		switch n % 3 {
		case 0:
			return nil, nil, errors.New("kitten unavailable")
		case 1:
			return []byte("not-json"), nil, nil
		default:
			return []byte(listPayload), nil, nil
		}
	}}

	base := NewClientWithSocket(runner, "")
	var observed struct {
		mu sync.Mutex
		n  int
	}
	base.SetListObserver(func(ListTransition) {
		observed.mu.Lock()
		observed.n++
		observed.mu.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client := base.WithSocket("sock-" + strconv.Itoa(i%4))
			for j := 0; j < 12; j++ {
				windows, err := client.ListWindows()
				if err == nil {
					// Mutate the returned tree: with a deep copy per caller
					// this is safe even while others read concurrently.
					for k := range windows {
						windows[k].Tabs = nil
					}
				}
				client.ListStatus()
				client.ListStatuses()
			}
		}(i)
	}
	wg.Wait()

	for _, status := range base.ListStatuses() {
		if status.InFlight {
			t.Fatalf("%s left in flight: %+v", status.Socket, status)
		}
		if status.Failures > status.Attempts {
			t.Fatalf("%s recorded more failures than attempts: %+v", status.Socket, status)
		}
		if status.Timeouts != 0 {
			t.Fatalf("%s recorded a timeout it never saw: %+v", status.Socket, status)
		}
	}
}
