package testutil

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// recorder stands in for a *testing.T so a failure can be observed instead of
// failing this test. Fatalf ends the goroutine the way t.Fatalf does.
type recorder struct {
	testing.TB
	failed  bool
	message string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.failed = true
	r.message = fmt.Sprintf(format, args...)
	panic(r)
}

func run(r *recorder, f func()) {
	defer func() {
		if v := recover(); v != nil && v != r {
			panic(v)
		}
	}()
	f()
}

func TestEventuallyReturnsOnceTheConditionHolds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var checks atomic.Int32
		start := time.Now()
		r := &recorder{TB: t}
		run(r, func() {
			Eventually(r, "the third check", func() bool { return checks.Add(1) == 3 }, Every(time.Second))
		})
		if r.failed {
			t.Fatalf("Eventually failed: %s", r.message)
		}
		if got := checks.Load(); got != 3 {
			t.Fatalf("checks = %d, want 3", got)
		}
		if waited := time.Since(start); waited != 2*time.Second {
			t.Fatalf("waited %s, want two intervals", waited)
		}
	})
}

func TestEventuallyFailsWithWhatItAwaitedAndTheDetail(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var checks atomic.Int32
		start := time.Now()
		r := &recorder{TB: t}
		run(r, func() {
			Eventually(r, "the impossible", func() bool { checks.Add(1); return false },
				Within(time.Second), Every(300*time.Millisecond), Detail(func() string { return "last seen: nothing" }))
		})
		if !r.failed {
			t.Fatal("Eventually passed a condition that never held")
		}
		for _, want := range []string{"the impossible", "last seen: nothing", "1s"} {
			if !strings.Contains(r.message, want) {
				t.Errorf("failure %q does not name %q", r.message, want)
			}
		}
		// Checked at 0, 300, 600, 900, and once more at 1200ms, past the deadline.
		if got := checks.Load(); got != 5 {
			t.Errorf("checks = %d, want 5", got)
		}
		if waited := time.Since(start); waited != 1200*time.Millisecond {
			t.Errorf("waited %s, want 1.2s", waited)
		}
	})
}

func TestEventuallyDefaults(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		r := &recorder{TB: t}
		run(r, func() { Eventually(r, "never", func() bool { return false }) })
		if !r.failed {
			t.Fatal("Eventually passed a condition that never held")
		}
		if waited := time.Since(start); waited < DefaultTimeout || waited > DefaultTimeout+DefaultInterval {
			t.Errorf("waited %s, want the default timeout %s", waited, DefaultTimeout)
		}
	})
}

func TestPollReportsInsteadOfFailing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		if Poll(time.Second, 100*time.Millisecond, func() bool { return false }) {
			t.Fatal("Poll held for a condition that never did")
		}
		if !Poll(0, time.Hour, func() bool { return true }) {
			t.Fatal("Poll with no time left must still check once")
		}
	})
}
