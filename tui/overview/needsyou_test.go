package overview

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
)

func TestNeedsYouJumpsAcrossPagesInBothModes(t *testing.T) {
	for _, compact := range []bool{false, true} {
		sessions := viewSessions(37)
		sessions[1].State = "key"
		sessions[10].State = "question"
		sessions[20].State = "error"
		sessions[35].State, sessions[35].Unseen = "stop", true
		// These must not become attention targets, even if their preview labels
		// describe an independent completed agent turn under an active ASL run.
		sessions[2].State = "warning"
		sessions[3].State = "stop"
		sessions[4].State = ""
		sessions[5].AgentTurn = &protocol.AgentTurnSnapshot{IconState: "stop"}
		sessions[5].Unseen, sessions[5].Looping = true, true
		m, r := atSize(t, sessions, 0, 160, 45)
		if compact {
			m = only(t, m, key(t, "c"))
		}
		for _, jump := range []struct {
			key   string
			index int
		}{
			{"n", 1}, {"n", 10}, {"n", 20}, {"n", 35}, {"n", 1},
			{"N", 35}, {"N", 20}, {"N", 10}, {"N", 1}, {"N", 35},
		} {
			m = only(t, m, key(t, jump.key))
			if m.selected != jump.index || m.selectedTabID != sessions[jump.index].TabID || m.selectedWindowID != sessions[jump.index].WindowID {
				t.Fatalf("compact=%v key=%s: selected=%d, want %d", compact, jump.key, m.selected, jump.index)
			}
			if !strings.Contains(frameOf(m), sessions[jump.index].Project) {
				t.Fatal("attention jump did not render the target's page")
			}
			if !compact {
				if _, ok := m.captures.targets[sessions[jump.index].WindowID]; !ok {
					t.Fatal("jump did not retarget the visible previews")
				}
			} else if len(m.captures.targets) != 0 {
				t.Fatal("compact navigation started preview reads")
			}
		}
		if len(r.focus) != 0 || !reflect.DeepEqual(m.sessions, sessions) || !m.sessions[35].Unseen {
			t.Fatal("attention navigation focused a pane or changed session/unread state")
		}
	}
}

func TestNeedsYouNoTargetAndOnlyCurrentTargetAreInert(t *testing.T) {
	for _, count := range []int{0, 1, 5} {
		for _, currentNeedsYou := range []bool{false, true} {
			m, _ := newTestModel(0)
			m.sessions = viewSessions(count)
			if count > 0 {
				m.selectIndex(count - 1)
				if currentNeedsYou {
					m.sessions[m.selected].State = "key"
				}
			}
			before := m.selected
			for _, binding := range []string{"n", "N"} {
				next, cmd := send(t, m, key(t, binding))
				if next.selected != before || cmd != nil {
					t.Fatalf("count=%d currentNeedsYou=%v: %s moved or issued work", count, currentNeedsYou, binding)
				}
			}
		}
	}
}
