package actions

import (
	"context"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestToggleLockUpdatesProtectionAndTab(t *testing.T) {
	for _, locked := range []bool{false, true} {
		t.Run(map[bool]string{false: "lock", true: "unlock"}[locked], func(t *testing.T) {
			runner := kitty.NewMockRunner(nil)
			client := kitty.NewClientWithSocket(runner, "")
			target := session.RunningSession{
				WindowID: 7, Base: "🦊api", State: "bolt", Starred: true,
				Muted: true, Looping: true, Locked: locked,
			}
			result, err := DefaultRegistry().Execute(context.Background(), Dependencies{Kitty: client}, ToggleLock, Request{Session: &target})
			if err != nil {
				t.Fatal(err)
			}
			value, title, message := "true", "★🔒🔇⚡🔁🦊api", "locked session"
			if locked {
				value, title, message = "", "★🔇⚡🔁🦊api", "unlocked session"
			}
			if !result.Changed || result.Message != message {
				t.Fatalf("result = %+v", result)
			}
			want := [][]string{
				{"@", "set-user-vars", "--match=id:7", "amx_locked=" + value},
				{"@", "set-tab-title", "--match=window_id:7", "--", title},
			}
			if len(runner.Calls) != len(want) {
				t.Fatalf("calls = %+v", runner.Calls)
			}
			for i := range want {
				if !reflect.DeepEqual(runner.Calls[i].Args, want[i]) {
					t.Errorf("call %d = %v, want %v", i, runner.Calls[i].Args, want[i])
				}
			}
		})
	}
}
