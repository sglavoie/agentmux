package daemon

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
)

func TestHandler_PreserveCurrentIcon(t *testing.T) {
	h := newTestHandler()
	resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", PreservesIcon: true})
	if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
		t.Errorf("PreservesIcon: want icon_state=%q, got %+v", IconStatePreserve, resp)
	}
}

func TestHandler_PreserveCurrentRestoresPreviousState(t *testing.T) {
	cases := []struct {
		name       string
		prevAction string
		wantIdle   string
	}{
		{"active remains clearable", "active", ""},
		{"subagent remains clearable", "subagent", ""},
		{"permission remains sticky", "permission", IconStatePreserve},
		{"failure remains sticky", "failure", IconStatePreserve},
		{"elicitation remains sticky", "elicitation", IconStatePreserve},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			guard := NewStateGuard(50 * time.Millisecond)
			notifier := NewNotifier(&config.NotificationConfig{})
			notifier.dispatch = func(_ context.Context, _ int, _, _, _ string) (bool, error) { return false, nil }
			h := NewHandler(guard, notifier)

			resp := h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: tc.prevAction})
			if !resp.OK || !resp.Allowed {
				t.Fatalf("seed %s: want allowed=true, got %+v", tc.prevAction, resp)
			}
			time.Sleep(60 * time.Millisecond)

			resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "TaskCompleted", PreservesIcon: true})
			if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
				t.Fatalf("preserve-current after %s: want icon_state=%q, got %+v", tc.prevAction, IconStatePreserve, resp)
			}

			resp = h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "idle"})
			if !resp.OK || !resp.Allowed || resp.IconState != tc.wantIdle {
				t.Fatalf("idle after preserve-current and %s: want icon_state=%q, got %+v", tc.prevAction, tc.wantIdle, resp)
			}
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
				h.Handle(Request{Type: TypeStateUpdate, WindowID: 1, Action: "active", Event: "PreToolUse"})
			},
			event: "TeammateIdle",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				guard := NewStateGuard(time.Hour)
				cfg := &config.NotificationConfig{Stop: true}
				notifier := NewNotifier(cfg)
				var mu sync.Mutex
				var calls []string
				notifier.dispatch = func(_ context.Context, _ int, _, _, subtitle string) (bool, error) {
					mu.Lock()
					calls = append(calls, subtitle)
					mu.Unlock()
					return false, nil
				}
				h := NewHandler(guard, notifier)

				tc.seed(h)

				resp := h.Handle(Request{
					Type:          TypeStateUpdate,
					WindowID:      1,
					Action:        "active",
					Event:         tc.event,
					PreservesIcon: true,
					SessionTitle:  "W:api",
				})
				if !resp.OK || !resp.Allowed || resp.IconState != IconStatePreserve {
					t.Fatalf("%s: want icon_state=%q, got %+v", tc.name, IconStatePreserve, resp)
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
