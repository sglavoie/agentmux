package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/kitty"
)

type notifyCall struct {
	windowID int
	account  string
	title    string
	subtitle string
	clicked  bool
}

// callRecorder is a thread-safe collector for dispatcher invocations. The
// notify dispatch runs in its own goroutine, so every read from the test
// must hold the same mutex as the write — otherwise the race detector flags
// it. Handler-level tests in this package use this helper to avoid
// hand-rolling per-test mutex bookkeeping.
type callRecorder struct {
	mu          sync.Mutex
	calls       []notifyCall
	clickResult bool
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

// setClickResult controls the interaction returned by the recorder's dispatch
// stub so focus tests can simulate the user clicking the notification body.
func (r *callRecorder) setClickResult(v bool) {
	r.mu.Lock()
	r.clickResult = v
	r.mu.Unlock()
}

func newTestNotifier(cfg *config.NotificationConfig) (*notification.Notifier, *callRecorder) {
	n := notification.NewNotifier(cfg)
	rec := &callRecorder{}
	n.SetBackend(func(_ context.Context, windowID int, account, title, subtitle string) (bool, error) {
		rec.mu.Lock()
		clicked := rec.clickResult
		rec.mu.Unlock()
		rec.add(notifyCall{
			windowID: windowID,
			account:  account,
			title:    title,
			subtitle: subtitle,
			clicked:  clicked,
		})
		if clicked {
			return true, nil
		}
		return false, nil
	})
	return n, rec
}

func newTestHandler() *Handler {
	h, _ := newTestHandlerWithNotificationConfig()
	return h
}

// newTestHandlerWithNotificationConfig is newTestHandler for tests that need to
// toggle a notification setting mid-test. The notifier keeps the returned
// pointer, so a later write to it is observed by the next dispatch decision.
func newTestHandlerWithNotificationConfig() (*Handler, *config.NotificationConfig) {
	guard := activity.NewStateGuard(time.Hour)
	cfg := &config.NotificationConfig{}
	notifier := notification.NewNotifier(cfg)
	notifier.SetBackend(func(_ context.Context, _ int, _, _, _ string) (bool, error) {
		return false, nil
	})
	return NewHandler(guard, notifier), cfg
}

// newSweepTestHandler builds a Handler wired with a notifier that records
// dispatches and a fake Kitty client whose SetTabTitle calls are recorded.
// Threshold zero leaves the sweep disabled — used by the no-op test. The
// same threshold is applied to all three watchdog states so legacy tests
// that pre-date per-state thresholds continue to exercise the sweep
// uniformly; per-state behavior is covered by newSweepTestHandlerWithMap.
func newSweepTestHandler(threshold time.Duration) (*Handler, *fakeKittyClient, *callRecorder) {
	guard := activity.NewStateGuard(time.Hour)
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
	guard := activity.NewStateGuard(time.Hour)
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
	guard := activity.NewStateGuard(time.Hour)
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
