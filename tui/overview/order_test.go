package overview

import (
	"reflect"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/daemon/protocol"
	"github.com/sglavoie/agentmux/internal/session"
)

// row is the minimum a session needs to be ordered: a tab to sit at, a window to
// be keyed by, and whatever identity the case is about.
func row(tabID, windowID int, project string) session.RunningSession {
	return session.RunningSession{
		TabID:    tabID,
		WindowID: windowID,
		Key:      project,
		Base:     project,
		Project:  project,
	}
}

// names reads the ordered result back as the labels a reader would see, which is
// what every expectation below is written in.
func names(rows []session.RunningSession) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Key
	}
	return out
}

func TestOrderGroupsByProjectAndSortsCaseInsensitively(t *testing.T) {
	in := []session.RunningSession{
		row(5, 50, "zeta"),
		row(3, 30, "API"),
		row(1, 10, "beta"),
		row(2, 20, "api"),
		row(4, 40, "Beta"),
	}
	got := names(Order(in))
	want := []string{"API", "api", "Beta", "beta", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// TestOrderPutsARepositoryFamilyTogether. Two checkouts of one repository are
// two configured projects with two directories and two labels; the family key is
// what makes them one group, and the group's tiles must be contiguous.
func TestOrderPutsARepositoryFamilyTogether(t *testing.T) {
	family := func(tabID int, key, label string) session.RunningSession {
		s := row(tabID, tabID*10, key)
		s.FamilyKey = "repo:/src/api"
		s.FamilyLabel = label
		s.FamilyIsRepository = true
		return s
	}
	in := []session.RunningSession{
		family(4, "api-wip", "api"),
		row(2, 20, "docs"),
		family(1, "api-main", "api"),
		row(3, 30, "alpha"),
	}
	got := names(Order(in))
	want := []string{"alpha", "api-main", "api-wip", "docs"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// TestTwoGroupsSharingALabelStayApart. A family and an unrelated session can
// both be called "api". They are different groups, so their tiles must not
// interleave — which is what the identity tie-breaker behind the label is for.
func TestTwoGroupsSharingALabelStayApart(t *testing.T) {
	familyRow := func(tabID int, key string) session.RunningSession {
		s := row(tabID, tabID*10, key)
		s.FamilyKey = "repo:/src/api"
		s.FamilyLabel = "api"
		return s
	}
	loose := func(tabID int, key string) session.RunningSession {
		s := row(tabID, tabID*10, key)
		s.Project = "api"
		return s
	}
	in := []session.RunningSession{
		familyRow(4, "fam-b"),
		loose(1, "loose-a"),
		familyRow(2, "fam-a"),
		loose(3, "loose-b"),
	}
	got := names(Order(in))
	// The two family rows are adjacent and the two loose rows are adjacent; which
	// group leads is fixed by the identity prefix and asserted exactly so the
	// order cannot silently become discovery order.
	want := []string{"fam-a", "fam-b", "loose-a", "loose-b"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// TestWithinAGroupTheTabIDDecides. Tab IDs are stable for the life of the tab,
// so they are what keeps a project's tiles from shuffling between refreshes.
func TestWithinAGroupTheTabIDDecides(t *testing.T) {
	in := []session.RunningSession{
		row(9, 90, "api"), row(2, 20, "api"), row(7, 70, "api"), row(1, 10, "api"),
	}
	got := Order(in)
	want := []int{1, 2, 7, 9}
	for i, tab := range want {
		if got[i].TabID != tab {
			t.Fatalf("position %d holds tab %d, want %d", i, got[i].TabID, tab)
		}
	}
}

// TestRowsSharingATabStillHaveAFixedOrder. Two panes in one tab is unusual but
// legal, and "undefined" is not an acceptable answer for where a tile goes.
func TestRowsSharingATabStillHaveAFixedOrder(t *testing.T) {
	a := row(3, 31, "api")
	a.Key = "pane-a"
	b := row(3, 30, "api")
	b.Key = "pane-b"
	got := names(Order([]session.RunningSession{a, b}))
	want := []string{"pane-b", "pane-a"} // window 30 before window 31
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
	// And the reverse input gives the same answer, which is what makes it an order
	// rather than a preserved arrival sequence.
	if got := names(Order([]session.RunningSession{b, a})); !reflect.DeepEqual(got, want) {
		t.Errorf("reversed input ordered as %v, want %v", got, want)
	}
}

// TestStatusNeverReordersATile is the rule the whole file exists for: a tile may
// change what it says, never where it is. Every state the dashboard knows is
// applied to the same set and the resulting order compared against the quiet one.
func TestStatusNeverReordersATile(t *testing.T) {
	base := []session.RunningSession{
		row(1, 10, "api"), row(2, 20, "api"), row(3, 30, "docs"), row(4, 40, "zeta"),
	}
	quiet := names(Order(base))

	states := []string{"", "bolt", "stop", "key", "warning", "error", "subagent", "compact", "question"}
	for _, state := range states {
		for i := range base {
			noisy := make([]session.RunningSession, len(base))
			copy(noisy, base)
			noisy[i].State = state
			noisy[i].Unseen = true
			if got := names(Order(noisy)); !reflect.DeepEqual(got, quiet) {
				t.Fatalf("with %q on %s the order became %v, want %v", state, base[i].Key, got, quiet)
			}
		}
	}

	// And with every row in a different state at once.
	noisy := make([]session.RunningSession, len(base))
	copy(noisy, base)
	for i := range noisy {
		noisy[i].State = states[i%len(states)]
		noisy[i].Unseen = i%2 == 0
	}
	if got := names(Order(noisy)); !reflect.DeepEqual(got, quiet) {
		t.Errorf("a mixed-state set ordered as %v, want %v", got, quiet)
	}
}

func TestOpeningOrderRanksAttentionThenDoneThenNonWorkingThenWorking(t *testing.T) {
	makeRow := func(tab int, name, state string, unseen bool) session.RunningSession {
		s := row(tab, tab*10, name)
		s.State, s.Unseen = state, unseen
		return s
	}
	input := []session.RunningSession{
		makeRow(10, "a-compacting", "compact", false),
		makeRow(1, "a-working", "bolt", false),
		makeRow(2, "b-done", "stop", false),
		makeRow(3, "c-question", "question", false),
		makeRow(4, "d-subagent", "subagent", false),
		makeRow(5, "e-unread", "stop", true),
		makeRow(6, "f-tool-failed", "warning", false),
		makeRow(7, "g-error", "error", false),
		makeRow(8, "h-working", "bolt", false),
	}
	// The aggregate state can say a run is active while the agent has
	// finished. The tile uses the agent turn's status, so ordering does too.
	agentDone := makeRow(9, "i-agent-done", "bolt", false)
	agentDone.AgentTurn = &protocol.AgentTurnSnapshot{IconState: "stop"}
	input = append(input, agentDone)
	before := append([]session.RunningSession(nil), input...)
	got := names(OpeningOrder(input, nil, 5*time.Minute))
	want := []string{"c-question", "e-unread", "g-error", "b-done", "i-agent-done", "a-compacting", "d-subagent", "f-tool-failed", "a-working", "h-working"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("opening order = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(input, before) {
		t.Error("OpeningOrder modified the caller's snapshot")
	}
}

func TestRetainOrderMovesATileToItsTierAndPreservesTierOrder(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  string
		unseen bool
		want   []string
	}{
		{name: "permission", state: "key", want: []string{"z-existing", "x-changing", "a-new", "w-idle", "y-running"}},
		{name: "question", state: "question", want: []string{"z-existing", "x-changing", "a-new", "w-idle", "y-running"}},
		{name: "error", state: "error", want: []string{"z-existing", "x-changing", "a-new", "w-idle", "y-running"}},
		{name: "unseen completion", state: "stop", unseen: true, want: []string{"z-existing", "x-changing", "a-new", "w-idle", "y-running"}},
		{name: "seen completion", state: "stop", want: []string{"z-existing", "a-new", "x-changing", "w-idle", "y-running"}},
		{name: "working", state: "bolt", want: []string{"z-existing", "a-new", "w-idle", "y-running", "x-changing"}},
		{name: "warning", state: "warning", want: []string{"z-existing", "a-new", "w-idle", "x-changing", "y-running"}},
		{name: "compacting", state: "compact", want: []string{"z-existing", "a-new", "w-idle", "x-changing", "y-running"}},
		{name: "subagent", state: "subagent", want: []string{"z-existing", "a-new", "w-idle", "x-changing", "y-running"}},
		{name: "iconless", want: []string{"z-existing", "a-new", "w-idle", "x-changing", "y-running"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := []session.RunningSession{row(1, 10, "z-existing"), row(6, 60, "w-idle"), row(2, 20, "y-running"), row(3, 30, "x-changing"), row(4, 40, "closed")}
			previous[0].State = "question"
			previous[2].State, previous[3].State = "bolt", "bolt"
			current := []session.RunningSession{row(5, 50, "a-new"), previous[3], previous[2], previous[1], previous[0]}
			current[0].State = "key"
			current[1].State, current[1].Unseen = tc.state, tc.unseen
			current[4].State = "error" // changing severity must not reorder attention cards
			beforePrevious := append([]session.RunningSession(nil), previous...)
			beforeCurrent := append([]session.RunningSession(nil), current...)
			got := RetainOrder(previous, current, nil, 5*time.Minute)
			if !reflect.DeepEqual(names(got), tc.want) {
				t.Fatalf("refresh order = %v, want %v", names(got), tc.want)
			}
			if !reflect.DeepEqual(previous, beforePrevious) || !reflect.DeepEqual(current, beforeCurrent) {
				t.Fatal("RetainOrder modified an input snapshot")
			}
			if repeated := RetainOrder(got, current, nil, 5*time.Minute); !reflect.DeepEqual(repeated, got) {
				t.Fatal("unchanged refresh shuffled cards")
			}
		})
	}
}

// TestOrderLeavesTheCallerSliceAlone. The caller holds the live set; sorting it
// underneath them would reorder the model's own list as a side effect of
// rendering.
func TestOrderLeavesTheCallerSliceAlone(t *testing.T) {
	in := []session.RunningSession{row(3, 30, "zeta"), row(1, 10, "api"), row(2, 20, "docs")}
	before := names(in)
	out := Order(in)
	if after := names(in); !reflect.DeepEqual(after, before) {
		t.Errorf("the input became %v, want it left as %v", after, before)
	}
	if len(out) != len(in) {
		t.Fatalf("Order returned %d rows for %d inputs", len(out), len(in))
	}
	out[0].Key = "mutated"
	if in[0].Key == "mutated" {
		t.Error("the result shares backing storage with the input")
	}
}

func TestOrderHandlesTheEmptyAndSingleCases(t *testing.T) {
	if got := Order(nil); len(got) != 0 {
		t.Errorf("Order(nil) returned %d rows", len(got))
	}
	if got := Order([]session.RunningSession{}); len(got) != 0 {
		t.Errorf("Order(empty) returned %d rows", len(got))
	}
	one := []session.RunningSession{row(1, 10, "api")}
	if got := names(Order(one)); !reflect.DeepEqual(got, []string{"api"}) {
		t.Errorf("Order(one) = %v", got)
	}
}

// TestAnUnlabelledRowIsOrderedByTheNameItsTileShows. A session with no
// configured project and no family still has a Kitty base name, and ordering it
// by an empty string would pile every such row at the top in discovery order.
func TestAnUnlabelledRowIsOrderedByTheNameItsTileShows(t *testing.T) {
	bare := func(tabID int, base string) session.RunningSession {
		return session.RunningSession{TabID: tabID, WindowID: tabID * 10, Key: base, Base: base}
	}
	in := []session.RunningSession{bare(3, "zsh"), bare(1, "nvim"), bare(2, "api")}
	got := names(Order(in))
	want := []string{"api", "nvim", "zsh"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// TestOrderIsStableAcrossRepeatedCalls. The refresh runs on a timer, so an order
// that depended on input arrangement would show up as tiles that shuffle while
// nothing is happening.
func TestOrderIsStableAcrossRepeatedCalls(t *testing.T) {
	in := []session.RunningSession{
		row(4, 40, "Zeta"), row(1, 10, "api"), row(3, 30, "API"), row(2, 20, "docs"),
	}
	want := names(Order(in))
	for i := 0; i < 5; i++ {
		// Rotate the input; the answer must not move.
		in = append(in[1:], in[0])
		if got := names(Order(in)); !reflect.DeepEqual(got, want) {
			t.Fatalf("rotation %d ordered as %v, want %v", i, got, want)
		}
	}
}
