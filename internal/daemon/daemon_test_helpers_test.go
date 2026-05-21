package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/kitty"
)

type notifyCall struct {
	title    string
	subtitle string
}

// callRecorder is a thread-safe collector for dispatcher invocations. The
// notify dispatch runs in its own goroutine, so every read from the test
// must hold the same mutex as the write — otherwise the race detector flags
// it. Both notify_test.go and handler_test.go use this helper to avoid
// hand-rolling per-test mutex bookkeeping.
type callRecorder struct {
	mu    sync.Mutex
	calls []notifyCall
}

func (r *callRecorder) add(c notifyCall) {
	r.mu.Lock()
	r.calls = append(r.calls, c)
	r.mu.Unlock()
}

func (r *callRecorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// snapshot returns a copy so tests can iterate without holding the mutex.
func (r *callRecorder) snapshot() []notifyCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]notifyCall, len(r.calls))
	copy(out, r.calls)
	return out
}

func newTestNotifier(cfg *config.NotificationConfig) (*Notifier, *callRecorder) {
	n := NewNotifier(cfg)
	rec := &callRecorder{}
	n.dispatch = func(_ context.Context, title, subtitle string) error {
		rec.add(notifyCall{title, subtitle})
		return nil
	}
	return n, rec
}

func newTestHandler() *Handler {
	guard := NewStateGuard(time.Hour)
	notifier := NewNotifier(&config.NotificationConfig{})
	notifier.dispatch = func(_ context.Context, _, _ string) error { return nil }
	return NewHandler(guard, notifier)
}

// newSweepTestHandler builds a Handler wired with a notifier that records
// dispatches and a fake Kitty client whose SetTabTitle calls are recorded.
// Threshold zero leaves the sweep disabled — used by the no-op test. The
// same threshold is applied to all three watchdog states so legacy tests
// that pre-date per-state thresholds continue to exercise the sweep
// uniformly; per-state behavior is covered by newSweepTestHandlerWithMap.
func newSweepTestHandler(threshold time.Duration) (*Handler, *fakeKittyClient, *callRecorder) {
	guard := NewStateGuard(time.Hour)
	notifier, rec := newTestNotifier(&config.NotificationConfig{})
	h := NewHandler(guard, notifier)
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	h.SetStaleThresholds(map[string]time.Duration{
		"active":   threshold,
		"subagent": threshold,
		"compact":  threshold,
	}, nil)
	return h, fake, rec
}

// newSweepTestHandlerWithMap is a sibling of newSweepTestHandler that takes
// per-action thresholds so per-state tests can configure different durations
// for active/subagent/compact independently.
func newSweepTestHandlerWithMap(global map[string]time.Duration, perAccount map[string]map[string]time.Duration) (*Handler, *fakeKittyClient, *callRecorder) {
	guard := NewStateGuard(time.Hour)
	notifier, rec := newTestNotifier(&config.NotificationConfig{})
	h := NewHandler(guard, notifier)
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	h.SetStaleThresholds(global, perAccount)
	return h, fake, rec
}

// newHookSilenceTestHandler builds a Handler wired with a recording notifier
// and a fake Kitty client. The global / perAccount thresholds drive
// SweepHookSilence; tests populate fake.windows to control which accounts the
// sweep considers "running" and seed h.lastSeenEventAt under stateMu.
func newHookSilenceTestHandler(global time.Duration, perAccount map[string]time.Duration) (*Handler, *fakeKittyClient, *callRecorder) {
	guard := NewStateGuard(time.Hour)
	notifier, rec := newTestNotifier(&config.NotificationConfig{})
	h := NewHandler(guard, notifier)
	fake := &fakeKittyClient{}
	h.SetKittyClient(fake)
	h.SetHookSilenceThresholds(global, perAccount)
	return h, fake, rec
}

// fakeWindowsForAccount builds a kitten-@-ls-shaped windows list with a
// single tab whose only window carries the given cc_account user var.
// Mirrors fakeOSWindowsWithTab but populates UserVars.Account so the
// hook-silence sweep can enumerate the account.
func fakeWindowsForAccount(account string) []kitty.OSWindow {
	return []kitty.OSWindow{
		{
			ID: 1,
			Tabs: []kitty.Tab{
				{
					ID: 100,
					Windows: []kitty.Window{
						{ID: 42, UserVars: kitty.UserVars{Account: account}},
					},
				},
			},
		},
	}
}
