package daemon

// Closed-window GC with two Kitty instances. Window IDs mean something only
// inside the instance that issued them, so a listing of one instance must not
// close a window whose hooks named another; that window is judged by its own
// instance instead.

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/sglavoie/agentmux/internal/hookevent"
	"github.com/sglavoie/agentmux/internal/kitty"
)

// perSocketRunner answers `kitten @ --to SOCKET ls` from a table keyed by
// socket, and counts the listings each socket received.
type perSocketRunner struct {
	mu      sync.Mutex
	answers map[string][]byte
	failing map[string]bool
	lists   map[string]int
	writes  map[string][][]string
}

func (r *perSocketRunner) Run(_ string, args ...string) ([]byte, []byte, error) {
	socket := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--to" {
			socket = args[i+1]
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lists == nil {
		r.lists = map[string]int{}
	}
	command := ""
	for _, arg := range args {
		if arg == "ls" || arg == "set-user-vars" || arg == "set-tab-title" {
			command = arg
			break
		}
	}
	if command != "ls" {
		if r.writes == nil {
			r.writes = make(map[string][][]string)
		}
		r.writes[socket] = append(r.writes[socket], append([]string(nil), args...))
		return nil, nil, nil
	}
	r.lists[socket]++
	if r.failing[socket] {
		return nil, []byte("busy"), errors.New("kitten failed")
	}
	if answer, ok := r.answers[socket]; ok {
		return answer, nil, nil
	}
	return []byte("[]"), nil, nil
}

func (r *perSocketRunner) listCount(socket string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lists[socket]
}

