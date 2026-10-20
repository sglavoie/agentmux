package testutil

import (
	"testing"
	"time"
)

// DefaultTimeout and DefaultInterval are Eventually's bounds when no option
// changes them. The timeout only matters to a failing test, so it is generous:
// a loaded machine must not turn a slow pass into a failure.
const (
	DefaultTimeout  = 10 * time.Second
	DefaultInterval = 10 * time.Millisecond
)

// Option adjusts one Eventually call.
type Option func(*poll)

type poll struct {
	timeout  time.Duration
	interval time.Duration
	detail   func() string
}

// Within sets how long Eventually waits before failing the test.
func Within(timeout time.Duration) Option {
	return func(p *poll) { p.timeout = timeout }
}

// Every sets how long Eventually sleeps between checks.
func Every(interval time.Duration) Option {
	return func(p *poll) { p.interval = interval }
}

// Detail adds what detail reports, read once at the timeout, to the failure,
// so the failure can say what was last observed rather than only what was
// awaited.
func Detail(detail func() string) Option {
	return func(p *poll) { p.detail = detail }
}

// Eventually checks cond until it holds, failing t with what when it has not
// held within the timeout. cond is always checked at least once, and once
// more at the deadline, so a condition that comes true during the last sleep
// still passes.
func Eventually(t testing.TB, what string, cond func() bool, opts ...Option) {
	t.Helper()
	p := poll{timeout: DefaultTimeout, interval: DefaultInterval}
	for _, opt := range opts {
		opt(&p)
	}
	if Poll(p.timeout, p.interval, cond) {
		return
	}
	if p.detail != nil {
		t.Fatalf("timed out after %s waiting for %s: %s", p.timeout, what, p.detail())
	}
	t.Fatalf("timed out after %s waiting for %s", p.timeout, what)
}

// Poll checks cond every interval until it holds or timeout passes, and
// reports whether it held. It is Eventually for a caller that decides for
// itself what a timeout means.
func Poll(timeout, interval time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}
