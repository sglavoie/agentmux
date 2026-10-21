package daemon

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/daemon/activity"
	"github.com/sglavoie/agentmux/internal/daemon/notification"
	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestHandler_PreserveCurrentIcon(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", PreservesIcon: true})
	if !resp.OK || !resp.Allowed || resp.IconState != protocol.IconStatePreserve {
		t.Errorf("PreservesIcon: want icon_state=%q, got %+v", protocol.IconStatePreserve, resp)
	}
}

func TestHandler_PreserveCurrentRestoresPreviousState(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		prevAction string
		wantIdle   string
	}{
		{"active remains clearable", "active", ""},
		{"subagent remains clearable", "subagent", ""},
		{"permission remains sticky", "permission", protocol.IconStatePreserve},
		{"failure remains sticky", "failure", protocol.IconStatePreserve},
		{"elicitation remains sticky", "elicitation", protocol.IconStatePreserve},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The guard window passes on the bubble's clock, not the wall's.
			synctest.Test(t, func(t *testing.T) {
				guard := activity.NewStateGuard(50 * time.Millisecond)
				notifier := notification.NewNotifier(&config.NotificationConfig{})
				notifier.SetBackend(func(_ context.Context, _ int, _, _, _ string) (bool, error) {
					return false, nil
				})
				h := NewHandler(guard, notifier)

				resp := h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: tc.prevAction})
				if !resp.OK || !resp.Allowed {
					t.Fatalf("seed %s: want allowed=true, got %+v", tc.prevAction, resp)
				}
				time.Sleep(60 * time.Millisecond)

				resp = h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", Event: "TaskCompleted", PreservesIcon: true})
				if !resp.OK || !resp.Allowed || resp.IconState != protocol.IconStatePreserve {
					t.Fatalf("preserve-current after %s: want icon_state=%q, got %+v", tc.prevAction, protocol.IconStatePreserve, resp)
				}

				resp = h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "idle"})
				if !resp.OK || !resp.Allowed || resp.IconState != tc.wantIdle {
					t.Fatalf("idle after preserve-current and %s: want icon_state=%q, got %+v", tc.prevAction, tc.wantIdle, resp)
				}
			})
		})
	}
}

func TestHandler_ChildCompleteEventsDoNotNotifyStop(t *testing.T) {
	cases := []struct {
		name  string
		seed  func(*Handler)
		event string
	}{
		{
			name: "TeammateIdle preserves parent activity",
			seed: func(h *Handler) {
				h.Handle(protocol.Request{Type: protocol.TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
			},
			event: "TeammateIdle",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				guard := activity.NewStateGuard(time.Hour)
				cfg := &config.NotificationConfig{Stop: true}
				notifier := notification.NewNotifier(cfg)
				var mu sync.Mutex
				var calls []string
				notifier.SetBackend(func(_ context.Context, _ int, _, _, subtitle string) (bool, error) {
					mu.Lock()
					calls = append(calls, subtitle)
					mu.Unlock()
					return false, nil
				})
				h := NewHandler(guard, notifier)

				tc.seed(h)

				resp := h.Handle(protocol.Request{
					Type:          protocol.TypeStateUpdate,
					WindowID:      1,
					Action:        "active",
					Event:         tc.event,
					PreservesIcon: true,
					SessionTitle:  "W:api",
				})
				if !resp.OK || !resp.Allowed || resp.IconState != protocol.IconStatePreserve {
					t.Fatalf("%s: want icon_state=%q, got %+v", tc.name, protocol.IconStatePreserve, resp)
				}

				synctest.Wait()

				mu.Lock()
				defer mu.Unlock()
				if len(calls) != 0 {
					t.Fatalf("%s: unexpected notifications %v", tc.name, calls)
				}
			})
		})
	}
}
