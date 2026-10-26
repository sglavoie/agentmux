package dashboard

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/config"
	"github.com/sglavoie/agentmux/internal/session"
)

// pinnedBandRows is one waiting and one working pinned session among a
// waiting, a working, and a seen-stop unpinned one, listed in the session
// order (pinned first) that SortRows receives.
func pinnedBandRows() []session.RunningSession {
	row := func(id int, state string, starred bool) session.RunningSession {
		return session.RunningSession{
			TabID: id, WindowID: id, Base: "row" + string(rune('a'+id)), Account: "work",
			State: state, Starred: starred,
		}
	}
	return []session.RunningSession{
		row(1, "bolt", true),
		row(2, "key", true),
		row(3, "stop", false),
		row(4, "bolt", false),
		row(5, "question", false),
	}
}

// TestPinnedSessionsLeadAboveNeedsYou: pinned sessions are listed first,
// under their own heading, whatever their urgency, and keep the session order
// among themselves; Needs you follows them. A pinned session that waits stays
// under Pinned, as it does in the workspace sidebar.
func TestPinnedSessionsLeadAboveNeedsYou(t *testing.T) {
	rows := pinnedBandRows()
	if got, want := sortedTabIDs(rows, nil), []int{1, 2, 5, 4, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want the pins in their order, then Needs you, Running, Idle: %v", got, want)
	}
	in := ListInput{Cfg: &config.Config{}, Width: 60, Now: time.Now(), Items: BuildItems(rows)}
	var headings []string
	for _, line := range strings.Split(stripAnsi(RenderList(in)), "\n") {
		trimmed := strings.TrimSpace(line)
		for _, label := range groupLabels {
			if strings.HasPrefix(trimmed, label+" (") {
				headings = append(headings, trimmed)
			}
		}
	}
	if want := []string{"Pinned (2)", "Needs you (1)", "Running (1)", "Idle (1)"}; !reflect.DeepEqual(headings, want) {
		t.Fatalf("headings = %q, want %q", headings, want)
	}
}

// TestAPinIsABandNotABucket: everything that asks how urgent a session is
// still answers by its state, so the waiting pin is in the Needs you queue and
// wears its border, and is counted once, under Pinned.
func TestAPinIsABandNotABucket(t *testing.T) {
	rows := pinnedBandRows()
	SortRows(rows, nil, sortTestIdle)
	list := BuildItems(rows)
	waiting := 1 // the pinned key row, second after sorting
	if !rows[waiting].Starred || rows[waiting].State != "key" {
		t.Fatalf("row %d = %+v, want the waiting pin", waiting, rows[waiting])
	}
	if got := list.GroupAt(waiting, nil, sortTestIdle); got != attentionGroup {
		t.Errorf("GroupAt(waiting pin) = %d, want Needs you", got)
	}
	if got := list.BandAt(waiting, nil, sortTestIdle); got != pinnedBand {
		t.Errorf("BandAt(waiting pin) = %d, want Pinned", got)
	}
	if !NeedsYou(rows[waiting], nil, sortTestIdle) {
		t.Error("a waiting pin left the Needs you queue")
	}
	target, ok := FindNextNeedsYou(list, -1, 1, nil, sortTestIdle)
	if !ok || target.Row != waiting {
		t.Errorf("n from the top = %+v (%v), want the waiting pin first", target, ok)
	}
	if got := list.countInGroup(attentionGroup, nil, sortTestIdle); got != 1 {
		t.Errorf("Needs you counts %d, want only the unpinned waiting row", got)
	}
	if got := list.countInGroup(pinnedBand, nil, sortTestIdle); got != 2 {
		t.Errorf("Pinned counts %d, want 2", got)
	}
	// The windowed walk charges the headings the renderer draws: five rows,
	// four headings, and a blank line before each heading after the first.
	scroll := ScrollInput{Items: list, IdleThreshold: sortTestIdle, GroupHeadings: true}
	if got := scroll.LineCost(0, list.Len()-1); got != 5+4+3 {
		t.Errorf("LineCost = %d, want 12", got)
	}
}
