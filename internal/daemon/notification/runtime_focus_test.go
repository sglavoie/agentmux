package notification

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestNotifierClickShowsRuntimeSession(t *testing.T) {
	const id = "rs-0123456789abcdef0123456789abcdef"
	for name, tc := range map[string]struct {
		wired bool
		err   error
		want  string
	}{
		"shown":     {true, nil, "focus:focused:focus_success"},
		"stopped":   {true, ErrFocusTargetStale, "focus:suppressed:focus_target_stale"},
		"no viewer": {true, ErrFocusNoViewer, "focus:suppressed:focus_no_viewer"},
		"failed":    {true, errors.New("tmux broke"), "focus:failed:focus_failed"},
		"not wired": {false, nil, "focus:suppressed:focus_unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				n, calls := newTestNotifier(&config.NotificationConfig{Stop: true})
				log := &dispositionLog{}
				n.SetObserver(log.observe)
				var shown []string
				if tc.wired {
					n.SetSessionFocus(func(runtimeSessionID string) error {
						shown = append(shown, runtimeSessionID)
						return tc.err
					})
				}
				calls.setClickResult(true)

				n.MaybeNotifyTarget(NotificationTarget{WindowID: 1<<40 + 3, Account: "work", Navigable: true, RuntimeSessionID: id, Backend: BackendKindTmux}, "W:api", "stop", false)
				synctest.Wait()

				if calls.len() != 1 {
					t.Fatalf("dispatches = %d; one notice should be dispatched", calls.len())
				}
				if tc.wired && !slices.Equal(shown, []string{id}) {
					t.Fatalf("shown = %v; want %s once", shown, id)
				}
				if !log.has(tc.want) {
					t.Fatalf("dispositions = %v; want %s", log.entries, tc.want)
				}
			})
		})
	}
}