// twoInstanceHandler returns a handler whose window 7 belongs to the instance
// at foreign and window 8 to the one at listed, with both socket files present.
func twoInstanceHandler(t *testing.T, runner *perSocketRunner) (h *Handler, listed, foreign string) {
	t.Helper()
	dir := t.TempDir()
	listed = "unix:" + filepath.Join(dir, "kitty-1")
	foreign = "unix:" + filepath.Join(dir, "kitty-2")
	for _, socket := range []string{listed, foreign} {
		if err := os.WriteFile(socket[len("unix:"):], nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h = newTestHandler()
	h.useKittyClient(kitty.NewClientWithSocket(runner, ""))
	h.stateMu.Lock()
	h.ensureWindowStateLocked(7)
	h.rememberKittySocketLocked(7, foreign)
	h.ensureWindowStateLocked(8)
	h.rememberKittySocketLocked(8, listed)
	h.stateMu.Unlock()
	return h, listed, foreign
}

func storeHas(h *Handler, windowID int) bool {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	_, ok := h.store[windowID]
	return ok
}

func TestListingOfOneInstanceKeepsAnotherInstancesWindows(t *testing.T) {
	t.Parallel()

	h, listed, _ := twoInstanceHandler(t, &perSocketRunner{})

	h.pruneClosedWindowsFor(time.Now(), map[int]bool{}, listed)

	if !storeHas(h, 7) {
		t.Fatal("window 7 of the other instance was closed by a listing that could not see it")
	}
	if storeHas(h, 8) {
		t.Fatal("window 8 of the listed instance survived a listing that no longer reports it")
	}
}

func TestCollidingWindowNumbersPruneOnlyTheClosedInstance(t *testing.T) {
	t.Parallel()

	h, listed, foreign := twoInstanceHandler(t, &perSocketRunner{})
	h.stateMu.Lock()
	// The fixture's foreign window claimed bare 7 first; the listed
	// instance therefore receives the alias for its own window 7.
	alias := h.kittySlotLocked(listed, 7)
	h.ensureWindowStateLocked(alias)
	h.rememberKittySocketLocked(alias, listed)
	h.stateMu.Unlock()
	if alias == 7 {
		t.Fatal("colliding window reused the same reducer slot")
	}
	h.pruneClosedWindowsFor(time.Now(), map[int]bool{7: true}, listed)
	if !storeHas(h, alias) || !storeHas(h, 7) {
		t.Fatalf("listing %s closed a live slot or foreign slot", listed)
	}
	h.pruneClosedWindowsFor(time.Now(), map[int]bool{}, listed)
	if storeHas(h, alias) || !storeHas(h, 7) {
		t.Fatalf("listing %s failed to retire its own slot or closed %s", listed, foreign)
	}
}

func TestCollidingWindowRendererWritesTheOwningKitty(t *testing.T) {
	t.Parallel()

	runner := &perSocketRunner{answers: map[string][]byte{}}
	h, listed, foreign := twoInstanceHandler(t, runner)
	h.useKittyClient(kitty.NewClientWithSocket(runner, ""))
	runner.answers[listed] = mustMarshalWindows(t, fakeOSWindowsWithTab(7, "W:listed"))
	runner.answers[foreign] = mustMarshalWindows(t, fakeOSWindowsWithTab(7, "W:foreign"))
	h.stateMu.Lock()
	alias := h.kittySlotLocked(listed, 7)
	st := h.ensureWindowStateLocked(alias)
	st.DesiredIcon = "stop"
	st.Dirty = true
	st.Seq = 1
	h.rememberKittySocketLocked(alias, listed)
	h.stateMu.Unlock()
	h.renderQueuedWindow(alias, fakeOSWindowsWithTab(7, "W:foreign"), foreign)
	runner.mu.Lock()
	writesToListed := append([][]string(nil), runner.writes[listed]...)
	writesToForeign := append([][]string(nil), runner.writes[foreign]...)
	runner.mu.Unlock()
	if len(writesToListed) == 0 || len(writesToForeign) != 0 {
		t.Fatalf("renderer writes to listed=%d foreign=%d; want only listed", len(writesToListed), len(writesToForeign))
	}
	if got := writesToListed[0]; !slices.Contains(got, "--match=id:7") {
		t.Fatalf("renderer match = %v; want real Kitty window 7", got)
	}
}

func TestForeignKittyRestoreKeepsSameNumberSeparate(t *testing.T) {
	t.Parallel()

	h := newTestHandler()
	const first, second = "unix:/kitty-first", "unix:/kitty-second"
	h.stateMu.Lock()
	firstSlot := h.kittySlotLocked(first, 7)
	h.ensureWindowStateLocked(firstSlot).DesiredIcon = "bolt"
	h.rememberKittySocketLocked(firstSlot, first)
	h.stateMu.Unlock()
	seed := newTestHandler()
	requireAccepted(t, seed.Handle(legacyHook(t, 7, second, "UserPromptSubmit", hookevent.HookPayload{SessionID: "restored"})))
	seed.stateMu.Lock()
	payload, err := seed.store[7].MarshalUserVar()
	seed.stateMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	windows := []kitty.OSWindow{{Tabs: []kitty.Tab{{ID: 2, Title: "W:second", Windows: []kitty.Window{{
		ID: 7, UserVars: kitty.UserVars{Profile: "work", Harness: "claude", AMXState: payload},
	}}}}}}
	h.restoreFromUserVarsFor(windows, time.Now(), second)
	h.stateMu.Lock()
	secondSlot := h.runtimeRoutes.kittySlotOf(second, 7)
	firstIcon := h.store[firstSlot].DesiredIcon
	secondIcon := h.store[secondSlot].DesiredIcon
	h.stateMu.Unlock()
	if secondSlot == firstSlot || firstIcon != "bolt" || secondIcon != "bolt" {
		t.Fatalf("restored colliding slots first=%d/%s second=%d/%s", firstSlot, firstIcon, secondSlot, secondIcon)
	}
}

func TestUnscopedPruneStillJudgesEveryWindow(t *testing.T) {
	t.Parallel()

	h, _, _ := twoInstanceHandler(t, &perSocketRunner{})

	h.pruneClosedWindows(time.Now(), map[int]bool{})

	if storeHas(h, 7) || storeHas(h, 8) {
		t.Fatal("a listing of unknown provenance must keep judging every window")
	}
}

func TestForeignWindowIsJudgedByItsOwnInstance(t *testing.T) {
	t.Parallel()

	runner := &perSocketRunner{answers: map[string][]byte{}}
	h, listed, foreign := twoInstanceHandler(t, runner)

	runner.answers[foreign] = mustMarshalWindows(t, fakeOSWindowsWithTab(7, "W:api"))
	h.pruneForeignWindows(time.Now(), listed)
	if !storeHas(h, 7) {
		t.Fatal("window 7 was closed although its own instance still reports it")
	}
	if runner.listCount(listed) != 0 {
		t.Fatal("the listed instance was listed again; only other instances need a listing")
	}

	runner.answers[foreign] = mustMarshalWindows(t, fakeOSWindowsWithTab(9, "W:other"))
	h.pruneForeignWindows(time.Now(), listed)
	if storeHas(h, 7) {
		t.Fatal("window 7 survived although its own instance no longer reports it")
	}
	if !storeHas(h, 8) {
		t.Fatal("the foreign pass closed a window of the listed instance")
	}
}

func TestForeignWindowSurvivesAFailedListing(t *testing.T) {
	t.Parallel()

	runner := &perSocketRunner{failing: map[string]bool{}}
	h, listed, foreign := twoInstanceHandler(t, runner)
	runner.failing[foreign] = true

	h.pruneForeignWindows(time.Now(), listed)

	if !storeHas(h, 7) {
		t.Fatal("a failed listing of a live instance closed its window")
	}
}

func TestForeignWindowClosesWhenItsInstanceExited(t *testing.T) {
	t.Parallel()

	runner := &perSocketRunner{}
	h, listed, foreign := twoInstanceHandler(t, runner)
	if err := os.Remove(foreign[len("unix:"):]); err != nil {
		t.Fatal(err)
	}

	h.pruneForeignWindows(time.Now(), listed)

	if storeHas(h, 7) {
		t.Fatal("window 7 survived the exit of its Kitty instance")
	}
	if runner.listCount(foreign) != 0 {
		t.Fatal("an instance whose socket file is gone was dialed anyway")
	}
}

func otherTrees(h *Handler) map[string][]kitty.OSWindow {
	h.stateMu.Lock()
	defer h.stateMu.Unlock()
	return h.otherKittyTrees
}

func TestForeignInstanceTreeIsKeptForTheCompanion(t *testing.T) {
	t.Parallel()

	runner := &perSocketRunner{answers: map[string][]byte{}, failing: map[string]bool{}}
	h, listed, foreign := twoInstanceHandler(t, runner)
	runner.answers[foreign] = mustMarshalWindows(t, fakeOSWindowsWithTab(7, "W:notes"))

	h.pruneForeignWindows(time.Now(), listed)
	if trees := otherTrees(h); len(trees[foreign]) != 1 || len(trees) != 1 {
		t.Fatalf("other trees = %+v; want the foreign instance's listing", trees)
	}

	runner.failing[foreign] = true
	h.pruneForeignWindows(time.Now(), listed)
	if trees := otherTrees(h); len(trees[foreign]) != 1 {
		t.Fatal("a failed listing dropped the instance's last tree")
	}

	h.stateMu.Lock()
	delete(h.store, 7)
	h.stateMu.Unlock()
	h.pruneForeignWindows(time.Now(), listed)
	if trees := otherTrees(h); len(trees) != 0 {
		t.Fatalf("other trees = %+v; want none once no window of another instance remains", trees)
	}
}
