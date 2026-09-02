package notification

import (
	"context"
	"sync"

	"github.com/sglavoie/agentmux/internal/config"
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
// it. Every test in this package uses this helper to avoid hand-rolling
// per-test mutex bookkeeping.
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

func newTestNotifier(cfg *config.NotificationConfig) (*Notifier, *callRecorder) {
	n := NewNotifier(cfg)
	rec := &callRecorder{}
	n.dispatch = func(_ context.Context, windowID int, account, title, subtitle string) (dispatchResult, error) {
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
			return deliveredResult(interactionClicked), nil
		}
		return deliveredResult(interactionUnavailable), nil
	}
	return n, rec
}
