package rendering

import (
	"testing"

	"github.com/sglavoie/agentmux/internal/daemon/state"
	"github.com/sglavoie/agentmux/internal/kitty"
	"github.com/sglavoie/agentmux/internal/palette"
)

func TestDesiredTabTitlePreservesForeignSegments(t *testing.T) {
	live := palette.BuildTitleParts(palette.TitleParts{
		Base:    "📟W:renamed",
		State:   "stop",
		Starred: true,
		Locked:  true,
		Muted:   true,
	})
	want := palette.BuildTitleParts(palette.TitleParts{
		Base:    "📟W:renamed",
		State:   "bolt",
		Starred: true,
		Locked:  true,
		Muted:   true,
		Looping: true,
	})
	tab := &kitty.Tab{Title: live}
	st := &state.WindowState{DesiredIcon: "bolt", Looping: true, LoopingSet: true}

	if got := DesiredTabTitle(tab, st); got != want {
		t.Fatalf("DesiredTabTitle = %q, want %q", got, want)
	}
}

func TestDesiredTabTitleReflectsOwnerLock(t *testing.T) {
	for _, locked := range []bool{false, true} {
		tab := &kitty.Tab{
			Title:   palette.BuildTitleParts(palette.TitleParts{Base: "api", State: "stop", Locked: !locked}),
			Windows: []kitty.Window{{ID: 7, UserVars: kitty.UserVars{Profile: "work", Harness: "codex", Locked: locked}}},
		}
		want := "⚡api"
		if locked {
			want = "🔒⚡api"
		}
		if got := DesiredTabTitle(tab, &state.WindowState{DesiredIcon: "bolt"}); got != want {
			t.Fatalf("locked=%v: DesiredTabTitle = %q, want %q", locked, got, want)
		}
	}
}

func TestDesiredTabTitleLeavesLoopMarkerToItsWriterUntilKnown(t *testing.T) {
	// A window whose loop state has never been decided must not clear a loop
	// marker some other writer (title restore, sibling routing) put there.
	live := palette.BuildTitleParts(palette.TitleParts{Base: "W:api", State: "stop", Looping: true})
	tab := &kitty.Tab{Title: live}
	st := &state.WindowState{DesiredIcon: "bolt"}

	want := palette.BuildTitleParts(palette.TitleParts{Base: "W:api", State: "bolt", Looping: true})
	if got := DesiredTabTitle(tab, st); got != want {
		t.Fatalf("DesiredTabTitle = %q, want %q", got, want)
	}
}

func TestDesiredTabTitleClearsLoopMarkerOnceLoopStateIsKnown(t *testing.T) {
	live := palette.BuildTitleParts(palette.TitleParts{Base: "W:api", State: "stop", Looping: true})
	tab := &kitty.Tab{Title: live}
	st := &state.WindowState{DesiredIcon: "bolt", LoopingSet: true}

	want := palette.BuildTitleParts(palette.TitleParts{Base: "W:api", State: "bolt"})
	if got := DesiredTabTitle(tab, st); got != want {
		t.Fatalf("DesiredTabTitle = %q, want %q", got, want)
	}
}

func managedIDs(ids ...int) func(int) bool {
	set := make(map[int]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return func(id int) bool { return set[id] }
}

func TestTitleOwnerPrefersKittysOwner(t *testing.T) {
	tab := &kitty.Tab{Windows: []kitty.Window{
		{ID: 41, UserVars: kitty.UserVars{Profile: "work", Harness: "codex"}},
		{ID: 42, IsActive: true, UserVars: kitty.UserVars{Profile: "work", Harness: "codex"}},
	}}
	// Both windows are launch-managed, so Kitty's own owner resolution applies
	// and the daemon's managed set is never consulted.
	owner, ok := TitleOwner(tab, managedIDs())
	if !ok || owner.ID != 42 {
		t.Fatalf("TitleOwner = %+v ok=%v, want the active managed window 42", owner, ok)
	}
}

func TestTitleOwnerFallsBackToTheActiveTrackedWindow(t *testing.T) {
	// No window carries launch user vars, so Kitty reports no owner; the
	// daemon's own tracked set decides.
	tab := &kitty.Tab{Windows: []kitty.Window{
		{ID: 41},
		{ID: 42, IsActive: true},
		{ID: 43},
	}}
	owner, ok := TitleOwner(tab, managedIDs(41, 42))
	if !ok || owner.ID != 42 {
		t.Fatalf("TitleOwner = %+v ok=%v, want 42", owner, ok)
	}
}

func TestTitleOwnerFallsBackToTheFirstTrackedWindow(t *testing.T) {
	tab := &kitty.Tab{Windows: []kitty.Window{
		{ID: 41},
		{ID: 42},
		{ID: 43, IsActive: true},
	}}
	owner, ok := TitleOwner(tab, managedIDs(41, 42))
	if !ok || owner.ID != 41 {
		t.Fatalf("TitleOwner = %+v ok=%v, want 41", owner, ok)
	}
}

func TestTitleOwnerReportsNoOwnerForAnUntrackedTab(t *testing.T) {
	tab := &kitty.Tab{Windows: []kitty.Window{{ID: 41}, {ID: 42}}}
	if owner, ok := TitleOwner(tab, managedIDs()); ok {
		t.Fatalf("TitleOwner = %+v ok=%v, want no owner", owner, ok)
	}
	if owner, ok := TitleOwner(nil, managedIDs(41)); ok {
		t.Fatalf("TitleOwner(nil) = %+v ok=%v, want no owner", owner, ok)
	}
	if owner, ok := TitleOwner(tab, nil); ok {
		t.Fatalf("TitleOwner(nil predicate) = %+v ok=%v, want no owner", owner, ok)
	}
}
