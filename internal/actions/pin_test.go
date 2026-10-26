package actions

import (
	"context"
	"reflect"
	"testing"

	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/session"
)

func TestTogglePinWritesOnlyTheTitleStar(t *testing.T) {
	t.Parallel()

	for _, starred := range []bool{false, true} {
		t.Run(map[bool]string{false: "pin", true: "unpin"}[starred], func(t *testing.T) {
			runner := kitty.NewMockRunner(nil)
			client := kitty.NewClientWithSocket(runner, "")
			target := session.RunningSession{
				WindowID: 7, Base: "🦊api", State: "bolt", Starred: starred,
				Muted: true, Looping: true, Locked: true,
			}
			result, err := DefaultRegistry().Execute(context.Background(), Dependencies{Kitty: client}, TogglePin, Request{Session: &target})
			if err != nil {
				t.Fatal(err)
			}
			title, message := "★🔒🔇⚡🔁🦊api", "pinned session"
			if starred {
				title, message = "🔒🔇⚡🔁🦊api", "unpinned session"
			}
			if !result.Changed || result.Message != message {
				t.Fatalf("result = %+v", result)
			}
			want := [][]string{{"@", "set-tab-title", "--match=window_id:7", "--", title}}
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
